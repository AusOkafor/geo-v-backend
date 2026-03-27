package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yourname/geo-backend/internal/platform"
)

// InsertCitationRecord saves one AI scan result to citation_records.
func InsertCitationRecord(ctx context.Context, db *pgxpool.Pool, merchantID int64, r platform.CitationResult) error {
	competitorsJSON, err := json.Marshal(r.Competitors)
	if err != nil {
		return err
	}

	_, err = db.Exec(ctx, `
		INSERT INTO citation_records
			(merchant_id, platform, query, query_type, mentioned, position, sentiment, competitors, tokens_used, cost_usd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		merchantID,
		r.Platform,
		r.Query,
		"", // query_type populated by caller if needed
		r.Mentioned,
		r.Position,
		r.Sentiment,
		competitorsJSON,
		r.TokensIn+r.TokensOut,
		r.CostUSD,
	)
	return err
}

// UpsertScanCost adds (or accumulates) daily cost for merchant+platform.
func UpsertScanCost(ctx context.Context, db *pgxpool.Pool, merchantID int64, platformName string, tokensIn, tokensOut int, costUSD float64) error {
	_, err := db.Exec(ctx, `
		INSERT INTO scan_costs (merchant_id, platform, queries_run, tokens_used, cost_usd)
		VALUES ($1, $2, 1, $3, $4)
		ON CONFLICT (merchant_id, cost_date, platform)
		DO UPDATE SET
			queries_run = scan_costs.queries_run + 1,
			tokens_used = scan_costs.tokens_used + EXCLUDED.tokens_used,
			cost_usd    = scan_costs.cost_usd + EXCLUDED.cost_usd
	`, merchantID, platformName, tokensIn+tokensOut, costUSD)
	return err
}

// UpsertVisibilityScores aggregates today's citation_records into visibility_scores.
func UpsertVisibilityScores(ctx context.Context, db *pgxpool.Pool, merchantID int64) error {
	_, err := db.Exec(ctx, `
		INSERT INTO visibility_scores (merchant_id, platform, score_date, score, queries_run, queries_hit)
		SELECT
			merchant_id,
			platform,
			CURRENT_DATE,
			CASE WHEN COUNT(*) = 0 THEN 0
			     ELSE ROUND(SUM(CASE WHEN mentioned THEN 1 ELSE 0 END)::numeric / COUNT(*) * 100)
			END::smallint AS score,
			COUNT(*)::int AS queries_run,
			SUM(CASE WHEN mentioned THEN 1 ELSE 0 END)::int AS queries_hit
		FROM citation_records
		WHERE merchant_id = $1
		  AND scanned_at = CURRENT_DATE
		GROUP BY merchant_id, platform
		ON CONFLICT (merchant_id, platform, score_date)
		DO UPDATE SET
			score       = EXCLUDED.score,
			queries_run = EXCLUDED.queries_run,
			queries_hit = EXCLUDED.queries_hit
	`, merchantID)
	return err
}
