package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MerchantReviewStatus is the review state stored on a merchant row.
type MerchantReviewStatus struct {
	MerchantID           int64      `json:"merchant_id"`
	ShopDomain           string     `json:"shop_domain"`
	BrandName            string     `json:"brand_name"`
	ReviewApp            *string    `json:"review_app"`
	AvgRating            *float64   `json:"avg_rating"`
	TotalReviews         int        `json:"total_reviews"`
	ReviewSchemaInjected bool       `json:"review_schema_injected"`
	ReviewsLastScannedAt *time.Time `json:"reviews_last_scanned_at"`
}

// HasReviews returns true when the merchant has a detected review app with actual reviews.
func (s *MerchantReviewStatus) HasReviews() bool {
	return s.TotalReviews > 0 && s.ReviewApp != nil && *s.ReviewApp != "none"
}

// SafeAvgRating returns the avg rating as a float64, or 0 if nil.
func (s *MerchantReviewStatus) SafeAvgRating() float64 {
	if s.AvgRating == nil {
		return 0
	}
	return *s.AvgRating
}

// ReviewAppLabel returns a human-readable app name.
func (s *MerchantReviewStatus) ReviewAppLabel() string {
	if s.ReviewApp == nil {
		return "Unknown"
	}
	return *s.ReviewApp
}

// SaveMerchantReviews upserts review fields on the merchants row.
// Pass app="" and rating=0 and count=0 to record a "no reviews found" scan.
// appKey is the extracted public API key for the review app (may be empty).
func SaveMerchantReviews(
	ctx context.Context,
	db *pgxpool.Pool,
	merchantID int64,
	app string,
	appKey string,
	avgRating float64,
	totalReviews int,
	schemaInjected bool,
) error {
	var appVal *string
	if app != "" && app != "none" {
		appVal = &app
	}
	var appKeyVal *string
	if appKey != "" {
		appKeyVal = &appKey
	}
	var ratingVal *float64
	if avgRating > 0 {
		ratingVal = &avgRating
	}

	_, err := db.Exec(ctx, `
		UPDATE merchants SET
			review_app              = $1,
			review_app_key          = $2,
			avg_rating              = $3,
			total_reviews           = $4,
			review_schema_injected  = $5,
			reviews_last_scanned_at = NOW(),
			updated_at              = NOW()
		WHERE id = $6
	`, appVal, appKeyVal, ratingVal, totalReviews, schemaInjected, merchantID)
	return err
}

// GetMerchantReviewStatus returns the review columns for a single merchant.
func GetMerchantReviewStatus(ctx context.Context, db *pgxpool.Pool, merchantID int64) (*MerchantReviewStatus, error) {
	var s MerchantReviewStatus
	err := db.QueryRow(ctx, `
		SELECT id, shop_domain, brand_name,
		       review_app, avg_rating, total_reviews,
		       review_schema_injected, reviews_last_scanned_at
		FROM merchants WHERE id = $1
	`, merchantID).Scan(
		&s.MerchantID, &s.ShopDomain, &s.BrandName,
		&s.ReviewApp, &s.AvgRating, &s.TotalReviews,
		&s.ReviewSchemaInjected, &s.ReviewsLastScannedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetAllMerchantReviewStatuses returns review status for all active merchants,
// ordered by: un-scanned first, then has-reviews, then no-reviews.
func GetAllMerchantReviewStatuses(ctx context.Context, db *pgxpool.Pool) ([]MerchantReviewStatus, error) {
	rows, err := db.Query(ctx, `
		SELECT id, shop_domain, brand_name,
		       review_app, avg_rating, total_reviews,
		       review_schema_injected, reviews_last_scanned_at
		FROM merchants
		WHERE active = true
		ORDER BY
		    reviews_last_scanned_at IS NULL DESC,
		    total_reviews DESC,
		    id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MerchantReviewStatus
	for rows.Next() {
		var s MerchantReviewStatus
		if err := rows.Scan(
			&s.MerchantID, &s.ShopDomain, &s.BrandName,
			&s.ReviewApp, &s.AvgRating, &s.TotalReviews,
			&s.ReviewSchemaInjected, &s.ReviewsLastScannedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
