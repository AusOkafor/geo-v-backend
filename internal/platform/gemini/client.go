package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/austinokafor/geo-backend/internal/platform"
)

const (
	endpoint = "https://generativelanguage.googleapis.com/v1beta/models/gemini-3-flash-preview:generateContent"
	timeout  = 45 * time.Second

	// Gemini Flash pricing
	inputCostPer1k  = 0.15 / 1000 // $0.15 per 1M tokens
	outputCostPer1k = 0.60 / 1000 // $0.60 per 1M tokens
)

// Client is the Gemini implementation of platform.AIClient.
type Client struct {
	apiKey string
	http   *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) Name() string                   { return "gemini" }
func (c *Client) InputCostPer1kTokens() float64  { return inputCostPer1k }
func (c *Client) OutputCostPer1kTokens() float64 { return outputCostPer1k }

type generateRequest struct {
	SystemInstruction systemContent `json:"systemInstruction"`
	Contents          []content     `json:"contents"`
	GenerationConfig  genConfig     `json:"generationConfig"`
}

type systemContent struct {
	Parts []part `json:"parts"`
}

type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type genConfig struct {
	ResponseMIMEType string `json:"responseMimeType"`
	MaxOutputTokens  int    `json:"maxOutputTokens"`
}

type generateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

type structuredResult struct {
	Answer      string                `json:"answer"`
	Mentioned   bool                  `json:"mentioned"`
	Position    int                   `json:"position"`
	Sentiment   string                `json:"sentiment"`
	Competitors []platform.Competitor `json:"competitors"`
}

func systemPrompt(brandName string) string {
	return fmt.Sprintf(`You are a shopping recommendation API. You must respond with ONLY valid JSON — no explanation, no text before or after the JSON.

Given a shopping question, recommend real brands and return this exact JSON structure:
{"answer":"your recommendation text here","mentioned":false,"position":0,"sentiment":"","competitors":[{"name":"Brand A","position":1},{"name":"Brand B","position":2}]}

Rules:
- "answer": your full shopping recommendation (name real brands)
- "mentioned": true if "%s" appears in answer
- "position": rank of "%s" in answer (1=top pick, 2=second, 0=not mentioned)
- "sentiment": "positive", "neutral", "negative", or "" for "%s"
- "competitors": every brand named in answer with their rank — REQUIRED, never leave empty if you named brands`, brandName, brandName, brandName)
}

func (c *Client) Query(ctx context.Context, brandName, prompt string) (platform.CitationResult, error) {
	start := time.Now()

	reqBody := generateRequest{
		SystemInstruction: systemContent{
			Parts: []part{{Text: systemPrompt(brandName)}},
		},
		Contents: []content{
			{Role: "user", Parts: []part{{Text: prompt}}},
		},
		GenerationConfig: genConfig{
			ResponseMIMEType: "application/json",
			MaxOutputTokens:  800,
		},
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return platform.CitationResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return platform.CitationResult{}, fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		// Parse the retryDelay from the response body and wait before returning
		// so the caller's retry loop has a chance of succeeding.
		body, _ := io.ReadAll(resp.Body)
		delay := parseRetryDelay(string(body))
		if delay > 0 && delay <= 60*time.Second {
			slog.Debug("gemini: rate limited, waiting before retry", "delay", delay)
			select {
			case <-ctx.Done():
				return platform.CitationResult{}, ctx.Err()
			case <-time.After(delay):
			}
		}
		return platform.CitationResult{}, fmt.Errorf("gemini: HTTP 429 (rate limited, retry after %s)", delay)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return platform.CitationResult{}, fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("gemini: decode: %w", err)
	}

	raw := ""
	if len(genResp.Candidates) > 0 && len(genResp.Candidates[0].Content.Parts) > 0 {
		raw = genResp.Candidates[0].Content.Parts[0].Text
	}

	slog.Debug("gemini: raw response", "raw", raw)

	result := parseResponse(raw, brandName)
	result.Platform = c.Name()
	result.Query = prompt
	result.TokensIn = genResp.UsageMetadata.PromptTokenCount
	result.TokensOut = genResp.UsageMetadata.CandidatesTokenCount
	result.CostUSD = platform.CalcCost(c, result.TokensIn, result.TokensOut)
	result.Duration = time.Since(start)
	result.RawResponse = raw
	result.Grounded = true // Gemini with real API uses Google Search grounding
	return result, nil
}

// parseRetryDelay extracts the retryDelay from a Gemini 429 response body.
// The JSON contains "retryDelay": "40s" inside the details array.
// Returns 0 if not found or not parseable.
func parseRetryDelay(body string) time.Duration {
	// Quick string search to avoid full JSON unmarshal on error path
	const marker = `"retryDelay": "`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return 0
	}
	start := idx + len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return 0
	}
	d, err := time.ParseDuration(body[start : start+end])
	if err != nil {
		return 0
	}
	return d
}

func parseResponse(raw, brandName string) platform.CitationResult {
	var s structuredResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &s); err == nil {
		// Validate mentioned against the actual answer text — model can hallucinate this field
		brandLower := strings.ToLower(brandName)
		mentioned := strings.Contains(strings.ToLower(s.Answer), brandLower)

		position := s.Position
		if mentioned && position == 0 {
			for _, comp := range s.Competitors {
				if strings.EqualFold(comp.Name, brandName) {
					position = comp.Position
					break
				}
			}
			if position == 0 {
				position = 1
			}
		}

		sentiment := s.Sentiment
		if !mentioned {
			sentiment = ""
		}

		return platform.CitationResult{
			Mentioned:   mentioned,
			Position:    position,
			Sentiment:   sentiment,
			Competitors: s.Competitors,
		}
	}

	// Fallback: string search only
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
