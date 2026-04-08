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
	// TopQueries: the 3 queries where this competitor is most frequently cited.
	// Gives merchants a concrete view of WHERE they are losing — not just to whom.
	TopQueries        []string       `json:"top_queries"`
	// Class: "brand" = direct competitor | "retailer" = multi-brand store
	Class             string         `json:"class"`
	// Tier: 1 = high-confidence established brand, 2 = mid-confidence, 3 = uncertain
	Tier              int            `json:"tier"`
}

// competitorTier assigns a confidence tier based on score and cross-platform presence.
func competitorTier(score float64, platformCount int) int {
	if score >= 0.60 && platformCount >= 2 {
		return 1 // Established, cited broadly
	}
	if score >= 0.30 || platformCount >= 2 {
		return 2 // Real brand, limited signal
	}
	return 3 // Low confidence — may be noise
}

// competitorClass is used to classify competitor names for filtering and labelling.
// We classify instead of blacklist so the logic is inspectable and extensible.
type competitorClass string

const (
	classMarketplace competitorClass = "marketplace" // filtered — pure buy/sell platforms (Amazon, eBay)
	classPlatform    competitorClass = "platform"    // filtered — tech/social platforms (Shopify, Reddit)
	classRetailer    competitorClass = "retailer"    // kept + labelled — multi-brand stores (Nordstrom)
	classBrand       competitorClass = "brand"       // kept — direct DTC product brand competitor
)

// classifyCompetitor returns the class for a competitor name (already lower-cased).
func classifyCompetitor(nameLower string) competitorClass {
	if marketplaceNames[nameLower] || genericPhrases[nameLower] {
		return classMarketplace
	}
	if platformNames[nameLower] {
		return classPlatform
	}
	if retailerNames[nameLower] {
		return classRetailer
	}
	return classBrand
}

var marketplaceNames = map[string]bool{
	"amazon": true, "ebay": true, "aliexpress": true, "wish": true,
	"shein": true, "temu": true, "walmart": true, "target": true, "etsy": true,
}

var platformNames = map[string]bool{
	// Web builders & themes
	"shopify": true, "wordpress": true, "squarespace": true, "wix": true,
	"webflow": true, "bigcommerce": true, "magento": true, "woocommerce": true,
	"astra theme": true, "astra": true, "generatepress": true, "generatepress theme": true,
	"divi": true, "elementor": true, "avada": true, "themeforest": true,
	// Social & content
	"reddit": true, "quora": true, "pinterest": true, "youtube": true,
	"instagram": true, "facebook": true, "twitter": true, "tiktok": true,
	"linkedin": true, "snapchat": true, "tumblr": true,
	// Search engines
	"google": true, "bing": true, "yahoo": true, "duckduckgo": true,
}

var retailerNames = map[string]bool{
	// Jewelry / accessories multi-brand retailers
	"blue nile": true, "james allen": true, "brilliant earth": true,
	"zales": true, "kay jewelers": true, "kay": true, "jared": true,
	"helzberg": true, "signet": true, "h samuel": true, "ernest jones": true,
	// Department & luxury stores
	"nordstrom": true, "bloomingdales": true, "bloomingdale's": true,
	"neiman marcus": true, "saks fifth avenue": true, "saks": true,
	"macy's": true, "macys": true,
	// Multi-brand fashion / lifestyle
	"net-a-porter": true, "farfetch": true, "revolve": true, "asos": true,
	"h&m": true, "zara": true, "uniqlo": true,
	"forever 21": true, "forever21": true, "urban outfitters": true,
}

var genericPhrases = map[string]bool{
	"various brands": true, "multiple brands": true, "local brands": true,
	"independent brands": true, "online retailers": true, "boutique stores": true,
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

// platformWeights returns a per-platform confidence weight based on whether the
// platform's most recent scan results were web-grounded (1.0) or model-memory (0.35).
// This prevents mocked platforms from inflating the competitor score.
func platformWeights(ctx context.Context, db *pgxpool.Pool, merchantID int64) map[string]float64 {
	// Default: 0.2 for model-memory platforms (Together/mock).
	// Grounded platforms (OpenAI web search, Perplexity sonar) are upgraded to 1.0.
	weights := map[string]float64{"chatgpt": 0.2, "perplexity": 0.2, "gemini": 0.2}
	rows, err := db.Query(ctx, `
		SELECT platform, bool_or(grounded) AS grounded
		FROM citation_records
		WHERE merchant_id = $1
		  AND scanned_at = (SELECT MAX(scanned_at) FROM citation_records WHERE merchant_id = $1)
		GROUP BY platform
	`, merchantID)
	if err != nil {
		return weights
	}
	defer rows.Close()
	for rows.Next() {
		var platform string
		var grounded bool
		if rows.Scan(&platform, &grounded) == nil && grounded {
			weights[platform] = 1.0
		}
	}
	return weights
}

// GetCompetitors returns scored, filtered competitors for a merchant, last 30 days.
// Results are pre-grouped by brand name, scored across frequency + platform breadth + position quality,
// and filtered to remove junk (themes, social platforms, pure marketplaces).
func GetCompetitors(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]ScoredCompetitor, error) {
	weights := platformWeights(ctx, db, merchantID)

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

	result := scoreAndFilterCompetitors(raw, weights)

	// Enrich each competitor with the top 3 queries where they appear most often.
	topQueriesMap, err := competitorTopQueries(ctx, db, merchantID)
	if err == nil {
		for i := range result {
			if qs, ok := topQueriesMap[result[i].Name]; ok {
				result[i].TopQueries = qs
			}
		}
	}

	return result, nil
}

