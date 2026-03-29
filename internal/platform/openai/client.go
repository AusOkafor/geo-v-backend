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
	model    = "gpt-4o-mini"
	endpoint = "https://api.openai.com/v1/chat/completions"
	timeout  = 45 * time.Second

	inputCostPer1k  = 0.15 / 1000 // $0.15 per 1M tokens
	outputCostPer1k = 0.60 / 1000 // $0.60 per 1M tokens
)

// Client is the OpenAI implementation of platform.AIClient.
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

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	MaxTokens      int            `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
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

	reqBody := chatRequest{
		Model:     model,
		MaxTokens: 800,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt(brandName)},
			{Role: "user", Content: prompt},
		},
		ResponseFormat: responseFormat{Type: "json_object"},
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
		return platform.CitationResult{}, fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return platform.CitationResult{}, fmt.Errorf("openai: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("openai: decode: %w", err)
	}

	raw := ""
	if len(chatResp.Choices) > 0 {
		raw = chatResp.Choices[0].Message.Content
	}

	slog.Debug("openai: raw response", "raw", raw)

	result := parseResponse(raw, brandName)
	result.Platform = c.Name()
	result.Query = prompt
	result.TokensIn = chatResp.Usage.PromptTokens
	result.TokensOut = chatResp.Usage.CompletionTokens
	result.CostUSD = platform.CalcCost(c, result.TokensIn, result.TokensOut)
	result.Duration = time.Since(start)
	result.RawResponse = raw
	return result, nil
}

func parseResponse(raw, brandName string) platform.CitationResult {
	var s structuredResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &s); err == nil {
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
		}
	}

	// Fallback: string search
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
