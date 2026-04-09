package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/platform"
	"github.com/austinokafor/geo-backend/internal/query"
	"github.com/austinokafor/geo-backend/internal/store"
)

// Sentiment weights — document the values embedded in UpsertVisibilityScores SQL.
const (
	SentimentWeightPositive = 1.0
	SentimentWeightNeutral  = 0.5
	SentimentWeightNegative = 0.0
)

// ScanResult contains a summary of a completed scan run.
type ScanResult struct {
	QueriesRun    int
	TotalMentions int
	Platforms     []string
	DurationMs    int64
}

// ScanService handles all AI visibility scan business logic — executing queries
// against AI platforms, persisting results, and providing read access to scan data.
type ScanService struct {
	db          *pgxpool.Pool
	clients     []platform.AIClient
	riverClient *river.Client[pgx.Tx] // nil in API-only mode (read path only)
}

func NewScanService(db *pgxpool.Pool, clients []platform.AIClient, riverClient *river.Client[pgx.Tx]) *ScanService {
	return &ScanService{db: db, clients: clients, riverClient: riverClient}
}

// ─── Scan execution ───────────────────────────────────────────────────────────

// RunScan executes a full AI visibility scan for a merchant.
// Queries all configured AI platforms, stores citation records and costs, updates
// visibility scores, and enqueues fix generation when the scan succeeds.
func (s *ScanService) RunScan(ctx context.Context, merchantID int64) (*ScanResult, error) {
	start := time.Now()

	merchant, err := store.GetMerchant(ctx, s.db, merchantID)
	if err != nil {
		return nil, fmt.Errorf("scan: load merchant %d: %w", merchantID, err)
	}
	if !merchant.Active {
		slog.Info("scan: skipped (inactive)", "merchant_id", merchantID)
		return &ScanResult{}, nil
	}

	// Cost guardrail: skip scan if monthly spend exceeds 60% of plan revenue.
	monthlyCost, err := store.GetMonthlyCostByMerchant(ctx, s.db, merchantID)
	if err == nil && store.ExceedsGuardrail(monthlyCost, merchant.Plan) {
		slog.Warn("scan skipped: cost guardrail exceeded",
			"merchant_id", merchantID,
			"plan", merchant.Plan,
			"monthly_cost_usd", monthlyCost,
		)
		return &ScanResult{}, nil
	}

	queries := query.Generate(merchant.Category, merchant.BrandName)

	result := &ScanResult{}
	platformSeen := map[string]bool{}

	for _, q := range queries {
		for _, client := range s.clients {
			results, err := runWithRetries(ctx, client, merchant.BrandName, q.Text, 1)
			if err != nil {
				slog.Warn("scan: platform failed, skipping",
					"merchant_id", merchantID,
					"platform", client.Name(),
					"query", q.Text,
					"err", err,
				)
				continue
			}

			cr := aggregateResults(results)
			cr.Query = q.Text

			if err := store.InsertCitationRecord(ctx, s.db, merchantID, string(q.QueryType), cr); err != nil {
				return nil, fmt.Errorf("scan: insert citation: %w", err)
			}
			if err := store.UpsertScanCost(ctx, s.db, merchantID, cr.Platform, cr.TokensIn, cr.TokensOut, cr.CostUSD); err != nil {
				return nil, fmt.Errorf("scan: upsert cost: %w", err)
			}
			if err := store.StoreCompetitorMentions(ctx, s.db, merchantID, cr); err != nil {
				slog.Warn("scan: failed to store competitor mentions (non-fatal)", "err", err)
			}

			if cr.Mentioned {
				result.TotalMentions++
			}
			platformSeen[cr.Platform] = true
			result.QueriesRun++

			// Rate limit: 500ms between platform calls.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}

	for p := range platformSeen {
		result.Platforms = append(result.Platforms, p)
	}
	result.DurationMs = time.Since(start).Milliseconds()

	// Aggregate citation_records → visibility_scores.
	if err := store.UpsertVisibilityScores(ctx, s.db, merchantID); err != nil {
		return nil, err
	}

	// Enqueue fix generation — FixGenerationWorker is idempotent (skips existing types).
	if s.riverClient != nil {
		if _, err := s.riverClient.Insert(ctx, scanFixGenerationJobArgs{MerchantID: merchantID}, fixGenInsertOpts); err != nil {
			slog.Warn("scan: failed to enqueue fix generation", "merchant_id", merchantID, "err", err)
		}
	}

	return result, nil
}

// ─── Read methods ─────────────────────────────────────────────────────────────

// GetVisibilityScores returns the latest visibility score per platform within N days.
func (s *ScanService) GetVisibilityScores(ctx context.Context, merchantID int64, days int) ([]store.VisibilityScore, error) {
	return store.GetVisibilityScores(ctx, s.db, merchantID, days)
}

// GetDailyScores returns per-day visibility for the trend chart.
func (s *ScanService) GetDailyScores(ctx context.Context, merchantID int64, days int) ([]store.DailyScore, error) {
	return store.GetDailyScores(ctx, s.db, merchantID, days)
}

// GetCompetitors returns scored, filtered competitors for a merchant.
func (s *ScanService) GetCompetitors(ctx context.Context, merchantID int64) ([]store.ScoredCompetitor, error) {
	return store.GetCompetitors(ctx, s.db, merchantID)
}

// GetCompetitorGaps returns competitors that most often beat the merchant.
func (s *ScanService) GetCompetitorGaps(ctx context.Context, merchantID int64) ([]store.CompetitorGapEntry, error) {
	return store.GetCompetitorGaps(ctx, s.db, merchantID)
}

// UpsertVisibilityScores re-aggregates today's citation records into visibility scores.
// Called by handlers to ensure scores are fresh before serving them.
func (s *ScanService) UpsertVisibilityScores(ctx context.Context, merchantID int64) error {
	return store.UpsertVisibilityScores(ctx, s.db, merchantID)
}

// ─── Execution helpers ────────────────────────────────────────────────────────
// Moved from jobs/scan_worker.go; exported so scan_test.go can test them directly.

// IsRateLimitErr returns true for HTTP 429 errors where retrying is pointless.
func IsRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "429") || contains(msg, "rate limited")
}