// competitorTopQueries returns the top 3 queries per competitor name (last 30 days).
func competitorTopQueries(ctx context.Context, db *pgxpool.Pool, merchantID int64) (map[string][]string, error) {
	rows, err := db.Query(ctx, `
		WITH expanded AS (
			SELECT
				comp->>'name' AS comp_name,
				query,
				COUNT(*) AS cnt
			FROM citation_records
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(competitors) = 'array' THEN competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE merchant_id = $1
			  AND scanned_at >= CURRENT_DATE - interval '30 days'
			  AND comp->>'name' IS NOT NULL AND comp->>'name' != ''
			GROUP BY comp->>'name', query
		),
		ranked AS (
			SELECT comp_name, query, cnt,
				ROW_NUMBER() OVER (PARTITION BY comp_name ORDER BY cnt DESC) AS rn
			FROM expanded
		)
		SELECT comp_name, array_agg(query ORDER BY cnt DESC) AS top_queries
		FROM ranked
		WHERE rn <= 3
		GROUP BY comp_name
	`, merchantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]string)
	for rows.Next() {
		var name string
		var queries []string
		if err := rows.Scan(&name, &queries); err != nil {
			return nil, err
		}
		result[name] = queries
	}
	return result, rows.Err()
}

func scoreAndFilterCompetitors(rows []rawCompetitorRow, weights map[string]float64) []ScoredCompetitor {
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

		// Classify and filter: marketplace and platform entries are never competitors.
		// Retailers are kept but labelled — they're real competition in a different category.
		class := classifyCompetitor(nameLower)
		if class == classMarketplace || class == classPlatform {
			continue
		}

		// Hard-filter: product descriptions masquerading as brand names (> 4 words)
		if len(strings.Fields(r.Name)) > 4 {
			continue
		}

		// Composite score
		//   50% frequency       — how often they're cited (primary signal)
		//   30% platform reach  — weighted by grounding quality (grounded=1.0, mock=0.35)
		//   20% position        — being cited first matters, but treat as soft signal
		freqScore := float64(r.TotalFrequency) / float64(maxFreq)

		// Weighted platform score: grounded platforms count fully, mocks count at 0.35.
		// Max possible weight = sum of all platform weights.
		totalWeight := weights["chatgpt"] + weights["perplexity"] + weights["gemini"]
		earnedWeight := 0.0
		for _, p := range r.Platforms {
			earnedWeight += weights[p]
		}
		platformScore := earnedWeight / totalWeight

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
			Class:             string(class),
			Tier:              competitorTier(score, r.PlatformCount),
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

// ─── AI Readiness Score ──────────────────────────────────────────────────────

// ReadinessDimension is one axis of the AI Readiness Score.
type ReadinessDimension struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`  // 0–10
	Label  string `json:"label"`  // "Not started" | "Weak" | "Building" | "Strong"
	Detail string `json:"detail"` // One-sentence explanation
}

// AIReadinessScore is a scored breakdown of how AI-ready the merchant's brand is.
type AIReadinessScore struct {
	Overall    int                  `json:"overall"`    // 0–100
	Dimensions []ReadinessDimension `json:"dimensions"`
	TopAction  string               `json:"top_action"` // Single highest-leverage next step
}

// GetAIReadinessScore computes a 3-bucket AI readiness score from existing data.
// Buckets map directly to the 3 reasons a brand is invisible to AI:
//   1. Not Found   — AI can't discover you (query coverage, content gaps)
//   2. Not Understood — AI can't parse you (schema, FAQ, brand entity clarity)
//   3. Not Trusted — AI doesn't have third-party evidence you exist (mentions, authority)
func GetAIReadinessScore(ctx context.Context, db *pgxpool.Pool, merchantID int64) (AIReadinessScore, error) {
	var result AIReadinessScore

	// ── Bucket 1: Not Found ───────────────────────────────────────────────────
	// Signal: how many queries the brand appeared in vs total queries run
	var queriesHit, queriesTotal int
	_ = db.QueryRow(ctx, `
		SELECT
			COUNT(CASE WHEN mentioned = true THEN 1 END)::int,
			COUNT(*)::int
		FROM citation_records
		WHERE merchant_id = $1
		  AND scanned_at >= CURRENT_DATE - interval '30 days'
	`, merchantID).Scan(&queriesHit, &queriesTotal)

	notFoundScore := 0
	notFoundDetail := "No scan data yet — run a scan to measure discovery"
	if queriesTotal > 0 {
		pct := (queriesHit * 100) / queriesTotal
		notFoundScore = min(10, pct/10)
		notFoundDetail = fmt.Sprintf("Found in %d of %d queries scanned across all platforms", queriesHit, queriesTotal)
		if queriesHit == 0 {
			notFoundDetail = fmt.Sprintf("Not found in any of %d scanned queries — AI has nothing to retrieve for your brand", queriesTotal)
		}
	}

	// ── Bucket 2: Not Understood ──────────────────────────────────────────────
	// Signal: schema + FAQ coverage (structured signals AI can parse)
	var schemaApplied, schemaPending, faqApplied, faqPending int
	_ = db.QueryRow(ctx, `
		SELECT
			SUM(CASE WHEN fix_type = 'schema' AND status IN ('applied','manual') THEN 1 ELSE 0 END)::int,
			SUM(CASE WHEN fix_type = 'schema' AND status IN ('pending','approved') THEN 1 ELSE 0 END)::int,
			SUM(CASE WHEN fix_type = 'faq' AND status IN ('applied','manual') THEN 1 ELSE 0 END)::int,
			SUM(CASE WHEN fix_type = 'faq' AND status IN ('pending','approved') THEN 1 ELSE 0 END)::int
		FROM pending_fixes
		WHERE merchant_id = $1
	`, merchantID).Scan(&schemaApplied, &schemaPending, &faqApplied, &faqPending)

	notUnderstoodScore := 0
	schemaPoints, faqPoints := 0, 0
	if schemaApplied > 0 {
		schemaPoints = 5
	} else if schemaPending > 0 {
		schemaPoints = 2
	}
	if faqApplied > 0 {
		faqPoints = 5
	} else if faqPending > 0 {
		faqPoints = 2
	}
	notUnderstoodScore = schemaPoints + faqPoints

	notUnderstoodDetail := "No schema or FAQ — AI cannot parse your catalog or match buyer questions"
	switch {
	case schemaApplied > 0 && faqApplied > 0:
		notUnderstoodDetail = "Schema and FAQ applied — AI can parse your products and match buyer queries"
	case schemaApplied > 0:
		notUnderstoodDetail = "Schema applied — FAQ still missing. Add buyer-intent Q&A to match how AI answers shopping questions"
	case faqApplied > 0:
		notUnderstoodDetail = "FAQ applied — schema still missing. Add JSON-LD so AI can extract price, material, and category"
	case schemaPending > 0 || faqPending > 0:
		notUnderstoodDetail = "Structure fixes generated but not yet applied — apply them to become AI-readable"
	}

	// ── Bucket 3: Not Trusted ─────────────────────────────────────────────────
	// Signal: how many platforms cite the brand (cross-platform = external trust signal)
	// and brand recognition rate on grounded (web-search) platforms
	var platformsWithMentions int
	_ = db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT platform)::int
		FROM citation_records
		WHERE merchant_id = $1
		  AND mentioned = true
		  AND scanned_at >= CURRENT_DATE - interval '30 days'
	`, merchantID).Scan(&platformsWithMentions)

	recognition, _ := GetBrandRecognitionStatus(ctx, db, merchantID)
	recognitionBonus := 0
	if recognition.TotalQueries > 0 {
		recognitionBonus = int(recognition.RecognitionRate * 3) // max +3 bonus
	}

	notTrustedScore := min(10, (platformsWithMentions*3)+recognitionBonus)
	notTrustedDetail := "No external citation signals — AI has no third-party evidence your brand exists"
	if platformsWithMentions == 3 {
		notTrustedDetail = "Cited across all 3 AI platforms — strong authority signal"
	} else if platformsWithMentions > 0 {
		notTrustedDetail = fmt.Sprintf("Cited on %d of 3 platforms — earn press mentions and directory listings to build cross-platform trust", platformsWithMentions)
	}

	dims := []ReadinessDimension{
		{
			Name:   "Not Found",
			Score:  notFoundScore,
			Label:  notFoundLabel(notFoundScore),
			Detail: notFoundDetail,
		},
		{
			Name:   "Not Understood",
			Score:  notUnderstoodScore,
			Label:  notUnderstoodLabel(notUnderstoodScore),
			Detail: notUnderstoodDetail,
		},
		{
			Name:   "Not Trusted",
			Score:  notTrustedScore,
			Label:  notTrustedLabel(notTrustedScore),
			Detail: notTrustedDetail,
		},
	}

	total := notFoundScore + notUnderstoodScore + notTrustedScore
	result.Overall = (total * 100) / 30 // 3 dims × 10 max = 30 → scale to 100
	if result.Overall > 100 {
		result.Overall = 100
	}
	result.Dimensions = dims
	result.TopAction = readinessTopAction(notFoundScore, notUnderstoodScore, notTrustedScore)
	return result, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func notFoundLabel(score int) string {
	switch {
	case score >= 8:
		return "Discoverable"
	case score >= 4:
		return "Partially found"
	case score >= 1:
		return "Barely visible"
	default:
		return "Not found"
	}
}

