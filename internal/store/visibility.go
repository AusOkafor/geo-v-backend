package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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

// ScoredCompetitor is a filtered, scored competitor with explanations.
type ScoredCompetitor struct {
	Name              string         `json:"name"`
	Platforms         []string       `json:"platforms"`
	BestPosition      int            `json:"best_position"`
	PlatformPositions map[string]int `json:"platform_positions"`
	TotalFrequency    int            `json:"total_frequency"`
	TotalScans        int            `json:"total_scans"`
	Score             float64        `json:"score"`
	WhyPoints         []string       `json:"why_points"`
}

// junkNames are non-brand entities that should never appear as competitors.
// These are platforms, themes, social networks, and pure marketplaces — not product brands.
var junkNames = map[string]bool{
	// Web builders & themes
	"shopify": true, "wordpress": true, "squarespace": true, "wix": true,
	"webflow": true, "bigcommerce": true, "magento": true, "woocommerce": true,
	"astra theme": true, "astra": true, "generatepress": true, "generatepress theme": true,
	"divi": true, "elementor": true, "avada": true, "themeforest": true,
	// Social & content platforms
	"reddit": true, "quora": true, "pinterest": true, "youtube": true,
	"instagram": true, "facebook": true, "twitter": true, "tiktok": true,
	"linkedin": true, "snapchat": true, "tumblr": true,
	// Search engines
	"google": true, "bing": true, "yahoo": true, "duckduckgo": true,
	// Pure marketplaces (no brand identity)
	"amazon": true, "ebay": true, "aliexpress": true, "wish": true,
	"shein": true, "temu": true,
}

