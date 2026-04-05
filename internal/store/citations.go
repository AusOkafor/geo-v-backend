package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/austinokafor/geo-backend/internal/platform"
)

// InsertCitationRecord saves one AI scan result to citation_records.
// queryType comes from the query generator (e.g. "price_bracket", "brand") and is
// caller-supplied because it is metadata about the question, not the AI response.
func InsertCitationRecord(ctx context.Context, db *pgxpool.Pool, merchantID int64, queryType string, r platform.CitationResult) error {
	competitors := r.Competitors
	if competitors == nil {
		competitors = []platform.Competitor{}
	}
	competitorsJSON, err := json.Marshal(competitors)
	if err != nil {
		return err
	}

	// SHA256 fingerprint of the raw answer text — proves the stored response
	// hasn't been tampered with since capture time.
	h := sha256.Sum256([]byte(r.AnswerText))
	responseHash := hex.EncodeToString(h[:])

	durationMs := int(r.Duration.Milliseconds())

	_, err = db.Exec(ctx, `
		INSERT INTO citation_records
			(merchant_id, platform, query, query_type, mentioned, position, sentiment,
			 competitors, tokens_used, cost_usd, grounded, answer_text,
			 response_hash, model_version, scan_duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`,
		merchantID,
		r.Platform,
		r.Query,
		queryType,
		r.Mentioned,
		r.Position,
		r.Sentiment,
		competitorsJSON,
		r.TokensIn+r.TokensOut,
		r.CostUSD,
		r.Grounded,
		r.AnswerText,
		responseHash,
		r.ModelVersion,
		durationMs,
	)
	return err
}

// PlatformSource describes how a platform's last scan was performed.
type PlatformSource struct {
	Platform string `json:"platform"`
	Grounded bool   `json:"grounded"` // true = real web search; false = model memory only
}

// GetPlatformSources returns grounding status per platform based on the most recent scan day.
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
// CompetitorCount is the average number of distinct competitors cited across platforms
// for this query — a proxy for how much opportunity exists (AI knows the topic well).
type QueryGap struct {
	Query           string   `json:"query"`
	QueryType       string   `json:"query_type"`
	Platforms       []string `json:"platforms"`
	CompetitorCount int      `json:"competitor_count"`
	Impact          string   `json:"impact"`     // "high" | "medium" | "low"
	Difficulty      string   `json:"difficulty"` // "hard" | "medium" | "easy"
}

