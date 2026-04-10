package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Visibility Pipeline ─────────────────────────────────────────────────────

// PipelineStep is one stage in the path from "invisible" to "AI-cited".
type PipelineStep struct {
	Step   string `json:"step"`   // "content" | "indexed" | "referenced" | "cited"
	Label  string `json:"label"`
	Status string `json:"status"` // "complete" | "in_progress" | "pending"
	Detail string `json:"detail"`
}

// VisibilityPipeline shows exactly where the merchant is in the AI visibility journey.
// It converts "apply fix and wait" into visible, named progress stages so merchants
// understand they are moving — even when the scan score is still 0.
type VisibilityPipeline struct {
	Steps       []PipelineStep `json:"steps"`
	CurrentStep int            `json:"current_step"` // 1–4
	Message     string         `json:"message"`      // Contextual guidance for the current stage
}

// GetVisibilityPipeline returns the merchant's current position in the 4-stage
// pipeline: content created → indexed → externally referenced → AI cited.
func GetVisibilityPipeline(ctx context.Context, db *pgxpool.Pool, merchantID int64) (VisibilityPipeline, error) {
	// Step 1: Content — any fix applied or marked manual
	var fixAppliedAt *time.Time
	var fixType string
	_ = db.QueryRow(ctx, `
		SELECT fix_type, applied_at
		FROM pending_fixes
		WHERE merchant_id = $1
		  AND status IN ('applied', 'manual')
		ORDER BY COALESCE(applied_at, updated_at) ASC
		LIMIT 1
	`, merchantID).Scan(&fixType, &fixAppliedAt)

	contentDone := fixAppliedAt != nil || fixType != ""

	// Step 2: Indexed — estimate based on days elapsed since content was published.
	// We don't have a Google Search Console integration, so we use time as a proxy:
	// pages typically get indexed within 7 days on Shopify (sitemap auto-submitted).
	indexedDone := false
	indexedDetail := "Waiting for content to be published first"
	if contentDone {
		daysSinceApplied := 0
		if fixAppliedAt != nil {
			daysSinceApplied = int(time.Since(*fixAppliedAt).Hours() / 24)
		}
		switch {
		case daysSinceApplied >= 7:
			indexedDone = true
			indexedDetail = fmt.Sprintf("Published %d days ago — estimated as indexed", daysSinceApplied)
		case daysSinceApplied > 0:
			indexedDetail = fmt.Sprintf("Published %d day(s) ago — typically indexed within 7 days", daysSinceApplied)
		default:
			indexedDetail = "Content published — indexing usually takes 1–7 days"
		}
	}

	// Step 3: External references — we can't auto-detect backlinks without an
	// external API (Ahrefs, Moz, etc.). Be honest: tell the merchant what to do.
	referencedDetail := "No external link detection yet — see Quick Wins for how to get your first mention"

	// Step 4: AI cited — merchant mentioned in at least one query in latest scan
	var mentionedCount int
	_ = db.QueryRow(ctx, `
		SELECT SUM(CASE WHEN mentioned THEN 1 ELSE 0 END)::int
		FROM citation_records
		WHERE merchant_id = $1
		  AND scanned_at = (SELECT MAX(scanned_at) FROM citation_records WHERE merchant_id = $1)
	`, merchantID).Scan(&mentionedCount)
	citedDone := mentionedCount > 0

	// Determine current step and build pipeline
	steps := []PipelineStep{
		{
			Step:   "content",
			Label:  "Content created",
			Status: pipelineStatus(contentDone, false),
			Detail: contentDetail(contentDone, fixType),
		},
		{
			Step:   "indexed",
			Label:  "Indexed by search engines",
			Status: pipelineStatus(indexedDone, !contentDone),
			Detail: indexedDetail,
		},
		{
			Step:   "referenced",
			Label:  "Referenced by external sources",
			Status: pipelineStatus(false, !indexedDone), // never auto-complete — requires human action
			Detail: referencedDetail,
		},
		{
			Step:   "cited",
			Label:  "Cited by AI models",
			Status: pipelineStatus(citedDone, true),
			Detail: citedDetail(citedDone, mentionedCount),
		},
	}

	currentStep := 1
	switch {
	case citedDone:
		currentStep = 4
	case indexedDone:
		currentStep = 3 // content + indexed done, need external reference
	case contentDone:
		currentStep = 2 // content done, waiting for indexing
	}

	return VisibilityPipeline{
		Steps:       steps,
		CurrentStep: currentStep,
		Message:     pipelineMessage(currentStep, contentDone, indexedDone, citedDone),
	}, nil
}