type rawCompetitorRow struct {
	Name           string
	Platforms      []string
	BestPosition   int
	TotalFrequency int
	PlatformCount  int
	TotalScans     int
	ChatGPTPos     *int
	PerplexityPos  *int
	GeminiPos      *int
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

// GetCompetitors returns scored, filtered competitors for a merchant, last 30 days.
// Results are pre-grouped by brand name, scored across frequency + platform breadth + position quality,
// and filtered to remove junk (themes, social platforms, pure marketplaces).
func GetCompetitors(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]ScoredCompetitor, error) {
	rows, err := db.Query(ctx, `
		WITH scan_base AS (
			SELECT id, platform, competitors
			FROM citation_records
			WHERE merchant_id = $1
			  AND scanned_at >= CURRENT_DATE - interval '30 days'
		),
		total_scans AS (
			SELECT COUNT(*)::int AS n FROM scan_base
		),
		expanded AS (
			SELECT
				comp->>'name'                             AS name,
				s.platform,
				COALESCE((comp->>'position')::int, 0)    AS position
			FROM scan_base s
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(s.competitors) = 'array' THEN s.competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE comp->>'name' IS NOT NULL AND comp->>'name' != ''
		),
		grouped AS (
			SELECT
				name,
				COALESCE(array_agg(DISTINCT platform), ARRAY[]::text[])  AS platforms,
				COALESCE(MIN(CASE WHEN position > 0 THEN position END), 0) AS best_position,
				COUNT(*)::int                                              AS total_frequency,
				COUNT(DISTINCT platform)::int                             AS platform_count,
				MIN(CASE WHEN platform = 'chatgpt'    AND position > 0 THEN position END) AS chatgpt_pos,
				MIN(CASE WHEN platform = 'perplexity' AND position > 0 THEN position END) AS perplexity_pos,
				MIN(CASE WHEN platform = 'gemini'     AND position > 0 THEN position END) AS gemini_pos
			FROM expanded
			GROUP BY name
		)
		SELECT
			g.name,
			g.platforms,
			g.best_position,
			g.total_frequency,
			g.platform_count,
			t.n AS total_scans,
			g.chatgpt_pos,
			g.perplexity_pos,
			g.gemini_pos
		FROM grouped g
		CROSS JOIN total_scans t
		ORDER BY g.total_frequency DESC, g.platform_count DESC
		LIMIT 50
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store.GetCompetitors: %w", err)
	}
	defer rows.Close()

	var raw []rawCompetitorRow
	for rows.Next() {
		var r rawCompetitorRow
		if err := rows.Scan(
			&r.Name, &r.Platforms, &r.BestPosition,
			&r.TotalFrequency, &r.PlatformCount, &r.TotalScans,
			&r.ChatGPTPos, &r.PerplexityPos, &r.GeminiPos,
		); err != nil {
			return nil, err
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return scoreAndFilterCompetitors(raw), nil
}

func scoreAndFilterCompetitors(rows []rawCompetitorRow) []ScoredCompetitor {
	if len(rows) == 0 {
		return []ScoredCompetitor{}
	}

	// Normalise against the highest-frequency competitor
	maxFreq := 0
	for _, r := range rows {
		if r.TotalFrequency > maxFreq {
			maxFreq = r.TotalFrequency
		}
	}

	var scored []ScoredCompetitor
	for _, r := range rows {
		nameLower := strings.ToLower(strings.TrimSpace(r.Name))

		// Hard-filter: known non-brand entities
		if junkNames[nameLower] {
			continue
		}

		// Hard-filter: product descriptions masquerading as brand names (> 4 words)
		if len(strings.Fields(r.Name)) > 4 {
			continue
		}

		// Composite score
		//   50% frequency  — how often they're cited (primary signal)
		//   30% platform   — cross-platform presence shows true dominance
		//   20% position   — being cited first matters more than being cited 5th
		freqScore := float64(r.TotalFrequency) / float64(maxFreq)
		platformScore := float64(r.PlatformCount) / 3.0
		posScore := 0.0
		if r.BestPosition > 0 {
			posScore = math.Max(0, float64(6-r.BestPosition)) / 5.0
		}
		score := 0.5*freqScore + 0.3*platformScore + 0.2*posScore
		score = math.Round(score*100) / 100

		platPos := map[string]int{}
		if r.ChatGPTPos != nil {
			platPos["chatgpt"] = *r.ChatGPTPos
		}
		if r.PerplexityPos != nil {
			platPos["perplexity"] = *r.PerplexityPos
		}
		if r.GeminiPos != nil {
			platPos["gemini"] = *r.GeminiPos
		}

		scored = append(scored, ScoredCompetitor{
			Name:              r.Name,
			Platforms:         r.Platforms,
			BestPosition:      r.BestPosition,
			PlatformPositions: platPos,
			TotalFrequency:    r.TotalFrequency,
			TotalScans:        r.TotalScans,
			Score:             score,
			WhyPoints:         buildWhyPoints(r),
		})
	}

	// Re-sort by composite score (SQL sorted by frequency only)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	// Return top 10 — more than that dilutes signal
	if len(scored) > 10 {
		scored = scored[:10]
	}

	return scored
}

func buildWhyPoints(r rawCompetitorRow) []string {
	var pts []string

	pct := 0
	if r.TotalScans > 0 {
		pct = (r.TotalFrequency * 100) / r.TotalScans
	}
	pts = append(pts, fmt.Sprintf("Appears in %d%% of AI responses (%d citations)", pct, r.TotalFrequency))

	switch {
	case r.BestPosition == 1:
		pts = append(pts, "Consistently cited as the #1 recommendation")
	case r.BestPosition == 2:
		pts = append(pts, "Frequently cited as a top-2 recommendation")
	case r.BestPosition > 0:
		pts = append(pts, fmt.Sprintf("Cited at position #%d in AI answers", r.BestPosition))
	}

	switch r.PlatformCount {
	case 3:
		pts = append(pts, "Cited across all 3 platforms — ChatGPT, Perplexity, and Gemini")
	case 2:
		pts = append(pts, fmt.Sprintf("Cited on %d of 3 AI platforms", r.PlatformCount))
	}

	return pts
}
