package jobs

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// OnboardingAuditWorker reads the merchant's live Shopify store state and
// persists results to merchant_audit. FixGenerationWorker reads this table
// to skip recommendations for things the merchant already has in place.
type OnboardingAuditWorker struct {
	river.WorkerDefaults[OnboardingAuditJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
}

func NewOnboardingAuditWorker(db *pgxpool.Pool, encKey []byte) *OnboardingAuditWorker {
	return &OnboardingAuditWorker{db: db, encryptionKey: encKey}
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
	// Check whether our geo_visibility/schema_json metafield already exists.
	schemaVal, err := shopify.GetShopMetafieldValue(ctx, merchant.ShopDomain, token, "geo_visibility", "schema_json")
	if err != nil {
		slog.Warn("onboarding audit: schema check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	}
	audit.SchemaLive = schemaVal != ""

	// ── 2. Description quality ─────────────────────────────────────────────────
	// Fetch top 10 products and measure description word counts.
	products, err := shopify.GetTopProducts(ctx, merchant.ShopDomain, token, 10, "")
	if err != nil {
		slog.Warn("onboarding audit: product fetch failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else if len(products) > 0 {
		totalWords := 0
		for _, p := range products {
			wc := len(strings.Fields(stripHTML(p.Description)))
			totalWords += wc
			if wc == 0 {
				audit.ProductsWithNoDescription++
			} else if wc < 50 {
				audit.ProductsWithShortDescription++
			}
		}
		audit.AvgDescriptionWords = totalWords / len(products)
	}

	// ── 3. Review app ──────────────────────────────────────────────────────────
	themeResult, err := shopify.DetectReviewAppFromTheme(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: review app detection failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else if themeResult != nil {
		audit.ReviewApp = themeResult.App
	}

	// ── 4. FAQ page ────────────────────────────────────────────────────────────
	hasFAQ, err := shopify.HasFAQPage(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("onboarding audit: FAQ page check failed (non-fatal)", "merchant_id", merchantID, "err", err)
	} else {
		audit.HasFAQPage = hasFAQ
	}

	// ── 5. Persist ─────────────────────────────────────────────────────────────
	if err := store.UpsertMerchantAudit(ctx, w.db, audit); err != nil {
		return err
	}

	slog.Info("onboarding audit: complete",
		"merchant_id", merchantID,
		"schema_live", audit.SchemaLive,
		"avg_desc_words", audit.AvgDescriptionWords,
		"no_desc", audit.ProductsWithNoDescription,
		"short_desc", audit.ProductsWithShortDescription,
		"has_faq_page", audit.HasFAQPage,
		"review_app", audit.ReviewApp,
	)
	return nil
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
