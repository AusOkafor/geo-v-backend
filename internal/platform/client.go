package platform

import (
	"context"
	"time"
)

// CitationResult holds the outcome of a single AI platform query.
type CitationResult struct {
	Platform    string
	Query       string
	Mentioned   bool
	Position    int    // 1 = first mention, 0 = not mentioned
	Sentiment   string // "positive" | "neutral" | "negative" | ""
	Competitors []Competitor
	TokensIn    int
	TokensOut   int
	CostUSD     float64
	Duration    time.Duration
	RawResponse string
	// AnswerText is the human-readable recommendation text extracted from the
	// structured JSON response. Stored separately so the dashboard can surface
	// real AI answers without re-parsing the raw JSON blob.
	AnswerText string
	// Grounded is true when the result came from a web-search-grounded API
	// (OpenAI Responses API with web_search_preview, Perplexity sonar).
	// False means model-memory only (Together.ai, ungrounded chat completions).
	Grounded bool
	// ModelVersion is the specific model identifier returned by or configured for
	// this platform (e.g. "gpt-4o-mini", "sonar", "gemini-2.5-flash").
	// Stored alongside the response so auditors can reproduce the exact model state.
	ModelVersion string
}

// Competitor is a brand cited instead of (or alongside) the merchant.
type Competitor struct {
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// AIClient is implemented by all three platform scanners.
type AIClient interface {
	Name() string
	Query(ctx context.Context, brandName, prompt string) (CitationResult, error)
	InputCostPer1kTokens() float64
	OutputCostPer1kTokens() float64
}

// CalcCost returns the USD cost for a single request given token counts.
// InputCostPer1kTokens / OutputCostPer1kTokens are cost in USD per 1 000 tokens.
func CalcCost(client AIClient, tokIn, tokOut int) float64 {
	return float64(tokIn)/1000*client.InputCostPer1kTokens() +
		float64(tokOut)/1000*client.OutputCostPer1kTokens()
}
