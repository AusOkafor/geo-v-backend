package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/austinokafor/geo-backend/internal/platform"
)

// InsertCitationRecord saves one AI scan result to citation_records.
func InsertCitationRecord(ctx context.Context, db *pgxpool.Pool, merchantID int64, r platform.CitationResult) error {
	competitors := r.Competitors
	if competitors == nil {
		competitors = []platform.Competitor{}
	}
	competitorsJSON, err := json.Marshal(competitors)
	if err != nil {
		return err
	}

	_, err = db.Exec(ctx, `
		INSERT INTO citation_records
			(merchant_id, platform, query, query_type, mentioned, position, sentiment, competitors, tokens_used, cost_usd, grounded)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
		r.Grounded,
	)
	return err
}

// PlatformSource describes how a platform's last scan was performed.
type PlatformSource struct {
	Platform string `json:"platform"`
	Grounded bool   `json:"grounded"` // true = real web search; false = model memory only
}

// GetPlatformSources returns grounding status per platform based on the most recent scan day.
// Used by the frontend to show "Web-grounded" vs "AI prediction" badges.
func GetPlatformSources(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]PlatformSource, error) {
	rows, err := db.Query(ctx, `
		SELECT platform, bool_or(grounded) AS grounded
		FROM citation_records
		WHERE merchant_id = $1
		  AND scanned_at = (
		      SELECT MAX(scanned_at) FROM citation_records WHERE merchant_id = $1
		  )
		GROUP BY platform
		ORDER BY platform
	`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []PlatformSource
	for rows.Next() {
		var s PlatformSource
		if err := rows.Scan(&s.Platform, &s.Grounded); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	if sources == nil {
		sources = []PlatformSource{}
	}
	return sources, rows.Err()
}

// QueryGap represents a query where the merchant was not mentioned on any platform.
type QueryGap struct {
	Query     string   `json:"query"`
	Platforms []string `json:"platforms"` // platforms that ran this query
}

// GetQueryGaps returns queries from the most recent scan where the merchant was not mentioned
// on any platform — i.e., total blind spots in AI visibility.
func GetQueryGaps(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]QueryGap, error) {
	rows, err := db.Query(ctx, `
		WITH latest AS (
			SELECT MAX(scanned_at) AS ts FROM citation_records WHERE merchant_id = $1
		),
		recent AS (
			SELECT query, platform, mentioned
			FROM citation_records, latest
			WHERE merchant_id = $1
			  AND scanned_at = latest.ts
		),
		per_query AS (
			SELECT
				query,
				array_agg(DISTINCT platform ORDER BY platform) AS platforms,
				bool_or(mentioned) AS any_mentioned
			FROM recent
			GROUP BY query
		)
		SELECT query, platforms
		FROM per_query
		WHERE NOT any_mentioned
		ORDER BY query
	`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gaps []QueryGap
	for rows.Next() {
		var g QueryGap
		if err := rows.Scan(&g.Query, &g.Platforms); err != nil {
			return nil, err
		}
		gaps = append(gaps, g)
	}
	if gaps == nil {
		gaps = []QueryGap{}
	}
	return gaps, rows.Err()
}

// BrandRecognitionStatus describes how well AI models recognise the merchant's brand.
type BrandRecognitionStatus struct {
	// RecognitionRate is the fraction of grounded-platform queries where the brand
	// was mentioned (0.0 – 1.0). Only grounded platforms are counted because
	// model-memory platforms regularly fail to recognise small/new brands.
	RecognitionRate   float64 `json:"recognition_rate"`
	MentionedQueries  int     `json:"mentioned_queries"`
	TotalQueries      int     `json:"total_queries"`
	// IsRecognized is true when at least one grounded platform mentioned the brand
	// in the most recent scan. False means AI has no knowledge of the brand.
	IsRecognized      bool    `json:"is_recognized"`
}

// GetBrandRecognitionStatus returns how well grounded AI platforms recognised
// the merchant's brand in their most recent scan.
func GetBrandRecognitionStatus(ctx context.Context, db *pgxpool.Pool, merchantID int64) (BrandRecognitionStatus, error) {
	var status BrandRecognitionStatus
	err := db.QueryRow(ctx, `
		SELECT
			COUNT(*)::int                                          AS total,
			SUM(CASE WHEN mentioned THEN 1 ELSE 0 END)::int       AS mentioned
		FROM citation_records
		WHERE merchant_id = $1
		  AND grounded = true
		  AND scanned_at = (SELECT MAX(scanned_at) FROM citation_records WHERE merchant_id = $1)
	`, merchantID).Scan(&status.TotalQueries, &status.MentionedQueries)
	if err != nil {
		return status, err
	}
	if status.TotalQueries > 0 {
		status.RecognitionRate = float64(status.MentionedQueries) / float64(status.TotalQueries)
	}
	status.IsRecognized = status.MentionedQueries > 0
	return status, nil
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
