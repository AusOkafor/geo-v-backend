package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VisibilityScore mirrors visibility_scores for API responses.
type VisibilityScore struct {
	Platform   string    `json:"platform"`
	Score      int       `json:"score"`
	QueriesRun int       `json:"queries_run"`
	QueriesHit int       `json:"queries_hit"`
	ScoreDate  time.Time `json:"score_date"`
}

// DailyScore is used for the trend chart (one row per day per platform aggregated to date).
type DailyScore struct {
	Date       string `json:"date"`
	ChatGPT    int    `json:"chatgpt"`
	Perplexity int    `json:"perplexity"`
	Gemini     int    `json:"gemini"`
}

// CompetitorRow represents a competitor mention from citation_records.
type CompetitorRow struct {
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	Position  int    `json:"position"`
	Frequency int    `json:"frequency"`
}

// GetVisibilityScores returns the latest visibility score per platform within N days.
func GetVisibilityScores(ctx context.Context, db *pgxpool.Pool, merchantID int64, days int) ([]VisibilityScore, error) {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT ON (platform)
			platform, score, queries_run, queries_hit, score_date
		FROM visibility_scores
		WHERE merchant_id = $1
		  AND score_date >= CURRENT_DATE - make_interval(days => $2)
		ORDER BY platform, score_date DESC
	`, merchantID, days)
	if err != nil {
		return nil, fmt.Errorf("store.GetVisibilityScores: %w", err)
	}
	defer rows.Close()

	var scores []VisibilityScore
	for rows.Next() {
		var s VisibilityScore
		if err := rows.Scan(&s.Platform, &s.Score, &s.QueriesRun, &s.QueriesHit, &s.ScoreDate); err != nil {
			return nil, err
		}
		scores = append(scores, s)
	}
	return scores, rows.Err()
}

// GetDailyScores returns per-day visibility for the trend chart.
func GetDailyScores(ctx context.Context, db *pgxpool.Pool, merchantID int64, days int) ([]DailyScore, error) {
	rows, err := db.Query(ctx, `
		SELECT
			score_date::text,
			MAX(CASE WHEN platform = 'chatgpt'    THEN score ELSE 0 END) AS chatgpt,
			MAX(CASE WHEN platform = 'perplexity' THEN score ELSE 0 END) AS perplexity,
			MAX(CASE WHEN platform = 'gemini'     THEN score ELSE 0 END) AS gemini
		FROM visibility_scores
		WHERE merchant_id = $1
		  AND score_date >= CURRENT_DATE - make_interval(days => $2)
		GROUP BY score_date
		ORDER BY score_date ASC
	`, merchantID, days)
	if err != nil {
		return nil, fmt.Errorf("store.GetDailyScores: %w", err)
	}
	defer rows.Close()

	var scores []DailyScore
	for rows.Next() {
		var s DailyScore
		if err := rows.Scan(&s.Date, &s.ChatGPT, &s.Perplexity, &s.Gemini); err != nil {
			return nil, err
		}
		scores = append(scores, s)
	}
	return scores, rows.Err()
}

// GetCompetitors returns top competitors cited instead of the merchant, last 30 days.
func GetCompetitors(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]CompetitorRow, error) {
	rows, err := db.Query(ctx, `
		SELECT
			comp->>'name'          AS name,
			platform,
			(comp->>'position')::int AS position,
			COUNT(*)               AS frequency
		FROM citation_records
		CROSS JOIN LATERAL jsonb_array_elements(
			CASE WHEN jsonb_typeof(competitors) = 'array' THEN competitors ELSE '[]'::jsonb END
		) AS comp
		WHERE merchant_id = $1
		  AND scanned_at >= CURRENT_DATE - interval '30 days'
		  AND comp->>'name' IS NOT NULL
		  AND comp->>'name' != ''
		GROUP BY name, platform, position
		ORDER BY frequency DESC
		LIMIT 20
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store.GetCompetitors: %w", err)
	}
	defer rows.Close()

	comps := []CompetitorRow{}
	for rows.Next() {
		var c CompetitorRow
		if err := rows.Scan(&c.Name, &c.Platform, &c.Position, &c.Frequency); err != nil {
			return nil, err
		}
		comps = append(comps, c)
	}
	return comps, rows.Err()
}