// GetQueryGaps returns queries from the most recent scan where the merchant was not
// mentioned, enriched with opportunity signals (competitor density, impact, difficulty).
func GetQueryGaps(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]QueryGap, error) {
	rows, err := db.Query(ctx, `
		WITH latest AS (
			SELECT MAX(scanned_at) AS ts FROM citation_records WHERE merchant_id = $1
		),
		recent AS (
			SELECT query, query_type, platform, mentioned, competitors
			FROM citation_records, latest
			WHERE merchant_id = $1
			  AND scanned_at = latest.ts
		),
		per_query AS (
			SELECT
				query,
				MAX(query_type)                                         AS query_type,
				array_agg(DISTINCT platform ORDER BY platform)          AS platforms,
				bool_or(mentioned)                                      AS any_mentioned,
				-- Count distinct competitor names across all platforms for this query
				COUNT(DISTINCT comp->>'name')::int                      AS competitor_count
			FROM recent
			LEFT JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(competitors) = 'array' THEN competitors ELSE '[]'::jsonb END
			) AS comp ON TRUE
			GROUP BY query
		)
		SELECT query, query_type, platforms, competitor_count
		FROM per_query
		WHERE NOT any_mentioned
		ORDER BY competitor_count DESC, query
	`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gaps []QueryGap
	for rows.Next() {
		var g QueryGap
		if err := rows.Scan(&g.Query, &g.QueryType, &g.Platforms, &g.CompetitorCount); err != nil {
			return nil, err
		}
		g.Impact = opportunityImpact(g.CompetitorCount)
		g.Difficulty = opportunityDifficulty(g.CompetitorCount)
		gaps = append(gaps, g)
	}
	if gaps == nil {
		gaps = []QueryGap{}
	}
	return gaps, rows.Err()
}

// opportunityImpact maps competitor density to how much creating content for this
// query could move the needle. More competitors = AI definitely knows the topic.
func opportunityImpact(competitorCount int) string {
	switch {
	case competitorCount >= 5:
		return "high"
	case competitorCount >= 3:
		return "medium"
	default:
		return "low"
	}
}

// opportunityDifficulty estimates how hard it is to break into a query's results.
// Many competitors = entrenched recommendations = harder to displace.
func opportunityDifficulty(competitorCount int) string {
	switch {
	case competitorCount >= 7:
		return "hard"
	case competitorCount >= 4:
		return "medium"
	default:
		return "easy"
	}
}

// LiveAnswer is a single AI platform response from the most recent scan,
// surfaced verbatim so merchants can see exactly what AI says (and who it cites).
type LiveAnswer struct {
	Query          string   `json:"query"`
	Platform       string   `json:"platform"`
	AnswerText     string   `json:"answer_text"`
	Competitors    []string `json:"competitors"`    // competitor names in cited order
	BrandMentioned bool     `json:"brand_mentioned"`
}

// GetLiveAnswers returns up to limit answers from the most recent scan,
// ordered by query then platform, skipping rows with no answer text.
func GetLiveAnswers(ctx context.Context, db *pgxpool.Pool, merchantID int64, limit int) ([]LiveAnswer, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(ctx, `
		SELECT query, platform, answer_text, competitors, mentioned
		FROM citation_records
		WHERE merchant_id = $1
		  AND answer_text IS NOT NULL
		  AND answer_text != ''
		  AND scanned_at = (SELECT MAX(scanned_at) FROM citation_records WHERE merchant_id = $1)
		ORDER BY query, platform
		LIMIT $2
	`, merchantID, limit)
	if err != nil {
		return nil, fmt.Errorf("store.GetLiveAnswers: %w", err)
	}
	defer rows.Close()

	var answers []LiveAnswer
	for rows.Next() {
		var a LiveAnswer
		var competitorsJSON []byte
		if err := rows.Scan(&a.Query, &a.Platform, &a.AnswerText, &competitorsJSON, &a.BrandMentioned); err != nil {
			return nil, err
		}
		// Extract just the names in order
		var comps []platform.Competitor
		if err := json.Unmarshal(competitorsJSON, &comps); err == nil {
			for _, c := range comps {
				if c.Name != "" {
					a.Competitors = append(a.Competitors, c.Name)
				}
			}
		}
		if a.Competitors == nil {
			a.Competitors = []string{}
		}
		answers = append(answers, a)
	}
	if answers == nil {
		answers = []LiveAnswer{}
	}
	return answers, rows.Err()
}

// BrandRecognitionStatus describes how well AI models recognise the merchant's brand.
type BrandRecognitionStatus struct {
	RecognitionRate  float64  `json:"recognition_rate"`  // 0.0–1.0, grounded platforms only
	MentionedQueries int      `json:"mentioned_queries"`
	TotalQueries     int      `json:"total_queries"`
	IsRecognized     bool     `json:"is_recognized"`
	// Tier: "not_recognized" (0 mentions) | "weak" (1–2) | "recognized" (3+)
	Tier             string   `json:"tier"`
	// Reasons: human-readable explanations of the tier
	Reasons          []string `json:"reasons"`
	// Confidence: "high" (≥20 grounded queries) | "medium" (8–19) | "low" (<8)
	Confidence       string   `json:"confidence"`
}

// GetBrandRecognitionStatus returns how well grounded AI platforms recognised
// the merchant's brand in their most recent scan.
func GetBrandRecognitionStatus(ctx context.Context, db *pgxpool.Pool, merchantID int64) (BrandRecognitionStatus, error) {
	var status BrandRecognitionStatus
	err := db.QueryRow(ctx, `
		SELECT
			COUNT(*)::int                                    AS total,
			SUM(CASE WHEN mentioned THEN 1 ELSE 0 END)::int AS mentioned
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

	// Tier
	switch {
	case status.MentionedQueries == 0:
		status.Tier = "not_recognized"
	case status.MentionedQueries <= 2:
		status.Tier = "weak"
	default:
		status.Tier = "recognized"
	}

	// Confidence — based on how many grounded queries we have (more = more reliable)
	switch {
	case status.TotalQueries >= 20:
		status.Confidence = "high"
	case status.TotalQueries >= 8:
		status.Confidence = "medium"
	default:
		status.Confidence = "low"
	}

	status.Reasons = buildRecognitionReasons(status.Tier, status.MentionedQueries, status.TotalQueries)
	return status, nil
}

func buildRecognitionReasons(tier string, mentioned, total int) []string {
	if total == 0 {
		return []string{"No scan data available — run a scan to check brand recognition"}
	}
	switch tier {
	case "not_recognized":
		return []string{
			fmt.Sprintf("Not found in any of %d web-grounded AI search queries", total),
			"AI models have no awareness of your brand in trusted online sources",
			"No structured product data detected that AI can reference",
			"Missing brand authority signals: reviews, press mentions, and backlinks",
		}
	case "weak":
		return []string{
			fmt.Sprintf("Mentioned in only %d of %d queries — inconsistent signal", mentioned, total),
			"Brand recognition is fragile and likely driven by a single source",
			"Not yet establishing a reliable cross-platform presence pattern",
		}
	default:
		return []string{
			fmt.Sprintf("Mentioned in %d of %d web-grounded queries", mentioned, total),
		}
	}
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
