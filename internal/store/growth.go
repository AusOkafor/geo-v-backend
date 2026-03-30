package store

import (
	"context"
	"fmt"
	"strings"
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
	Type     string `json:"type"`     // "social" | "directory" | "content" | "outreach"
	Title    string `json:"title"`
	Action   string `json:"action"`   // Exact thing to do
	Template string `json:"template"` // Copy-paste ready, empty if not applicable
	Timeline string `json:"timeline"` // "24h" | "48h" | "1 week"
	Impact   string `json:"impact"`
}

// GetQuickWins returns 4–5 specific actions the merchant can take immediately
// to get their first external citation — the step that unlocks AI visibility.
// brandName and category come from the merchant record (caller-supplied).
func GetQuickWins(ctx context.Context, db *pgxpool.Pool, merchantID int64, brandName, category string) ([]QuickWin, error) {
	// Pull top query gap (most competitors cited = highest opportunity)
	var topGapQuery string
	_ = db.QueryRow(ctx, `
		WITH latest AS (SELECT MAX(scanned_at) AS ts FROM citation_records WHERE merchant_id = $1),
		gaps AS (
			SELECT c.query, COUNT(DISTINCT comp->>'name')::int AS comp_count, bool_or(c.mentioned) AS any_mentioned
			FROM citation_records c, latest
			CROSS JOIN LATERAL jsonb_array_elements(
				CASE WHEN jsonb_typeof(c.competitors) = 'array' THEN c.competitors ELSE '[]'::jsonb END
			) AS comp
			WHERE c.merchant_id = $1 AND c.scanned_at = latest.ts
			GROUP BY c.query
		)
		SELECT query FROM gaps WHERE NOT any_mentioned ORDER BY comp_count DESC LIMIT 1
	`, merchantID).Scan(&topGapQuery)

	// Pull top competitor name
	var topCompetitor string
	_ = db.QueryRow(ctx, `
		SELECT comp->>'name'
		FROM citation_records
		CROSS JOIN LATERAL jsonb_array_elements(
			CASE WHEN jsonb_typeof(competitors) = 'array' THEN competitors ELSE '[]'::jsonb END
		) AS comp
		WHERE merchant_id = $1
		  AND scanned_at >= CURRENT_DATE - interval '30 days'
		  AND comp->>'name' IS NOT NULL AND comp->>'name' != ''
		GROUP BY comp->>'name'
		ORDER BY COUNT(*) DESC
		LIMIT 1
	`, merchantID).Scan(&topCompetitor)

	cat := strings.ToLower(category)
	if cat == "" {
		cat = "products"
	}

	var wins []QuickWin

	// Win 1: Reddit post — fastest, Perplexity indexes Reddit aggressively
	subreddit, redditTemplate := redditQuickWin(cat, brandName, topGapQuery)
	wins = append(wins, QuickWin{
		Type:     "social",
		Title:    fmt.Sprintf("Post in r/%s (Perplexity indexes Reddit fast)", subreddit),
		Action:   fmt.Sprintf("Post a genuine question or recommendation in r/%s that naturally mentions %s", subreddit, brandName),
		Template: redditTemplate,
		Timeline: "24h",
		Impact:   "Perplexity sonar picks up Reddit within 24–48h — this can trigger your first AI citation",
	})

	// Win 2: Comparison content — "Brand vs Competitor" pages rank immediately for AI
	if topCompetitor != "" {
		wins = append(wins, QuickWin{
			Type:     "content",
			Title:    fmt.Sprintf("Create a \"%s vs %s\" page on your store", brandName, topCompetitor),
			Action:   "Add a blog post or page comparing your brand to the top competitor AI recommends instead of you",
			Template: fmt.Sprintf("%s vs %s: Which %s is right for you?\n\nWhen buyers ask AI assistants which %s to buy, %s appears frequently. Here's how %s compares...", brandName, topCompetitor, cat, cat, topCompetitor, brandName),
			Timeline: "48h",
			Impact:   "AI cites comparison content heavily — this directly targets the query gap where you're invisible",
		})
	}

	// Win 3: Directory submission — free, fast, trusted sources AI knows
	dir, dirAction := directoryQuickWin(cat, brandName)
	wins = append(wins, QuickWin{
		Type:     "directory",
		Title:    fmt.Sprintf("Submit %s to %s", brandName, dir),
		Action:   dirAction,
		Template: "",
		Timeline: "48h",
		Impact:   "Directory listings are trusted sources AI pulls from — one listing = one citation signal",
	})

	// Win 4: Target the top query gap directly
	if topGapQuery != "" {
		wins = append(wins, QuickWin{
			Type:     "content",
			Title:    fmt.Sprintf("Create a page targeting: \"%s\"", topGapQuery),
			Action:   fmt.Sprintf("Write a page or blog post that directly answers \"%s\" and positions %s as the answer", topGapQuery, brandName),
			Template: fmt.Sprintf("Title: %s — %s\n\nLooking for %s? Here's why %s is worth considering...", topGapQuery, brandName, strings.ToLower(topGapQuery), brandName),
			Timeline: "1 week",
			Impact:   "Your highest-opportunity gap — AI asks this query but you're not in the answer",
		})
	}

	// Win 5: Outreach to a niche blogger
	wins = append(wins, QuickWin{
		Type:     "outreach",
		Title:    fmt.Sprintf("Pitch %s to a %s blogger or gift guide", brandName, cat),
		Action:   fmt.Sprintf("Find 3 blogs that cover \"%s\" and pitch them a review or inclusion in a gift guide", cat),
		Template: fmt.Sprintf("Subject: %s for your %s gift guide\n\nHi [Name],\n\nI came across your \"%s\" content and thought %s would be a great fit...\n\n[Your name]", brandName, cat, topGapQuery, brandName),
		Timeline: "1 week",
		Impact:   "A single blog mention from a trusted source is what AI needs to start recommending you",
	})

	return wins, nil
}