// RunWithRetries calls client.Query up to n times with exponential backoff.
// Returns all successful results (used for majority vote).
// Rate-limit errors (429) abort immediately.
func RunWithRetries(ctx context.Context, client platform.AIClient, brand, prompt string, n int) ([]platform.CitationResult, error) {
	return runWithRetries(ctx, client, brand, prompt, n)
}

func runWithRetries(ctx context.Context, client platform.AIClient, brand, prompt string, n int) ([]platform.CitationResult, error) {
	var results []platform.CitationResult
	backoff := time.Second

	var lastErr error
	for i := 0; i < n; i++ {
		result, err := client.Query(ctx, brand, prompt)
		if err == nil {
			results = append(results, result)
			continue
		}
		lastErr = err
		slog.Debug("scan: attempt failed", "platform", client.Name(), "attempt", i+1, "err", err)
		if IsRateLimitErr(err) {
			break
		}
		if i < n-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("all %d attempts failed for platform %s: %w", n, client.Name(), lastErr)
	}
	return results, nil
}

// AggregateResults applies majority vote on Mentioned and median on Position.
// Exported for testing.
func AggregateResults(results []platform.CitationResult) platform.CitationResult {
	return aggregateResults(results)
}

func aggregateResults(results []platform.CitationResult) platform.CitationResult {
	if len(results) == 0 {
		return platform.CitationResult{}
	}

	// Use the result with the most competitors as base to preserve competitor data.
	bestIdx := 0
	for i, r := range results {
		if len(r.Competitors) > len(results[bestIdx].Competitors) {
			bestIdx = i
		}
	}
	base := results[bestIdx]

	// Majority vote on Mentioned
	mentionedCount := 0
	totalTokensIn, totalTokensOut := 0, 0
	totalCost := 0.0
	for _, r := range results {
		if r.Mentioned {
			mentionedCount++
		}
		totalTokensIn += r.TokensIn
		totalTokensOut += r.TokensOut
		totalCost += r.CostUSD
	}
	base.Mentioned = mentionedCount > len(results)/2

	// Median position from results where mentioned.
	var positions []int
	for _, r := range results {
		if r.Mentioned && r.Position > 0 {
			positions = append(positions, r.Position)
		}
	}
	if len(positions) > 0 {
		base.Position = Median(positions)
	} else {
		base.Position = 0
	}

	// Consistency enforcement.
	if base.Mentioned && base.Position == 0 {
		base.Position = 1
	}
	if !base.Mentioned {
		base.Position = 0
	}

	// Average cost/tokens across all runs.
	base.TokensIn = totalTokensIn / len(results)
	base.TokensOut = totalTokensOut / len(results)
	base.CostUSD = totalCost / float64(len(results))

	return base
}

// Median returns the median of a slice of ints. Exported for testing.
func Median(vals []int) int {
	n := len(vals)
	if n == 0 {
		return 0
	}
	cp := make([]int, n)
	copy(cp, vals)
	// Insertion sort — fast enough for the small slices we encounter.
	for i := 1; i < n; i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	return cp[n/2]
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─── Internal job type (avoids import cycle jobs → service → jobs) ────────────

type scanFixGenerationJobArgs struct {
	MerchantID int64 `json:"merchant_id"`
}

func (scanFixGenerationJobArgs) Kind() string { return "fix_generation" }

var fixGenInsertOpts = &river.InsertOpts{Queue: "fixes", MaxAttempts: 2}
