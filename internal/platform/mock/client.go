// Package mock provides a zero-cost AIClient for development and staging.
// Set MOCK_AI=true to use these instead of real API clients.
package mock

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/austinokafor/geo-backend/internal/platform"
)

var knownCompetitors = []string{
	"Bellroy", "Fossil", "Herschel", "Coach", "MVMT",
	"Travelsmith", "Saddleback Leather", "Radley", "Filson", "Fjällräven",
}

// Client implements platform.AIClient without making any network calls.
type Client struct {
	name string
}

func New(name string) *Client { return &Client{name: name} }

func (c *Client) Name() string                   { return c.name }
func (c *Client) InputCostPer1kTokens() float64  { return 0 }
func (c *Client) OutputCostPer1kTokens() float64 { return 0 }

// Query returns deterministic fake citation data seeded from the query text.
// ~20% mention rate simulates a new/unknown brand.
func (c *Client) Query(_ context.Context, brandName, prompt string) (platform.CitationResult, error) {
	h := fnv.New32a()
	h.Write([]byte(c.name + "|" + brandName + "|" + prompt))
	seed := h.Sum32()

	// ~20% chance of being mentioned
	mentioned := seed%5 == 0
	position := 0
	if mentioned {
		position = int(seed%4) + 1
	}

	sentiment := "neutral"
	if mentioned && seed%3 == 0 {
		sentiment = "positive"
	}

	// 2–3 competitors, no duplicates, no self-mention
	numComps := 2 + int(seed%2)
	comps := make([]platform.Competitor, 0, numComps)
	offset := int(seed % uint32(len(knownCompetitors)))
	for i := 0; i < len(knownCompetitors) && len(comps) < numComps; i++ {
		idx := (offset + i) % len(knownCompetitors)
		name := knownCompetitors[idx]
		if name == brandName {
			continue
		}
		comps = append(comps, platform.Competitor{Name: name, Position: len(comps) + 1})
	}

	return platform.CitationResult{
		Platform:    c.name,
		Query:       prompt,
		Mentioned:   mentioned,
		Position:    position,
		Sentiment:   sentiment,
		Competitors: comps,
		TokensIn:    350 + int(seed%200),
		TokensOut:   120 + int(seed%80),
		CostUSD:     0,
		Duration:    time.Duration(40+seed%80) * time.Millisecond,
		Grounded:    false, // mock client never does real web search
	}, nil
}
