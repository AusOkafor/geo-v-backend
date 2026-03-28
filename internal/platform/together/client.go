package together

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
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
	Stop      []string      `json:"stop,omitempty"`
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
		Model:     c.model,
		MaxTokens: 400,
		Stop:      []string{"}\n", "} \n", "}\r\n"},
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

	slog.Debug("together: raw response", "platform", c.name, "raw", raw)

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
	parsed := false

	// Try full response as JSON first (model instructed to output JSON only)
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &s); err == nil && (s.Mentioned || len(s.Competitors) > 0) {
		parsed = true
	}
	// Fallback: find the last { in case model prepended text
	if !parsed {
		if lastBrace := strings.LastIndex(raw, "{"); lastBrace >= 0 {
			if err := json.Unmarshal([]byte(raw[lastBrace:]), &s); err == nil {
				parsed = true
			}
		}
	}

	if parsed {
		// Validate mentioned against the actual answer text — the model sometimes
		// returns mentioned:true even when the brand doesn't appear in the answer.
		brandLower := strings.ToLower(brandName)
		mentioned := strings.Contains(strings.ToLower(s.Answer), brandLower)

		position := s.Position
		// Fix position=0 when mentioned: model sets mentioned:true but omits the
		// brand from the competitors array, leaving position at its zero value.
		// Search competitors for a name match first, then fall back to rank 1.
		if mentioned && position == 0 {
			for _, comp := range s.Competitors {
				if strings.EqualFold(comp.Name, brandName) {
					position = comp.Position
					break
				}
			}
			if position == 0 {
				position = 1 // mentioned but rank unknown — treat as top mention
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

	// Last resort: simple string search
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