func pipelineStatus(done, blocked bool) string {
	if done {
		return "complete"
	}
	if blocked {
		return "pending"
	}
	return "in_progress"
}

func contentDetail(done bool, fixType string) string {
	if !done {
		return "No content published yet — apply your FAQ or schema fix to start"
	}
	switch fixType {
	case "faq":
		return "AI-optimized FAQ published — content matches buyer intent queries"
	case "schema":
		return "JSON-LD schema published — product data is now AI-parseable"
	case "description":
		return "AI-optimized product description published"
	default:
		return "Content fix applied to your store"
	}
}

func citedDetail(done bool, count int) string {
	if done {
		return fmt.Sprintf("Mentioned in %d AI query response(s) — you are in the game", count)
	}
	return "Not yet cited — external references are the final unlock"
}

func pipelineMessage(step int, contentDone, _, _ bool) string {
	switch step {
	case 1:
		return "You are invisible to AI right now. Apply the FAQ fix — it's the first piece of content that gives AI a reason to mention you."
	case 2:
		if !contentDone {
			return "No content published yet. Apply your FAQ fix to move forward."
		}
		return "Content published. You are not stuck — you are waiting for Google to index your page. This typically takes 1–7 days. Use this time to get your first external mention."
	case 3:
		return "Your content is indexed. AI can now find it — but won't cite it until an external source validates it. Get one mention from a blog, directory, or Reddit thread. That is the unlock."
	case 4:
		return "AI is citing your brand. Keep earning citations and run scans to track momentum."
	default:
		return "Run a scan to see your current pipeline status."
	}
}

// ─── Quick Wins ──────────────────────────────────────────────────────────────

// QuickWin is a specific action the merchant can take in 24–72 hours to accelerate
// their first AI citation. Unlike fixes (which improve the store), quick wins target
// external signals — the factor AI uses most to decide who to recommend.
type QuickWin struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Copy      string   `json:"copy"`
	ActionURL string   `json:"action_url"`
	Tags      []string `json:"tags"`
	Effort    string   `json:"effort"` // "15 min" | "30 min" | "1 hr" etc
}

