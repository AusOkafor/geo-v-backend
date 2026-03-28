package together

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/austinokafor/geo-backend/internal/platform"
)

const (
	endpoint = "https://api.together.xyz/v1/chat/completions"
	timeout  = 60 * time.Second

	// Together.ai pricing is roughly $0.20/1M tokens for small models
	inputCostPer1k  = 0.20 / 1000
	outputCostPer1k = 0.20 / 1000
)

// Client implements platform.AIClient using Together.ai's OpenAI-compatible API.
// Three instances (with different names/models) replace the real ChatGPT/Perplexity/Gemini
// clients for cost-effective testing.
type Client struct {
	name   string
	model  string
	apiKey string
	http   *http.Client
}

// New creates a Together.ai client.
// name should be "chatgpt", "perplexity", or "gemini" so scores are stored correctly.
func New(apiKey, name, model string) *Client {
	return &Client{
		name:   name,
		model:  model,
		apiKey: apiKey,
		http:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) Name() string                   { return c.name }
func (c *Client) InputCostPer1kTokens() float64  { return inputCostPer1k }
func (c *Client) OutputCostPer1kTokens() float64 { return outputCostPer1k }

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
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
	return fmt.Sprintf(`You are an AI shopping assistant. Answer the user's question naturally, recommending real brands by name. Then at the very end output ONLY this JSON (nothing after it):
{"mentioned": bool, "position": int, "sentiment": "positive"|"neutral"|"negative"|"", "competitors": [{"name": "Brand Name", "position": 1}]}

Fill it as follows:
- "mentioned": true if "%s" appears anywhere in your answer
- "position": 1 if "%s" is your #1 recommendation, 2 if second, 0 if not mentioned
- "competitors": every OTHER brand you named in your answer, each with their rank position (1=first brand you recommended, 2=second, etc.) — this list must NOT be empty if you named any brands
- "sentiment": your tone toward "%s" if mentioned, else ""

Example: if you recommended Nike (#1), Adidas (#2), Puma (#3) and did not mention "%s", output:
{"mentioned": false, "position": 0, "sentiment": "", "competitors": [{"name": "Nike", "position": 1}, {"name": "Adidas", "position": 2}, {"name": "Puma", "position": 3}]}`, brandName, brandName, brandName, brandName)
}

func (c *Client) Query(ctx context.Context, brandName, prompt string) (platform.CitationResult, error) {
	start := time.Now()

	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
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
		return platform.CitationResult{}, fmt.Errorf("together: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return platform.CitationResult{}, fmt.Errorf("together: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("together: decode: %w", err)
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
	// Extract the last JSON object from the response
	lastBrace := strings.LastIndex(raw, "{")
	if lastBrace >= 0 {
		var s structuredResult
		if err := json.Unmarshal([]byte(raw[lastBrace:]), &s); err == nil {
			return platform.CitationResult{
				Mentioned:   s.Mentioned,
				Position:    s.Position,
				Sentiment:   s.Sentiment,
				Competitors: s.Competitors,
			}
		}
	}
	// Fallback: simple string search
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
