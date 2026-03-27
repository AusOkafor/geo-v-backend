package fix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	claudeEndpoint = "https://api.anthropic.com/v1/messages"
	claudeModel    = "claude-haiku-4-5-20251001"
	claudeVersion  = "2023-06-01"
	fixTimeout     = 60 * time.Second
)

// FixType enumerates the kinds of fixes Claude can generate.
type FixType string

const (
	FixDescription FixType = "description"
	FixFAQ         FixType = "faq"
	FixSchema      FixType = "schema"
	FixListing     FixType = "listing"
)

// EstImpact returns the estimated visibility improvement for each fix type.
func EstImpact(t FixType) int {
	switch t {
	case FixDescription:
		return 23
	case FixFAQ:
		return 18
	case FixListing:
		return 12
	case FixSchema:
		return 8
	}
	return 5
}

// GenerateInput contains everything Claude needs to generate a fix.
type GenerateInput struct {
	BrandName          string
	Category           string
	CurrentDescription string
	Tags               []string
	Competitors        []string
	FixType            FixType
}

// GenerateResult is the parsed output from Claude.
type GenerateResult struct {
	Generated  json.RawMessage
	Title      string
	Explanation string
}

// claudeRequest is the Anthropic Messages API request body.
type claudeRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system"`
	Messages  []claudeMessage  `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// Generatable is implemented by both Generator (real) and MockGenerator (dev/staging).
type Generatable interface {
	Generate(ctx context.Context, in GenerateInput) (*GenerateResult, error)
}

// Generator calls the Claude API to produce optimization fixes.
type Generator struct {
	apiKey string
	http   *http.Client
}

func NewGenerator(apiKey string) *Generator {
	return &Generator{
		apiKey: apiKey,
		http:   &http.Client{Timeout: fixTimeout},
	}
}

const systemPrompt = `You are a GEO (Generative Engine Optimization) expert. Your job is to rewrite or generate content that makes e-commerce brands more likely to be cited when shoppers ask AI assistants for product recommendations. Return ONLY valid JSON — no markdown, no prose.`

func (g *Generator) Generate(ctx context.Context, in GenerateInput) (*GenerateResult, error) {
	userPrompt := buildPrompt(in)

	reqBody := claudeRequest{
		Model:     claudeModel,
		MaxTokens: 2048,
		System:    systemPrompt,
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", g.apiKey)
	req.Header.Set("anthropic-version", claudeVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fix: claude call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fix: claude HTTP %d", resp.StatusCode)
	}

	var claudeResp claudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return nil, fmt.Errorf("fix: decode claude response: %w", err)
	}

	raw := ""
	if len(claudeResp.Content) > 0 {
		raw = claudeResp.Content[0].Text
	}

	return parseResult(raw, in.FixType)
}

func buildPrompt(in GenerateInput) string {
	switch in.FixType {
	case FixDescription:
		return fmt.Sprintf(`Generate an optimized product description for brand "%s" (category: %s).

Current description: %s
Tags: %v
Top competitors AI cites instead: %v

Return JSON: {"title": "Fix title", "explanation": "Why this improves AI visibility", "generated": {"description": "800-1000 word HTML description with brand name 3-4x, semantic headings, embedded FAQ, material/dimension specifics"}}`,
			in.BrandName, in.Category, in.CurrentDescription, in.Tags, in.Competitors)

	case FixFAQ:
		return fmt.Sprintf(`Generate 10 buyer-intent FAQ Q&A pairs for brand "%s" (category: %s) that match how buyers ask ChatGPT, Perplexity, and Gemini for recommendations.

Return JSON: {"title": "Fix title", "explanation": "Why this improves AI visibility", "generated": {"faqs": [{"question": "...", "answer": "..."}]}}`,
			in.BrandName, in.Category)

	case FixSchema:
		return fmt.Sprintf(`Generate JSON-LD Product schema markup for brand "%s" (category: %s).

Return JSON: {"title": "Fix title", "explanation": "Why this improves AI visibility", "generated": {"schema": "JSON-LD string"}}`,
			in.BrandName, in.Category)

	case FixListing:
		return fmt.Sprintf(`Generate directory listing content for brand "%s" (category: %s) suitable for submission to Wirecutter, GQ, and niche directories.

Return JSON: {"title": "Fix title", "explanation": "Why this improves AI visibility", "generated": {"listing": "..."}}`,
			in.BrandName, in.Category)
	}

	return fmt.Sprintf(`Improve AI visibility for brand "%s". Return JSON with title, explanation, and generated fields.`, in.BrandName)
}

type resultJSON struct {
	Title       string          `json:"title"`
	Explanation string          `json:"explanation"`
	Generated   json.RawMessage `json:"generated"`
}

func parseResult(raw string, _ FixType) (*GenerateResult, error) {
	var r resultJSON
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		// Return raw as generated if JSON parsing fails
		return &GenerateResult{
			Title:      "AI-generated optimization",
			Explanation: "Generated by Claude",
			Generated:  json.RawMessage(`{"raw":` + jsonString(raw) + `}`),
		}, nil
	}
	return &GenerateResult{
		Title:       r.Title,
		Explanation: r.Explanation,
		Generated:   r.Generated,
	}, nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
