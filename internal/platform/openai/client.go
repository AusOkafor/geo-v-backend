package openai

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
	model             = "gpt-4o-mini"
	responsesEndpoint = "https://api.openai.com/v1/responses"
	timeout           = 90 * time.Second // web search adds latency

	inputCostPer1k     = 0.15 / 1000 // $0.15 per 1M tokens
	outputCostPer1k    = 0.60 / 1000 // $0.60 per 1M tokens
	searchCostPerQuery = 0.025        // $25 per 1000 searches = $0.025/call
)

// Client is the OpenAI implementation of platform.AIClient.
// It uses the Responses API with web_search_preview for real web grounding.
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

func (c *Client) Name() string                   { return "chatgpt" }
func (c *Client) InputCostPer1kTokens() float64  { return inputCostPer1k }
func (c *Client) OutputCostPer1kTokens() float64 { return outputCostPer1k }

type responsesRequest struct {
	Model        string `json:"model"`
	Instructions string `json:"instructions"`
	Input        string `json:"input"`
	Tools        []tool `json:"tools"`
}

type tool struct {
	Type string `json:"type"`
}

type responsesResponse struct {
	Output []outputItem `json:"output"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type outputItem struct {
	Type    string        `json:"type"`
	Content []contentItem `json:"content,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type structuredResult struct {
	Answer      string                `json:"answer"`
	Mentioned   bool                  `json:"mentioned"`
	Position    int                   `json:"position"`
	Sentiment   string                `json:"sentiment"`
	Competitors []platform.Competitor `json:"competitors"`
}

func systemPrompt(brandName string) string {
	return fmt.Sprintf(`You are a shopping research assistant. Use web search to find current brand recommendations, then respond with ONLY valid JSON.

Return this exact JSON structure based on what you find in web search results:
{"answer":"your recommendation based on search results","mentioned":false,"position":0,"sentiment":"","competitors":[{"name":"Brand A","position":1},{"name":"Brand B","position":2}]}

Rules:
- Search for real brands before answering — use only what you find in current web results
- "answer": your full shopping recommendation citing brands from search
- "mentioned": true if "%s" appears in answer
- "position": rank of "%s" in answer (1=top pick, 2=second, 0=not mentioned)
- "sentiment": "positive", "neutral", "negative", or "" for "%s"
- "competitors": brands you found in search results with their rank — omit if you found none`, brandName, brandName, brandName)
}

func (c *Client) Query(ctx context.Context, brandName, prompt string) (platform.CitationResult, error) {
	start := time.Now()

	reqBody := responsesRequest{
		Model:        model,
		Instructions: systemPrompt(brandName),
		Input:        prompt,
		Tools:        []tool{{Type: "web_search_preview"}},
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responsesEndpoint, bytes.NewReader(payload))
	if err != nil {
		return platform.CitationResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return platform.CitationResult{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return platform.CitationResult{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var apiResp responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("openai: decode: %w", err)
	}

	// Extract text from the message output item (skip web_search_call items)
	raw := ""
	for _, item := range apiResp.Output {
		if item.Type == "message" {
			for _, ct := range item.Content {
				if ct.Type == "output_text" {
					raw = ct.Text
					break
				}
			}
			break
		}
	}

	slog.Debug("openai: raw response", "raw", raw)

	result := parseResponse(raw, brandName)
	result.Platform = c.Name()
	result.Query = prompt
	result.TokensIn = apiResp.Usage.InputTokens
	result.TokensOut = apiResp.Usage.OutputTokens
	result.CostUSD = platform.CalcCost(c, result.TokensIn, result.TokensOut) + searchCostPerQuery
	result.Duration = time.Since(start)
	result.RawResponse = raw
	result.Grounded = true // Responses API with web_search_preview = real web grounding
	return result, nil
}

func parseResponse(raw, brandName string) platform.CitationResult {
	// The model may wrap JSON in markdown fences when responding after web search
	candidate := extractJSON(strings.TrimSpace(raw))

	var s structuredResult
	if err := json.Unmarshal([]byte(candidate), &s); err == nil {
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
