package service

import (
	"errors"
	"testing"

	"github.com/austinokafor/geo-backend/internal/platform"
)

// ─── AggregateResults ─────────────────────────────────────────────────────────

func TestAggregateResults_Empty(t *testing.T) {
	got := AggregateResults(nil)
	if got.Mentioned || got.Position != 0 {
		t.Errorf("empty slice: expected zero-value result, got %+v", got)
	}
}

func TestAggregateResults_SingleResult(t *testing.T) {
	results := []platform.CitationResult{
		{Platform: "chatgpt", Mentioned: true, Position: 2},
	}
	got := AggregateResults(results)
	if !got.Mentioned {
		t.Error("single mentioned result: expected Mentioned=true")
	}
	if got.Position != 2 {
		t.Errorf("single result: expected Position=2, got %d", got.Position)
	}
}

func TestAggregateResults_MajorityVoteMentioned(t *testing.T) {
	// 2 of 3 say mentioned = true → majority → Mentioned=true
	results := []platform.CitationResult{
		{Mentioned: true, Position: 1},
		{Mentioned: true, Position: 2},
		{Mentioned: false, Position: 0},
	}
	got := AggregateResults(results)
	if !got.Mentioned {
		t.Error("majority vote: expected Mentioned=true when 2/3 are true")
	}
}

func TestAggregateResults_MajorityVoteNotMentioned(t *testing.T) {
	// 1 of 3 say mentioned = true → minority → Mentioned=false
	results := []platform.CitationResult{
		{Mentioned: true, Position: 1},
		{Mentioned: false, Position: 0},
		{Mentioned: false, Position: 0},
	}
	got := AggregateResults(results)
	if got.Mentioned {
		t.Error("majority vote: expected Mentioned=false when 1/3 are true")
	}
	if got.Position != 0 {
		t.Errorf("not mentioned: expected Position=0, got %d", got.Position)
	}
}

func TestAggregateResults_MedianPosition(t *testing.T) {
	// Two mentioned results at positions 1 and 3 → median is position at index n/2=1 → 3
	results := []platform.CitationResult{
		{Mentioned: true, Position: 1},
		{Mentioned: true, Position: 3},
	}
	got := AggregateResults(results)
	// After insertion sort [1,3], n=2, n/2=1 → vals[1]=3
	if got.Position != 3 {
		t.Errorf("median position: expected 3, got %d", got.Position)
	}
}

func TestAggregateResults_MentionedRequiresPositionGe1(t *testing.T) {
	// mentioned=true but no position supplied → should default to 1
	results := []platform.CitationResult{
		{Mentioned: true, Position: 0},
	}
	got := AggregateResults(results)
	if got.Mentioned && got.Position == 0 {
		t.Error("mentioned=true with Position=0 should be corrected to Position=1")
	}
}

func TestAggregateResults_PreservesCompetitorRichResult(t *testing.T) {
	// Base should be the result with the most competitors.
	results := []platform.CitationResult{
		{Platform: "chatgpt", Mentioned: true, Competitors: []platform.Competitor{{Name: "BrandA"}, {Name: "BrandB"}}},
		{Platform: "chatgpt", Mentioned: true, Competitors: []platform.Competitor{}},
	}
	got := AggregateResults(results)
	if len(got.Competitors) != 2 {
		t.Errorf("expected 2 competitors from richest result, got %d", len(got.Competitors))
	}
}

func TestAggregateResults_AverageCost(t *testing.T) {
	results := []platform.CitationResult{
		{CostUSD: 0.002, TokensIn: 100, TokensOut: 50},
		{CostUSD: 0.004, TokensIn: 200, TokensOut: 100},
	}
	got := AggregateResults(results)
	wantCost := 0.003
	if got.CostUSD != wantCost {
		t.Errorf("average cost: expected %.4f, got %.4f", wantCost, got.CostUSD)
	}
}

// ─── Median ───────────────────────────────────────────────────────────────────

func TestMedian_Empty(t *testing.T) {
	if Median(nil) != 0 {
		t.Error("empty slice should return 0")
	}
	if Median([]int{}) != 0 {
		t.Error("empty slice should return 0")
	}
}

func TestMedian_Single(t *testing.T) {
	if got := Median([]int{7}); got != 7 {
		t.Errorf("single element: expected 7, got %d", got)
	}
}

func TestMedian_Sorted(t *testing.T) {
	// [1,2,3] → n=3, n/2=1 → vals[1]=2
	if got := Median([]int{1, 2, 3}); got != 2 {
		t.Errorf("sorted [1,2,3]: expected 2, got %d", got)
	}
}

func TestMedian_Unsorted(t *testing.T) {
	// [3,1,2] → sorted [1,2,3] → n/2=1 → 2
	if got := Median([]int{3, 1, 2}); got != 2 {
		t.Errorf("unsorted [3,1,2]: expected 2, got %d", got)
	}
}

func TestMedian_DoesNotMutateInput(t *testing.T) {
	input := []int{3, 1, 2}
	_ = Median(input)
	if input[0] != 3 || input[1] != 1 || input[2] != 2 {
		t.Error("Median should not mutate the input slice")
	}
}

// ─── IsRateLimitErr ───────────────────────────────────────────────────────────

func TestIsRateLimitErr_Nil(t *testing.T) {
	if IsRateLimitErr(nil) {
		t.Error("nil error should not be a rate limit error")
	}
}

func TestIsRateLimitErr_429(t *testing.T) {
	if !IsRateLimitErr(errors.New("HTTP 429 Too Many Requests")) {
		t.Error("error containing '429' should be a rate limit error")
	}
}

func TestIsRateLimitErr_RateLimited(t *testing.T) {
	if !IsRateLimitErr(errors.New("rate limited by upstream")) {
		t.Error("error containing 'rate limited' should be a rate limit error")
	}
}

func TestIsRateLimitErr_OtherError(t *testing.T) {
	if IsRateLimitErr(errors.New("connection refused")) {
		t.Error("unrelated error should not be a rate limit error")
	}
	if IsRateLimitErr(errors.New("timeout after 30s")) {
		t.Error("timeout error should not be a rate limit error")
	}
}

// ─── Sentiment weight constants ───────────────────────────────────────────────

func TestSentimentWeights(t *testing.T) {
	// Positive must be highest, then neutral, then negative must be zero.
	if SentimentWeightPositive <= SentimentWeightNeutral {
		t.Errorf("positive weight (%v) must be > neutral weight (%v)",
			SentimentWeightPositive, SentimentWeightNeutral)
	}
	if SentimentWeightNeutral <= SentimentWeightNegative {
		t.Errorf("neutral weight (%v) must be > negative weight (%v)",
			SentimentWeightNeutral, SentimentWeightNegative)
	}
	if SentimentWeightNegative != 0.0 {
		t.Errorf("negative weight must be 0.0, got %v", SentimentWeightNegative)
	}
	if SentimentWeightPositive != 1.0 {
		t.Errorf("positive weight must be 1.0, got %v", SentimentWeightPositive)
	}
}

// ─── ScanResult ───────────────────────────────────────────────────────────────

func TestScanResult_ZeroValue(t *testing.T) {
	var r ScanResult
	if r.QueriesRun != 0 || r.TotalMentions != 0 || r.DurationMs != 0 {
		t.Error("zero-value ScanResult should have all zero fields")
	}
	if r.Platforms != nil {
		t.Error("zero-value ScanResult.Platforms should be nil")
	}
}