// GetQuickWins returns 4–5 specific actions the merchant can take immediately
// to get their first external citation — the step that unlocks AI visibility.
// brandName and category come from the merchant record (caller-supplied).
func GetQuickWins(ctx context.Context, db *pgxpool.Pool, merchantID int64, brandName, category string) ([]QuickWin, error) {
	// Build quick wins from real merchant gaps (audit + scan-derived).
	var wins []QuickWin

	// 1) Collections needing descriptions (highest leverage for category queries).
	collections, _ := GetCollectionsEligibleForFix(ctx, db, merchantID)
	if n := len(collections); n > 0 {
		wins = append(wins, QuickWin{
			ID:        "collections_descriptions",
			Title:     fmt.Sprintf("Add descriptions to %d collection%s", n, plural(n)),
			Copy:      "Collection descriptions are a top signal for category (product search) queries. Add 2–3 sentences describing materials, use-cases, and who it’s for.",
			ActionURL: "/dashboard/fixes",
			Tags:      []string{"category_visibility", "collections"},
			Effort:    "30–60 min",
		})
	}

	// 2) Products missing critical attributes (e.g., material) and low completeness.
	products, _ := GetProductsNeedingAttention(ctx, db, merchantID, 50)
	missingMaterial := 0
	for _, p := range products {
		if p.MissingMaterialInfo {
			missingMaterial++
		}
	}
	if missingMaterial > 0 {
		wins = append(wins, QuickWin{
			ID:        "products_material_info",
			Title:     fmt.Sprintf("%d product%s missing material info", missingMaterial, plural(missingMaterial)),
			Copy:      "Material is required for AI product understanding (and many buyer prompts). Add it to product descriptions and specs for your top sellers first.",
			ActionURL: "/dashboard/audit",
			Tags:      []string{"product_data", "category_visibility"},
			Effort:    "15–45 min",
		})
	}

	// 3) Placeholder FAQ / policy content (hurts trust + answerability).
	pages, _ := GetPagesEligibleForFix(ctx, db, merchantID)
	faqPlaceholder := false
	for _, pg := range pages {
		if pg.PageType == "faq" && pg.IsPlaceholder {
			faqPlaceholder = true
			break
		}
	}
	if faqPlaceholder {
		wins = append(wins, QuickWin{
			ID:        "faq_placeholders",
			Title:     "FAQ page contains placeholder text",
			Copy:      "Replace placeholders with real shipping/returns/materials policies. Buyer-intent questions are where AI decides whether to cite you.",
			ActionURL: "/dashboard/settings",
			Tags:      []string{"trust", "faq"},
			Effort:    "20–30 min",
		})
	}

	// 4) Use actual scan gaps to suggest a specific content target.
	var topGapQuery string
	_ = db.QueryRow(ctx, `
		WITH latest AS (SELECT MAX(scanned_at) AS ts FROM citation_records WHERE merchant_id = $1),
		gaps AS (
			SELECT
				c.query,
				COUNT(DISTINCT lower(regexp_replace(trim(comp->>'name'), '\s+', ' ', 'g')))
					FILTER (WHERE comp->>'name' IS NOT NULL AND comp->>'name' != ''
						AND array_length(regexp_split_to_array(trim(comp->>'name'), '\s+'), 1) <= 4
						AND lower(comp->>'name') NOT LIKE '%best%'
						AND lower(comp->>'name') NOT LIKE '%top%'
						AND lower(comp->>'name') NOT LIKE '%under $%'
						AND lower(comp->>'name') NOT LIKE '%rated%'
						AND lower(comp->>'name') NOT LIKE '%brands%'
						AND lower(comp->>'name') NOT LIKE '%store%'
					)::int AS comp_count,
				bool_or(c.mentioned) AS any_mentioned
			FROM citation_records c, latest
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(c.competitors) = 'array' THEN c.competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE c.merchant_id = $1 AND c.scanned_at = latest.ts
			GROUP BY c.query
		)
		SELECT query FROM gaps WHERE NOT any_mentioned ORDER BY comp_count DESC LIMIT 1
	`, merchantID).Scan(&topGapQuery)
	if topGapQuery != "" {
		wins = append(wins, QuickWin{
			ID:        "top_query_gap",
			Title:     fmt.Sprintf("Create a page answering: “%s”", topGapQuery),
			Copy:      "You’re currently invisible on this exact buyer query. A focused page (or FAQ section) targeting it is the fastest path to a first category citation.",
			ActionURL: "/dashboard/fixes",
			Tags:      []string{"content", "category_visibility"},
			Effort:    "45–90 min",
		})
	}

	// Keep list short and high-signal.
	if len(wins) > 5 {
		wins = wins[:5]
	}
	if wins == nil {
		wins = []QuickWin{}
	}
	return wins, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ─── Scan Progress (Before / After) ─────────────────────────────────────────

// ScanProgress compares the two most recent scans to show concrete momentum.
// This replaces "apply fix and wait" with "here is exactly what changed."
type ScanProgress struct {
	PreviousScanDate string         `json:"previous_scan_date"` // empty if only one scan exists
	LatestScanDate   string         `json:"latest_scan_date"`
	ScoreDelta       map[string]int `json:"score_delta"`    // platform → score change (positive = improvement)
	MentionDelta     int            `json:"mention_delta"`  // change in total mentions
	NewCompetitors   []string       `json:"new_competitors"` // competitors newly appearing since last scan
	ResolvedGaps     []string       `json:"resolved_gaps"`  // queries now answered (you are in the answer)
	NewGaps          []string       `json:"new_gaps"`       // queries newly missing
	Narrative        string         `json:"narrative"`      // Plain-English explanation of what changed
}

// GetScanProgress returns a delta between the latest two scans.
// If only one scan exists, PreviousScanDate is empty and all deltas are 0.
func GetScanProgress(ctx context.Context, db *pgxpool.Pool, merchantID int64) (ScanProgress, error) {
	var result ScanProgress

	// Get the two most recent distinct scan dates
	rows, err := db.Query(ctx, `
		SELECT DISTINCT scanned_at::text
		FROM citation_records
		WHERE merchant_id = $1
		ORDER BY scanned_at DESC
		LIMIT 2
	`, merchantID)
	if err != nil {
		return result, fmt.Errorf("store.GetScanProgress: %w", err)
	}
	defer rows.Close()

	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return result, err
		}
		dates = append(dates, d)
	}
	rows.Close()

	if len(dates) == 0 {
		result.Narrative = "No scan data yet — run your first scan to start tracking progress."
		result.ScoreDelta = map[string]int{}
		result.NewCompetitors = []string{}
		result.ResolvedGaps = []string{}
		result.NewGaps = []string{}
		return result, nil
	}

	result.LatestScanDate = dates[0]

	if len(dates) == 1 {
		// Only one scan — show absolute state, no delta
		var mentionedQueries int
		_ = db.QueryRow(ctx, `
			SELECT SUM(CASE WHEN mentioned THEN 1 ELSE 0 END)::int
			FROM citation_records WHERE merchant_id = $1 AND scanned_at::text = $2
		`, merchantID, dates[0]).Scan(&mentionedQueries)

		result.ScoreDelta = map[string]int{"chatgpt": 0, "perplexity": 0, "gemini": 0}
		result.NewCompetitors = []string{}
		result.ResolvedGaps = []string{}
		result.NewGaps = []string{}
		result.Narrative = fmt.Sprintf(
			"First scan completed on %s. You were mentioned in %d queries. Run a second scan after applying your fixes to see before/after comparison.",
			dates[0], mentionedQueries,
		)
		return result, nil
	}

	result.PreviousScanDate = dates[1]
	latest, prev := dates[0], dates[1]

	// Score delta per platform
	result.ScoreDelta = map[string]int{}
	scoreRows, err := db.Query(ctx, `
		SELECT
			platform,
			SUM(CASE WHEN scanned_at::text = $2 THEN CASE WHEN mentioned THEN 1 ELSE 0 END ELSE 0 END) AS latest_hits,
			SUM(CASE WHEN scanned_at::text = $2 THEN 1 ELSE 0 END)                                      AS latest_total,
			SUM(CASE WHEN scanned_at::text = $3 THEN CASE WHEN mentioned THEN 1 ELSE 0 END ELSE 0 END) AS prev_hits,
			SUM(CASE WHEN scanned_at::text = $3 THEN 1 ELSE 0 END)                                      AS prev_total
		FROM citation_records
		WHERE merchant_id = $1 AND scanned_at::text IN ($2, $3)
		GROUP BY platform
	`, merchantID, latest, prev)
	if err != nil {
		return result, err
	}
	defer scoreRows.Close()

	latestMentions, prevMentions := 0, 0
	for scoreRows.Next() {
		var platform string
		var latestHits, latestTotal, prevHits, prevTotal int
		if err := scoreRows.Scan(&platform, &latestHits, &latestTotal, &prevHits, &prevTotal); err != nil {
			return result, err
		}
		latestScore := 0
		if latestTotal > 0 {
			latestScore = (latestHits * 100) / latestTotal
		}
		prevScore := 0
		if prevTotal > 0 {
			prevScore = (prevHits * 100) / prevTotal
		}
		result.ScoreDelta[platform] = latestScore - prevScore
		latestMentions += latestHits
		prevMentions += prevHits
	}
	scoreRows.Close()
	result.MentionDelta = latestMentions - prevMentions

	// New competitors (appeared in latest but not previous)
	compRows, err := db.Query(ctx, `
		WITH latest_comps AS (
			SELECT DISTINCT comp->>'name' AS name
			FROM citation_records
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(competitors) = 'array' THEN competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE merchant_id = $1 AND scanned_at::text = $2
			  AND comp->>'name' IS NOT NULL AND comp->>'name' != ''
		),
		prev_comps AS (
			SELECT DISTINCT comp->>'name' AS name
			FROM citation_records
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(competitors) = 'array' THEN competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE merchant_id = $1 AND scanned_at::text = $3
			  AND comp->>'name' IS NOT NULL AND comp->>'name' != ''
		)
		SELECT lc.name FROM latest_comps lc
		LEFT JOIN prev_comps pc ON lower(lc.name) = lower(pc.name)
		WHERE pc.name IS NULL
		LIMIT 5
	`, merchantID, latest, prev)
	if err != nil {
		return result, err
	}
	defer compRows.Close()
	for compRows.Next() {
		var name string
		if err := compRows.Scan(&name); err != nil {
			return result, err
		}
		result.NewCompetitors = append(result.NewCompetitors, name)
	}
	compRows.Close()
	if result.NewCompetitors == nil {
		result.NewCompetitors = []string{}
	}

	// Resolved gaps (mentioned in latest but not in previous — you got into the answer)
	resolvedRows, err := db.Query(ctx, `
		WITH latest_q AS (
			SELECT query, bool_or(mentioned) AS mentioned
			FROM citation_records WHERE merchant_id = $1 AND scanned_at::text = $2
			GROUP BY query
		),
		prev_q AS (
			SELECT query, bool_or(mentioned) AS mentioned
			FROM citation_records WHERE merchant_id = $1 AND scanned_at::text = $3
			GROUP BY query
		)
		SELECT l.query FROM latest_q l JOIN prev_q p ON l.query = p.query
		WHERE l.mentioned = true AND p.mentioned = false
		LIMIT 5
	`, merchantID, latest, prev)
	if err != nil {
		return result, err
	}
	defer resolvedRows.Close()
	for resolvedRows.Next() {
		var q string
		if err := resolvedRows.Scan(&q); err != nil {
			return result, err
		}
		result.ResolvedGaps = append(result.ResolvedGaps, q)
	}
	resolvedRows.Close()
	if result.ResolvedGaps == nil {
		result.ResolvedGaps = []string{}
	}

	// New gaps (not mentioned in latest but were mentioned in previous — regression)
	newGapRows, err := db.Query(ctx, `
		WITH latest_q AS (
			SELECT query, bool_or(mentioned) AS mentioned
			FROM citation_records WHERE merchant_id = $1 AND scanned_at::text = $2
			GROUP BY query
		),
		prev_q AS (
			SELECT query, bool_or(mentioned) AS mentioned
			FROM citation_records WHERE merchant_id = $1 AND scanned_at::text = $3
			GROUP BY query
		)
		SELECT l.query FROM latest_q l JOIN prev_q p ON l.query = p.query
		WHERE l.mentioned = false AND p.mentioned = true
		LIMIT 5
	`, merchantID, latest, prev)
	if err != nil {
		return result, err
	}
	defer newGapRows.Close()
	for newGapRows.Next() {
		var q string
		if err := newGapRows.Scan(&q); err != nil {
			return result, err
		}
		result.NewGaps = append(result.NewGaps, q)
	}
	newGapRows.Close()
	if result.NewGaps == nil {
		result.NewGaps = []string{}
	}

	result.Narrative = buildProgressNarrative(result, latestMentions, prevMentions)
	return result, nil
}

func buildProgressNarrative(r ScanProgress, latestMentions, prevMentions int) string {
	if latestMentions == 0 && prevMentions == 0 {
		return fmt.Sprintf(
			"Scans on %s and %s both show 0 citations. This is expected at this stage — your content fix hasn't been externally validated yet. Apply the FAQ fix and get one external mention to break through.",
			r.PreviousScanDate, r.LatestScanDate,
		)
	}
	if latestMentions > prevMentions {
		delta := latestMentions - prevMentions
		return fmt.Sprintf(
			"Progress detected: +%d new query mention(s) since %s. You moved from %d to %d citations. Keep earning external mentions to accelerate.",
			delta, r.PreviousScanDate, prevMentions, latestMentions,
		)
	}
	if latestMentions < prevMentions {
		return fmt.Sprintf(
			"Citations dropped from %d to %d since %s. This can happen when AI models update their training or search index. Run the FAQ and schema fixes to rebuild your signal.",
			prevMentions, latestMentions, r.PreviousScanDate,
		)
	}
	return fmt.Sprintf(
		"No change between %s and %s (%d citations both scans). Content is indexed but not yet validated externally — one press mention or directory listing is the unlock.",
		r.PreviousScanDate, r.LatestScanDate, latestMentions,
	)
}
