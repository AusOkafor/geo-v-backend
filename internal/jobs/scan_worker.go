package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/platform"
	"github.com/austinokafor/geo-backend/internal/query"
	"github.com/austinokafor/geo-backend/internal/store"
)

// ScanWorker executes AI visibility scans for a single merchant.
type ScanWorker struct {
	river.WorkerDefaults[ScanJobArgs]
	db      *pgxpool.Pool
	clients []platform.AIClient
}

func (w *ScanWorker) Timeout(_ *river.Job[ScanJobArgs]) time.Duration {
	return 20 * time.Minute // real LLM calls can be slow; override River's default
}

func NewScanWorker(db *pgxpool.Pool, clients []platform.AIClient) *ScanWorker {
	return &ScanWorker{db: db, clients: clients}
}

func (w *ScanWorker) Work(ctx context.Context, job *river.Job[ScanJobArgs]) error {
	merchantID := job.Args.MerchantID
	slog.Info("scan: starting", "merchant_id", merchantID)

	// Load merchant
	merchant, err := store.GetMerchant(ctx, w.db, merchantID)
	if err != nil {
		return fmt.Errorf("scan: load merchant %d: %w", merchantID, err)
	}
	if !merchant.Active {
		slog.Info("scan: skipped (inactive)", "merchant_id", merchantID)
		return nil
	}

	// Cost guardrail: skip scan if monthly spend exceeds 60% of plan revenue
	monthlyCost, err := store.GetMonthlyCostByMerchant(ctx, w.db, merchantID)
	if err == nil && store.ExceedsGuardrail(monthlyCost, merchant.Plan) {
		slog.Warn("scan skipped: cost guardrail exceeded",
			"merchant_id", merchantID,
			"plan", merchant.Plan,
			"monthly_cost_usd", monthlyCost,
		)
		return nil
	}

	// Generate queries
	queries := query.Generate(merchant.Category, merchant.BrandName)

	// Run each query against each platform
	for _, q := range queries {
		for _, client := range w.clients {
			results, err := runWithRetries(ctx, client, merchant.BrandName, q.Text, 3)
			if err != nil {
				slog.Warn("scan: platform failed, skipping",
					"merchant_id", merchantID,
					"platform", client.Name(),
					"query", q.Text,
					"err", err,
				)
				continue
			}

			result := aggregateResults(results)
			result.Query = q.Text

			if err := store.InsertCitationRecord(ctx, w.db, merchantID, result); err != nil {
				return fmt.Errorf("scan: insert citation: %w", err)
			}
			if err := store.UpsertScanCost(ctx, w.db, merchantID, result.Platform, result.TokensIn, result.TokensOut, result.CostUSD); err != nil {
				return fmt.Errorf("scan: upsert cost: %w", err)
			}

			// Rate limit: 500ms between platform calls
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}

	// Aggregate citation_records → visibility_scores
	return store.UpsertVisibilityScores(ctx, w.db, merchantID)
}

// runWithRetries calls client.Query up to n times with exponential backoff.
// Returns all successful results (used for majority vote).
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

// aggregateResults applies majority vote on Mentioned and median on Position.
func aggregateResults(results []platform.CitationResult) platform.CitationResult {
	if len(results) == 0 {
		return platform.CitationResult{}
	}

	base := results[0]

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

	// Use median position from results where mentioned
	positions := []int{}
	for _, r := range results {
		if r.Mentioned && r.Position > 0 {
			positions = append(positions, r.Position)
		}
	}
	if len(positions) > 0 {
		base.Position = median(positions)
	} else {
		base.Position = 0
	}

	// Average cost/tokens across all runs
	base.TokensIn = totalTokensIn / len(results)
	base.TokensOut = totalTokensOut / len(results)
	base.CostUSD = totalCost / float64(len(results))

	return base
}

func median(vals []int) int {
	n := len(vals)
	if n == 0 {
		return 0
	}
	// Simple insertion sort for small slices
	for i := 1; i < n; i++ {
		for j := i; j > 0 && vals[j] < vals[j-1]; j-- {
			vals[j], vals[j-1] = vals[j-1], vals[j]
		}
	}
	return vals[n/2]
}
