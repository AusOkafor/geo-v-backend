package jobs

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// OnboardingAuditWorker reads the merchant's live Shopify store state and
// persists results to merchant_audit, merchant_product_audit, merchant_collection_audit,
// merchant_page_audit, and merchant_audit_progress.
// After completing it queues fix generation so the merchant sees fixes immediately
// after install without needing to trigger a full AI scan first.
type OnboardingAuditWorker struct {
	river.WorkerDefaults[OnboardingAuditJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
	riverClient   *river.Client[pgx.Tx]
}

func NewOnboardingAuditWorker(db *pgxpool.Pool, encKey []byte, riverClient *river.Client[pgx.Tx]) *OnboardingAuditWorker {
	return &OnboardingAuditWorker{db: db, encryptionKey: encKey, riverClient: riverClient}
}

func (w *OnboardingAuditWorker) Work(ctx context.Context, job *river.Job[OnboardingAuditJobArgs]) error {
	merchantID := job.Args.MerchantID

	merchant, err := store.GetMerchant(ctx, w.db, merchantID)
	if err != nil {
		return err
	}
	if !merchant.Active {
		return nil
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, w.encryptionKey)
	if err != nil {
		return err
	}

	audit := &store.MerchantAudit{MerchantID: merchantID}

	// ── 1. Schema live? ────────────────────────────────────────────────────────
	schemaVal, err := shopify.GetShopMetafieldValue(ctx, merchant.ShopDomain, token, "geo_visibility", "schema_json")
	if err != nil {
		slog.Warn("onboarding audit: schema check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}
	audit.SchemaLive = schemaVal != ""

	// ── 2. Products ────────────────────────────────────────────────────────────
	products, err := shopify.FetchAllProducts(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: product fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}

	var totalWords int
	productsNeedingAttention := 0
	for _, p := range products {
		plainText := stripHTML(p.DescriptionHTML)
		wc := len(strings.Fields(plainText))
		totalWords += wc

		if wc == 0 {
			audit.ProductsWithNoDescription++
		} else if wc < 50 {
			audit.ProductsWithShortDescription++
		}

		needsAttention := wc < 50 ||
			!containsAny(plainText, materialKeywords) ||
			!containsAny(plainText, sizingKeywords) ||
			!containsAny(plainText, careKeywords)

		score := descCompletenessScore(plainText)
		if needsAttention {
			productsNeedingAttention++
		}

		pa := &store.ProductAudit{
			MerchantID:              merchantID,
			ProductID:               p.ID,
			ProductHandle:           "", // not in ProductNode — handle is not fetched in current query
			ProductTitle:            p.Title,
			CurrentDescriptionWords: wc,
			MissingMaterialInfo:     !containsAny(plainText, materialKeywords),
			MissingSizingInfo:       !containsAny(plainText, sizingKeywords),
			MissingCareInstructions: !containsAny(plainText, careKeywords),
			CompletenessScore:       score,
			NeedsAttention:          needsAttention,
		}
		if err := store.UpsertProductAudit(ctx, w.db, pa); err != nil {
			slog.Warn("onboarding audit: upsert product audit failed (non-fatal)", "product_id", p.ID, "err", err)
		}
	}

	if len(products) > 0 {
		audit.AvgDescriptionWords = totalWords / len(products)
	}

	// ── 3. Review app ──────────────────────────────────────────────────────────
	themeResult, err := shopify.DetectReviewAppFromTheme(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: review app detection failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else if themeResult != nil {
		audit.ReviewApp = themeResult.App
	}

	// ── 4. FAQ page (aggregate flag) ───────────────────────────────────────────
	hasFAQ, err := shopify.HasFAQPage(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: FAQ page check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else {
		audit.HasFAQPage = hasFAQ
	}

	// ── 5. Collections ─────────────────────────────────────────────────────────
	collections, err := shopify.FetchAllCollections(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: collection fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}
	collectionsNeedingAttention := 0
	for _, c := range collections {
		wc := len(strings.Fields(stripHTML(c.Description)))
		// Eligible for AI description: has products, description is absent or very short
		eligible := c.ProductCount >= 3 && wc < 50
		needsAttention := c.ProductCount >= 3 && wc < 50
		if needsAttention {
			collectionsNeedingAttention++
		}
		ca := &store.CollectionAudit{
			MerchantID:              merchantID,
			CollectionID:            c.ID,
			CollectionHandle:        c.Handle,
			CollectionTitle:         c.Title,
			CurrentDescriptionWords: wc,
			ProductCount:            c.ProductCount,
			AIDescriptionEligible:   eligible,
			NeedsAttention:          needsAttention,
		}
		if err := store.UpsertCollectionAudit(ctx, w.db, ca); err != nil {
			slog.Warn("onboarding audit: upsert collection audit failed (non-fatal)", "collection_id", c.ID, "err", err)
		}
	}

	// ── 6. Pages ───────────────────────────────────────────────────────────────
	pages, err := shopify.FetchAllPages(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: page fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}

	// Track which essential types were found
	foundPageTypes := map[string]bool{}
	for _, pg := range pages {
		pt := classifyPage(pg.Title, pg.Handle)
		if pt == "other" {
			continue
		}
		foundPageTypes[pt] = true
		body := stripHTML(pg.Body)
		wc := len(strings.Fields(body))

		pa := &store.PageAudit{
			MerchantID:        merchantID,
			PageID:            pg.ID,
			PageHandle:        pg.Handle,
			PageTitle:         pg.Title,
			PageType:          pt,
			WordCount:         wc,
			FAQQuestionCount:  strings.Count(pg.Body, "?"),
			AboutHasStory:     containsAny(body, storyKeywords),
			AboutHasTeam:      containsAny(body, teamKeywords),
			AIContentEligible: pt == "faq" || pt == "about" || pt == "size_guide",
			NeedsAttention:    wc < 100,
			IsPlaceholder:     false,
		}
		if err := store.UpsertPageAudit(ctx, w.db, pa); err != nil {
			slog.Warn("onboarding audit: upsert page audit failed (non-fatal)", "page_id", pg.ID, "err", err)
		}
	}

	// Create placeholder records for missing essential pages so fix worker can target them
	essentialPages := []struct {
		pageType string
		title    string
	}{
		{"faq", "FAQ"},
		{"about", "About Us"},
		{"size_guide", "Size Guide"},
	}
	pagesNeedingAttention := 0
	for _, ep := range essentialPages {
		if !foundPageTypes[ep.pageType] {
			pagesNeedingAttention++
			pa := &store.PageAudit{
				MerchantID:        merchantID,
				PageTitle:         ep.title,
				PageType:          ep.pageType,
				AIContentEligible: true,
				NeedsAttention:    true,
				IsPlaceholder:     true,
			}
			if err := store.UpsertPageAudit(ctx, w.db, pa); err != nil {
				slog.Warn("onboarding audit: upsert placeholder page failed (non-fatal)", "page_type", ep.pageType, "err", err)
			}
		}
	}

	// ── 7. Persist aggregate audit ─────────────────────────────────────────────
	if err := store.UpsertMerchantAudit(ctx, w.db, audit); err != nil {
		return err
	}

	// ── 8. Persist progress snapshot ──────────────────────────────────────────
	progress := &store.AuditProgress{
		MerchantID:                merchantID,
		TotalProducts:             len(products),
		ProductsNeedingAttention:  productsNeedingAttention,
		TotalCollections:          len(collections),
		CollectionsNeedingAttention: collectionsNeedingAttention,
		TotalPagesAudited:         len(foundPageTypes),
		PagesNeedingAttention:     pagesNeedingAttention,
	}
	if total := len(products) + len(collections) + len(foundPageTypes); total > 0 {
		fixed := total - productsNeedingAttention - collectionsNeedingAttention - pagesNeedingAttention
		if fixed < 0 {
			fixed = 0
		}
		progress.OverallCompletenessScore = float64(fixed) / float64(total) * 100
	}
	if err := store.UpsertAuditProgress(ctx, w.db, progress); err != nil {
		slog.Warn("onboarding audit: upsert progress failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}

	slog.Info("onboarding audit: complete",
		"merchant_id", merchantID,
		"schema_live", audit.SchemaLive,
		"avg_desc_words", audit.AvgDescriptionWords,
		"no_desc", audit.ProductsWithNoDescription,
		"short_desc", audit.ProductsWithShortDescription,
		"has_faq_page", audit.HasFAQPage,
		"review_app", audit.ReviewApp,
		"products_audited", len(products),
		"collections_audited", len(collections),
		"pages_found", len(foundPageTypes),
		"pages_missing", pagesNeedingAttention,
	)

	// Queue fix generation so the merchant sees actionable fixes immediately after
	// install — without needing to trigger a full AI scan first.
	// The fix worker will retry automatically if product sync is still in progress.
	if _, err := w.riverClient.Insert(ctx, FixGenerationJobArgs{MerchantID: merchantID}, nil); err != nil {
		slog.Warn("onboarding audit: failed to queue fix generation (non-fatal)",
			"merchant_id", merchantID, "err", err)
	} else {
		slog.Info("onboarding audit: fix generation queued", "merchant_id", merchantID)
	}

	return nil
}

// ─── Detection helpers ────────────────────────────────────────────────────────

var materialKeywords = []string{
	"cotton", "polyester", "wool", "linen", "silk", "leather", "nylon", "spandex",
	"fabric", "material", "blend", "woven", "knit", "suede", "canvas", "denim",
}

var sizingKeywords = []string{
	"size", "fit", "measurement", "inches", "cm", "centimeter",
	"small", "medium", "large", "xl", "xxl", "waist", "chest", "length",
	"model wears", "true to size", "runs small", "runs large",
}

var careKeywords = []string{
	"wash", "dry", "clean", "care", "iron", "machine wash", "hand wash",
	"tumble dry", "dry clean", "bleach", "laundry",
}

var storyKeywords = []string{
	"founded", "started", "began", "story", "mission", "believe", "passion",
	"family", "journey", "created", "established",
}

var teamKeywords = []string{
	"team", "founder", "founders", "staff", "crew", "people", "meet",
}

// containsAny returns true if text contains at least one of the keywords (case-insensitive).
func containsAny(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// classifyPage returns a page type based on title/handle keywords.
func classifyPage(title, handle string) string {
	s := strings.ToLower(title + " " + handle)
	switch {
	case strings.Contains(s, "faq") || strings.Contains(s, "frequently"):
		return "faq"
	case strings.Contains(s, "about") || strings.Contains(s, "our story") || strings.Contains(s, "who we are"):
		return "about"
	case strings.Contains(s, "size") || strings.Contains(s, "sizing") || strings.Contains(s, "fit guide"):
		return "size_guide"
	case strings.Contains(s, "shipping") || strings.Contains(s, "delivery"):
		return "shipping"
	case strings.Contains(s, "return") || strings.Contains(s, "refund") || strings.Contains(s, "exchange"):
		return "returns"
	case strings.Contains(s, "contact") || strings.Contains(s, "get in touch"):
		return "contact"
	}
	return "other"
}

// descCompletenessScore returns a 0.0–1.0 score based on word count and element presence.
func descCompletenessScore(plainText string) float64 {
	wc := len(strings.Fields(plainText))
	var score float64

	// Word count component (0–0.4)
	switch {
	case wc >= 150:
		score += 0.4
	case wc >= 80:
		score += 0.3
	case wc >= 50:
		score += 0.2
	case wc > 0:
		score += 0.1
	}

	// Element components (0.2 each)
	if containsAny(plainText, materialKeywords) {
		score += 0.2
	}
	if containsAny(plainText, sizingKeywords) {
		score += 0.2
	}
	if containsAny(plainText, careKeywords) {
		score += 0.2
	}

	return score
}

// stripHTML removes HTML tags from a string for accurate word counting.
func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return b.String()
}
