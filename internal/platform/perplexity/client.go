package perplexity

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
	model    = "sonar"
	endpoint = "https://api.perplexity.ai/chat/completions"
	timeout  = 60 * time.Second // sonar does live web search, needs more time

	inputCostPer1k  = 1.00 / 1000
	outputCostPer1k = 1.00 / 1000
)

// Client is the Perplexity implementation of platform.AIClient.
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

func (c *Client) Name() string                   { return "perplexity" }
func (c *Client) InputCostPer1kTokens() float64  { return inputCostPer1k }
func (c *Client) OutputCostPer1kTokens() float64 { return outputCostPer1k }

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
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

Given a shopping question, search the web for real brands and return this exact JSON structure:
{"answer":"your recommendation text here","mentioned":false,"position":0,"sentiment":"","competitors":[{"name":"Brand A","position":1},{"name":"Brand B","position":2}]}

Rules:
- "answer": your full shopping recommendation (name real brands you found)
- "mentioned": true if "%s" appears in answer
- "position": rank of "%s" in answer (1=top pick, 2=second, 0=not mentioned)
- "sentiment": "positive", "neutral", "negative", or "" for "%s"
- "competitors": every brand named in answer with their rank — REQUIRED, never leave empty if you named brands`, brandName, brandName, brandName)
}

func (c *Client) Query(ctx context.Context, brandName, prompt string) (platform.CitationResult, error) {
	start := time.Now()

	reqBody := chatRequest{
		Model:     model,
		MaxTokens: 800,
		Messages: []message{
			{Role: "system", Content: systemPrompt(brandName)},
			{Role: "user", Content: prompt},
		},
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return platform.CitationResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return platform.CitationResult{}, fmt.Errorf("perplexity: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return platform.CitationResult{}, fmt.Errorf("perplexity: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("perplexity: decode: %w", err)
	}

	raw := ""
	if len(chatResp.Choices) > 0 {
		raw = chatResp.Choices[0].Message.Content
	}

	slog.Debug("perplexity: raw response", "raw", raw)

	result := parseResponse(raw, brandName)
	result.Platform = c.Name()
	result.Query = prompt
	result.TokensIn = chatResp.Usage.PromptTokens
	result.TokensOut = chatResp.Usage.CompletionTokens
	result.CostUSD = platform.CalcCost(c, result.TokensIn, result.TokensOut)
	result.Duration = time.Since(start)
	result.RawResponse = raw
	result.Grounded = true // Perplexity sonar = real-time web search grounded
	result.ModelVersion = model
	return result, nil
}

// extractJSON pulls the first complete JSON object out of a mixed prose+JSON response.
// Perplexity's sonar model sometimes wraps the JSON in markdown or prose.
func extractJSON(raw string) string {
	start := strings.Index(raw, "{")
	if start < 0 {
		return raw
	}
	end := strings.LastIndex(raw, "}")
	if end <= start {
		return raw
	}
	return raw[start : end+1]
}

func parseResponse(raw, brandName string) platform.CitationResult {
	// sonar may wrap JSON in markdown fences or prose — extract the JSON object
	candidate := extractJSON(strings.TrimSpace(raw))

	var s structuredResult
	if err := json.Unmarshal([]byte(candidate), &s); err == nil {
		// Validate mentioned against the actual answer text
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
			AnswerText:  s.Answer,
		}
	}

	// Fallback: string search
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