func notUnderstoodLabel(score int) string {
	switch {
	case score >= 8:
		return "Well structured"
	case score >= 4:
		return "Partially structured"
	case score >= 1:
		return "Weak structure"
	default:
		return "No structure"
	}
}

func notTrustedLabel(score int) string {
	switch {
	case score >= 8:
		return "Trusted"
	case score >= 4:
		return "Building trust"
	case score >= 1:
		return "Weak signals"
	default:
		return "Not trusted"
	}
}

func readinessTopAction(notFoundScore, notUnderstoodScore, notTrustedScore int) string {
	// Fix in order: structure first, then discovery, then trust
	if notUnderstoodScore < 4 {
		return "Apply the schema and FAQ fixes — AI cannot understand your brand without them"
	}
	if notFoundScore < 4 {
		return "Create content targeting your top query gaps — AI can't find you for the queries buyers actually ask"
	}
	if notTrustedScore < 4 {
		return "Get your brand mentioned in at least one trusted external source — press, directory, or editorial"
	}
	return "You're building momentum — keep publishing content and earning external citations"
}

// ─── Next 3 Actions ──────────────────────────────────────────────────────────

// NextAction is a single prioritized recommendation for the merchant.
type NextAction struct {
	Priority int    `json:"priority"` // 1 (highest) to 3
	Type     string `json:"type"`     // "fix" | "content" | "structure"
	Title    string `json:"title"`
	Why      string `json:"why"`
	Impact   string `json:"impact"` // e.g. "+18 pts estimated"
}

