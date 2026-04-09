package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MerchantAudit holds the results of the onboarding store audit.
// Populated once at install and re-run on demand via the admin endpoint.
type MerchantAudit struct {
	ID                           int64
	MerchantID                   int64
	SchemaLive                   bool   // our geo_visibility/schema_json metafield is live
	AvgDescriptionWords          int    // average word count across top products
	ProductsWithNoDescription    int    // products with 0-word descriptions
	ProductsWithShortDescription int    // products with < 50-word descriptions
	HasFAQPage                   bool   // merchant has a Shopify page titled/handled "faq"
	ReviewApp                    string // detected review app slug, or "" if none
	GoogleMerchantCenterConnected bool   // Google & YouTube app installed → Merchant Center linked
	GoogleProductFeedActive       bool   // product feed is actively syncing to Google
	AuditedAt                    time.Time
}

// GetMerchantAudit returns the most recent audit for a merchant.
// Returns nil (not an error) if no audit row exists yet.
func GetMerchantAudit(ctx context.Context, db *pgxpool.Pool, merchantID int64) (*MerchantAudit, error) {
	var a MerchantAudit
	err := db.QueryRow(ctx, `
		SELECT id, merchant_id, schema_live, avg_description_words,
		       products_with_no_description, products_with_short_description,
		       has_faq_page, review_app,
		       COALESCE(google_merchant_center_connected, FALSE),
		       COALESCE(google_product_feed_active, FALSE),
		       audited_at
		FROM merchant_audit WHERE merchant_id = $1
	`, merchantID).Scan(
		&a.ID, &a.MerchantID, &a.SchemaLive, &a.AvgDescriptionWords,
		&a.ProductsWithNoDescription, &a.ProductsWithShortDescription,
		&a.HasFAQPage, &a.ReviewApp,
		&a.GoogleMerchantCenterConnected, &a.GoogleProductFeedActive,
		&a.AuditedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store.GetMerchantAudit: %w", err)
	}
	return &a, nil
}

// UpsertMerchantAudit inserts or updates the audit row for a merchant.
func UpsertMerchantAudit(ctx context.Context, db *pgxpool.Pool, a *MerchantAudit) error {
	_, err := db.Exec(ctx, `
		INSERT INTO merchant_audit
			(merchant_id, schema_live, avg_description_words,
			 products_with_no_description, products_with_short_description,
			 has_faq_page, review_app,
			 google_merchant_center_connected, google_product_feed_active,
			 audited_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (merchant_id) DO UPDATE SET
			schema_live                        = EXCLUDED.schema_live,
			avg_description_words              = EXCLUDED.avg_description_words,
			products_with_no_description       = EXCLUDED.products_with_no_description,
			products_with_short_description    = EXCLUDED.products_with_short_description,
			has_faq_page                       = EXCLUDED.has_faq_page,
			review_app                         = EXCLUDED.review_app,
			google_merchant_center_connected   = EXCLUDED.google_merchant_center_connected,
			google_product_feed_active         = EXCLUDED.google_product_feed_active,
			audited_at                         = now()
	`, a.MerchantID, a.SchemaLive, a.AvgDescriptionWords,
		a.ProductsWithNoDescription, a.ProductsWithShortDescription,
		a.HasFAQPage, a.ReviewApp,
		a.GoogleMerchantCenterConnected, a.GoogleProductFeedActive,
	)
	if err != nil {
		return fmt.Errorf("store.UpsertMerchantAudit: %w", err)
	}
	return nil
}
