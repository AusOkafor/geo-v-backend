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