// GetNextActions returns up to 3 prioritized actions derived from existing scan + fix data.
// Sequence: structure (schema/faq) → content (specific gap queries) → authority (external).
// Structure must come first — there is no point creating content if AI cannot parse it.
func GetNextActions(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]NextAction, error) {
	var actions []NextAction

	// Check structural gaps first — these block everything else
	var schemaCount, faqCount int
	_ = db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM pending_fixes
		WHERE merchant_id = $1 AND fix_type = 'schema' AND status IN ('applied','manual','pending','approved')
	`, merchantID).Scan(&schemaCount)
	_ = db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM pending_fixes
		WHERE merchant_id = $1 AND fix_type = 'faq' AND status IN ('applied','manual','pending','approved')
	`, merchantID).Scan(&faqCount)

	// Also check the onboarding audit — when schema is already live on the store
	// we skip creating a schema fix record, so schemaCount == 0 even though schema exists.
	var auditSchemaLive bool
	_ = db.QueryRow(ctx, `SELECT schema_live FROM merchant_audit WHERE merchant_id = $1`, merchantID).Scan(&auditSchemaLive)

	missingSchema := schemaCount == 0 && !auditSchemaLive
	missingFAQ := faqCount == 0

	// Action 1 — structural blocker: schema if missing, otherwise FAQ
	if missingSchema {
		actions = append(actions, NextAction{
			Priority: 1,
			Type:     "structure",
			Title:    "Add JSON-LD product schema to your store",
			Why:      "AI assistants cannot parse your catalog without structured data — this is the foundation everything else builds on",
			Impact:   "+8 pts estimated",
		})
	} else if missingFAQ {
		actions = append(actions, NextAction{
			Priority: 1,
			Type:     "structure",
			Title:    "Publish an AI-optimized FAQ page",
			Why:      "Buyer-intent Q&A directly matches how ChatGPT and Perplexity answer shopping queries — highest single-fix impact",
			Impact:   "+18 pts estimated",
		})
	} else {
		// Both structure fixes exist — show the highest pending fix as action 1
		var fixTitle, fixType string
		var fixImpact int
		err := db.QueryRow(ctx, `
			SELECT title, fix_type, est_impact
			FROM pending_fixes
			WHERE merchant_id = $1
			  AND status = 'pending'
			  AND fix_layer = 'structure'
			ORDER BY est_impact DESC, created_at ASC
			LIMIT 1
		`, merchantID).Scan(&fixTitle, &fixType, &fixImpact)
		if err == nil {
			actions = append(actions, NextAction{
				Priority: 1,
				Type:     "fix",
				Title:    fixTitle,
				Why:      fmt.Sprintf("Applying this %s fix removes a structural gap AI is penalising you for", fixType),
				Impact:   fmt.Sprintf("+%d pts estimated", fixImpact),
			})
		}
	}

	// Action 2 — highest-opportunity content gap from actual scan data
	// Query where the most competitors appeared but this merchant didn't = most winnable
	var gapQuery string
	var competitorCount int
	err2 := db.QueryRow(ctx, `
		WITH latest AS (
			SELECT MAX(scanned_at) AS ts FROM citation_records WHERE merchant_id = $1
		),
		per_query AS (
			SELECT
				c.query,
				COUNT(DISTINCT comp->>'name')::int AS competitor_count,
				bool_or(c.mentioned)               AS any_mentioned
			FROM citation_records c, latest
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(c.competitors) = 'array' THEN c.competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE c.merchant_id = $1
			  AND c.scanned_at = latest.ts
			GROUP BY c.query
		)
		SELECT query, competitor_count
		FROM per_query
		WHERE NOT any_mentioned AND competitor_count >= 2
		ORDER BY competitor_count DESC
		LIMIT 1
	`, merchantID).Scan(&gapQuery, &competitorCount)
	if err2 == nil {
		actions = append(actions, NextAction{
			Priority: 2,
			Type:     "content",
			Title:    fmt.Sprintf("Create content for: \"%s\"", gapQuery),
			Why:      fmt.Sprintf("AI named %d competitors for this exact query — it knows the topic. Add a page or FAQ answer targeting this query so you appear too.", competitorCount),
			Impact:   "High — AI-ready query with existing competition",
		})
	} else {
		// No gap data yet — show highest content fix
		var fixTitle, fixType string
		var fixImpact int
		err := db.QueryRow(ctx, `
			SELECT title, fix_type, est_impact
			FROM pending_fixes
			WHERE merchant_id = $1
			  AND status = 'pending'
			  AND fix_layer = 'content'
			ORDER BY est_impact DESC, created_at ASC
			LIMIT 1
		`, merchantID).Scan(&fixTitle, &fixType, &fixImpact)
		if err == nil {
			actions = append(actions, NextAction{
				Priority: 2,
				Type:     "fix",
				Title:    fixTitle,
				Why:      fmt.Sprintf("This %s improvement targets the queries AI is using to compare brands in your category", fixType),
				Impact:   fmt.Sprintf("+%d pts estimated", fixImpact),
			})
		}
	}

	// Action 3 — authority layer: external mentions are non-negotiable for long-term visibility
	// Check if brand has any external citation signal
	var mentionedPlatforms int
	_ = db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT platform)::int
		FROM citation_records
		WHERE merchant_id = $1
		  AND mentioned = true
		  AND scanned_at >= CURRENT_DATE - interval '30 days'
	`, merchantID).Scan(&mentionedPlatforms)

	if mentionedPlatforms == 0 {
		actions = append(actions, NextAction{
			Priority: 3,
			Type:     "authority",
			Title:    "Get your brand mentioned in one trusted external source",
			Why:      "AI has no third-party evidence your brand exists. One press mention, directory listing, or editorial citation changes this — and it compounds over time.",
			Impact:   "Foundational — no external mentions = no AI trust",
		})
	} else {
		actions = append(actions, NextAction{
			Priority: 3,
			Type:     "authority",
			Title:    "Earn backlinks from category-specific publications",
			Why:      fmt.Sprintf("You appear on %d of 3 platforms. Brands cited in editorial content get picked up by the platforms where you're still invisible.", mentionedPlatforms),
			Impact:   "Compounds over time — each mention widens platform reach",
		})
	}

	return actions, nil
}
