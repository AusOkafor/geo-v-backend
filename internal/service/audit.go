package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/fix"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// AuditService orchestrates store audits — reading live Shopify state and
// persisting results to the audit tables.
type AuditService struct {
	db            *pgxpool.Pool
	encryptionKey []byte
}

func NewAuditService(db *pgxpool.Pool, encKey []byte) *AuditService {
	return &AuditService{db: db, encryptionKey: encKey}
}

// RunFullAudit runs the complete store audit for a merchant and persists results.
// It reads live Shopify data, evaluates content quality, and writes to:
// merchant_audit, merchant_product_audit, merchant_collection_audit,
// merchant_page_audit, and merchant_audit_progress.
func (s *AuditService) RunFullAudit(ctx context.Context, merchantID int64) error {
	merchant, err := store.GetMerchant(ctx, s.db, merchantID)
	if err != nil {
		return err
	}
	if !merchant.Active {
		return nil
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, s.encryptionKey)
	if err != nil {
		return err
	}

	audit := &store.MerchantAudit{MerchantID: merchantID}

	// ── 1. Schema live + quality ───────────────────────────────────────────────
	schemaVal, err := shopify.GetShopMetafieldValue(ctx, merchant.ShopDomain, token, "geo_visibility", "schema_json")
	if err != nil {
		slog.Warn("audit: schema check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}
	audit.SchemaLive = schemaVal != ""
	if audit.SchemaLive {
		v := fix.ValidateSchema(schemaVal)
		audit.SchemaCompletenessScore = v.CompletenessScore
	}

	// ── 2. Products ────────────────────────────────────────────────────────────
	products, err := shopify.FetchAllProducts(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("audit: product fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}

	var totalWords int
	productsNeedingAttention := 0
	for _, p := range products {
		plainText := StripHTML(p.DescriptionHTML)
		wc := len(strings.Fields(plainText))
		totalWords += wc

		if wc == 0 {
			audit.ProductsWithNoDescription++
		} else if wc < 50 {
			audit.ProductsWithShortDescription++
		}

		needsAttention := wc < 50 ||
			!ContainsAny(plainText, MaterialKeywords) ||
			!ContainsAny(plainText, SizingKeywords) ||
			!ContainsAny(plainText, CareKeywords)

		score := DescCompletenessScore(plainText)
		if needsAttention {
			productsNeedingAttention++
		}

		pa := &store.ProductAudit{
			MerchantID:              merchantID,
			ProductID:               p.ID,
			ProductTitle:            p.Title,
			CurrentDescriptionWords: wc,
			MissingMaterialInfo:     !ContainsAny(plainText, MaterialKeywords),
			MissingSizingInfo:       !ContainsAny(plainText, SizingKeywords),
			MissingCareInstructions: !ContainsAny(plainText, CareKeywords),
			CompletenessScore:       score,
			NeedsAttention:          needsAttention,
		}
		if err := store.UpsertProductAudit(ctx, s.db, pa); err != nil {
			slog.Warn("audit: upsert product audit failed (non-fatal)", "product_id", p.ID, "err", err)
		}
	}

	if len(products) > 0 {
		audit.AvgDescriptionWords = totalWords / len(products)
	}

	// ── 3. Review app ──────────────────────────────────────────────────────────
	themeResult, err := shopify.DetectReviewAppFromTheme(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("audit: review app detection failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else if themeResult != nil {
		audit.ReviewApp = themeResult.App
	}

	// ── 4. FAQ page ────────────────────────────────────────────────────────────
	hasFAQ, err := shopify.HasFAQPage(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("audit: FAQ page check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else {
		audit.HasFAQPage = hasFAQ
	}

	// ── 5. Collections ─────────────────────────────────────────────────────────
	collections, err := shopify.FetchAllCollections(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("audit: collection fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}
	collectionsNeedingAttention := 0
	for _, c := range collections {
		wc := len(strings.Fields(StripHTML(c.Description)))
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
		if err := store.UpsertCollectionAudit(ctx, s.db, ca); err != nil {
			slog.Warn("audit: upsert collection audit failed (non-fatal)", "collection_id", c.ID, "err", err)
		}
	}

	// ── 6. Pages ───────────────────────────────────────────────────────────────
	pages, err := shopify.FetchAllPages(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("audit: page fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}

	foundPageTypes := map[string]bool{}
	pagesNeedingAttention := 0
	for _, pg := range pages {
		pt := ClassifyPage(pg.Title, pg.Handle)
		if pt == "other" {
			continue
		}
		foundPageTypes[pt] = true
		body := StripHTML(pg.Body)
		wc := len(strings.Fields(body))
		needsAttn := wc < 100
		if needsAttn {
			pagesNeedingAttention++
		}

		pa := &store.PageAudit{
			MerchantID:        merchantID,
			PageID:            pg.ID,
			PageHandle:        pg.Handle,
			PageTitle:         pg.Title,
			PageType:          pt,
			WordCount:         wc,
			FAQQuestionCount:  strings.Count(pg.Body, "?"),
			AboutHasStory:     ContainsAny(body, StoryKeywords),
			AboutHasTeam:      ContainsAny(body, TeamKeywords),
			AIContentEligible: pt == "faq" || pt == "about" || pt == "size_guide",
			NeedsAttention:    needsAttn,
			IsPlaceholder:     false,
		}
		if err := store.UpsertPageAudit(ctx, s.db, pa); err != nil {
			slog.Warn("audit: upsert page audit failed (non-fatal)", "page_id", pg.ID, "err", err)
		}
	}

	// Create placeholder records for missing essential pages.
	essentialPages := []struct {
		pageType string
		title    string
	}{
		{"faq", "FAQ"},
		{"about", "About Us"},
		{"size_guide", "Size Guide"},
	}
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
			if err := store.UpsertPageAudit(ctx, s.db, pa); err != nil {
				slog.Warn("audit: upsert placeholder page failed (non-fatal)", "page_type", ep.pageType, "err", err)
			}
		}
	}

	// ── 7. Google Merchant Center ──────────────────────────────────────────────
	mcStatus, err := shopify.CheckMerchantCenterStatus(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("audit: merchant center check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else if mcStatus != nil {
		audit.GoogleMerchantCenterConnected = mcStatus.Connected
		audit.GoogleProductFeedActive = mcStatus.ProductFeedActive
	}

	// ── 8. Persist aggregate audit ─────────────────────────────────────────────
	if err := store.UpsertMerchantAudit(ctx, s.db, audit); err != nil {
		return err
	}

	// ── 9. Persist progress snapshot ──────────────────────────────────────────
	missingEssentialPages := 0
	for _, ep := range essentialPages {
		if !foundPageTypes[ep.pageType] {
			missingEssentialPages++
		}
	}
	totalPagesAudited := len(foundPageTypes) + missingEssentialPages

	progress := &store.AuditProgress{
		MerchantID:                  merchantID,
		TotalProducts:               len(products),
		ProductsNeedingAttention:    productsNeedingAttention,
		TotalCollections:            len(collections),
		CollectionsNeedingAttention: collectionsNeedingAttention,
		TotalPagesAudited:           totalPagesAudited,
		PagesNeedingAttention:       pagesNeedingAttention,
	}
	if total := len(products) + len(collections) + len(foundPageTypes); total > 0 {
		fixed := total - productsNeedingAttention - collectionsNeedingAttention - pagesNeedingAttention
		if fixed < 0 {
			fixed = 0
		}
		progress.OverallCompletenessScore = float64(fixed) / float64(total) * 100
	}
	if err := store.UpsertAuditProgress(ctx, s.db, progress); err != nil {
		slog.Warn("audit: upsert progress failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}

	slog.Info("audit: complete",
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
		"pages_needing_attention", pagesNeedingAttention,
	)

	return nil
}

// GetProgress returns the latest audit progress snapshot.
func (s *AuditService) GetProgress(ctx context.Context, merchantID int64) (*store.AuditProgress, error) {
	return store.GetAuditProgress(ctx, s.db, merchantID)
}

// GetProductsNeedingAttention returns products needing content work.
func (s *AuditService) GetProductsNeedingAttention(ctx context.Context, merchantID int64, limit int) ([]store.ProductAudit, error) {
	return store.GetProductsNeedingAttention(ctx, s.db, merchantID, limit)
}

// GetCollectionsNeedingAttention returns collections eligible for an AI fix.
func (s *AuditService) GetCollectionsNeedingAttention(ctx context.Context, merchantID int64) ([]store.CollectionAudit, error) {
	return store.GetCollectionsEligibleForFix(ctx, s.db, merchantID)
}

// GetPagesNeedingAttention returns pages eligible for an AI fix.
func (s *AuditService) GetPagesNeedingAttention(ctx context.Context, merchantID int64) ([]store.PageAudit, error) {
	return store.GetPagesEligibleForFix(ctx, s.db, merchantID)
}

// ─── Content analysis helpers ─────────────────────────────────────────────────
// Exported so they can be unit-tested in audit_test.go.

var MaterialKeywords = []string{
	"cotton", "polyester", "wool", "linen", "silk", "leather", "nylon", "spandex",
	"fabric", "material", "blend", "woven", "knit", "suede", "canvas", "denim",
}

var SizingKeywords = []string{
	"size", "fit", "measurement", "inches", "cm", "centimeter",
	"small", "medium", "large", "xl", "xxl", "waist", "chest", "length",
	"model wears", "true to size", "runs small", "runs large",
}

var CareKeywords = []string{
	"wash", "dry", "clean", "care", "iron", "machine wash", "hand wash",
	"tumble dry", "dry clean", "bleach", "laundry",
}

var StoryKeywords = []string{
	"founded", "started", "began", "story", "mission", "believe", "passion",
	"family", "journey", "created", "established",
}

var TeamKeywords = []string{
	"team", "founder", "founders", "staff", "crew", "people", "meet",
}

// ContainsAny returns true if text contains at least one keyword (case-insensitive).
func ContainsAny(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ClassifyPage returns a page type based on title/handle keywords.
func ClassifyPage(title, handle string) string {
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

// DescCompletenessScore returns a 0.0–1.0 score based on word count and element presence.
func DescCompletenessScore(plainText string) float64 {
	wc := len(strings.Fields(plainText))
	var score float64

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

	if ContainsAny(plainText, MaterialKeywords) {
		score += 0.2
	}
	if ContainsAny(plainText, SizingKeywords) {
		score += 0.2
	}
	if ContainsAny(plainText, CareKeywords) {
		score += 0.2
	}

	return score
}

// StripHTML removes HTML tags from a string for accurate word counting.
func StripHTML(s string) string {
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
