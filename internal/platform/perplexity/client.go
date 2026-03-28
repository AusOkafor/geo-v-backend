package perplexity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/austinokafor/geo-backend/internal/platform"
)

const (
	model    = "sonar"
	endpoint = "https://api.perplexity.ai/chat/completions"
	timeout  = 45 * time.Second

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

func (c *Client) Name() string                    { return "perplexity" }
func (c *Client) InputCostPer1kTokens() float64   { return inputCostPer1k }
func (c *Client) OutputCostPer1kTokens() float64  { return outputCostPer1k }

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
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
	Mentioned   bool                  `json:"mentioned"`
	Position    int                   `json:"position"`
	Sentiment   string                `json:"sentiment"`
	Competitors []platform.Competitor `json:"competitors"`
}

func systemPrompt(brandName string) string {
	return fmt.Sprintf(`You are an AI search visibility analyst. When answering the user's question, note whether the brand "%s" is mentioned. After answering, return a JSON object:
{"mentioned": bool, "position": int, "sentiment": "positive"|"neutral"|"negative"|"", "competitors": [{"name": string, "position": int}]}
Where "position" is 1 if the brand is the first recommendation, 0 if not mentioned.`, brandName)
}

func (c *Client) Query(ctx context.Context, brandName, prompt string) (platform.CitationResult, error) {
	start := time.Now()

	reqBody := chatRequest{
		Model: model,
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
		return platform.CitationResult{}, fmt.Errorf("perplexity: HTTP %d", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("perplexity: decode: %w", err)
	}

	raw := ""
	if len(chatResp.Choices) > 0 {
		raw = chatResp.Choices[0].Message.Content
	}

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
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return platform.CitationResult{
			Mentioned:   s.Mentioned,
			Position:    s.Position,
			Sentiment:   s.Sentiment,
			Competitors: s.Competitors,
		}
	}
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