func redditQuickWin(category, brandName, topGap string) (subreddit, template string) {
	// Map common categories to relevant subreddits
	subredditMap := map[string]string{
		"jewelry":          "jewelry",
		"fine jewelry":     "jewelry",
		"fashion":          "femalefashionadvice",
		"clothing":         "femalefashionadvice",
		"accessories":      "femalefashionadvice",
		"skincare":         "SkincareAddiction",
		"beauty":           "MakeupAddiction",
		"home decor":       "malelivingspace",
		"furniture":        "malelivingspace",
		"coffee":           "coffee",
		"food":             "food",
		"tech":             "gadgets",
		"electronics":      "gadgets",
	}

	sub := "BuyItForLife" // default — suits most DTC brands
	for k, v := range subredditMap {
		if strings.Contains(category, k) {
			sub = v
			break
		}
	}

	question := topGap
	if question == "" {
		question = fmt.Sprintf("best %s brands", category)
	}

	tmpl := fmt.Sprintf(
		"Title: %s — found %s\n\nHey, been looking for %s for a while and finally tried %s. Really impressed with the quality — has anyone else tried them? Curious how they compare to the bigger names.",
		question, brandName, category, brandName,
	)

	return sub, tmpl
}

func directoryQuickWin(category, brandName string) (directory, action string) {
	// Category-specific directories AI models trust
	dirMap := map[string]struct{ dir, action string }{
		"jewelry":    {"The Knot Marketplace", fmt.Sprintf("Submit %s at theknot.com/marketplace — The Knot is heavily indexed by all three AI platforms for jewelry recommendations", brandName)},
		"fine jewelry": {"The Knot Marketplace", fmt.Sprintf("Submit %s at theknot.com/marketplace — The Knot is heavily indexed by all three AI platforms for jewelry recommendations", brandName)},
		"skincare":   {"Influenster", fmt.Sprintf("Create a brand profile for %s on Influenster — Perplexity frequently cites Influenster reviews", brandName)},
		"clothing":   {"Fashionista", fmt.Sprintf("Submit %s to Fashionista's brand directory — fashion AI queries frequently pull from Fashionista", brandName)},
		"home decor": {"Houzz", fmt.Sprintf("List %s products on Houzz — AI home decor recommendations frequently cite Houzz", brandName)},
		"coffee":     {"Coffeereview.com", fmt.Sprintf("Submit %s for a review on coffeereview.com — cited in most AI coffee queries", brandName)},
	}

	for k, v := range dirMap {
		if strings.Contains(category, k) {
			return v.dir, v.action
		}
	}

	// Default: Trustpilot — universally trusted, cited by all AI platforms
	return "Trustpilot", fmt.Sprintf("Create a free Trustpilot profile for %s and collect 5+ reviews — Trustpilot is cited by ChatGPT, Perplexity, and Gemini across almost every product category", brandName)
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
