package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorityScore measures how trusted/cited a brand is by AI from external web sources.
// Score is 0-100, computed from two signals:
//   - Grounded citation rate (60%): % of web-grounded AI queries where the brand was mentioned
//   - Listing completeness (40%): % of authority-layer fixes that have been applied
type AuthorityScore struct {
	Score           int    `json:"score"`
	GroundedRate    int    `json:"grounded_rate"`    // % of grounded queries where mentioned
	GroundedQueries int    `json:"grounded_queries"` // total grounded queries in last 30 days
	ListingsDone    int    `json:"listings_done"`    // applied/manual authority fixes
	ListingsTotal   int    `json:"listings_total"`   // total authority fixes generated
	Tier            string `json:"tier"`             // none | low | building | established
}

// GetAuthorityScore computes the authority score for a merchant.
// Grounded data comes from citation_records in the last 30 days.
// Listing data comes from pending_fixes where fix_layer = 'authority'.
func GetAuthorityScore(ctx context.Context, db *pgxpool.Pool, merchantID int64) (AuthorityScore, error) {
	const q = `
WITH grounded AS (
  SELECT
    COUNT(*) FILTER (WHERE mentioned AND grounded) AS grounded_hits,
    COUNT(*) FILTER (WHERE grounded)               AS grounded_total
  FROM citation_records
  WHERE merchant_id = $1
    AND scanned_at >= CURRENT_DATE - INTERVAL '30 days'
),
listings AS (
  SELECT
    COUNT(*) FILTER (WHERE status IN ('applied', 'manual')) AS done,
    COUNT(*) FILTER (WHERE status != 'rejected')            AS total
  FROM pending_fixes
  WHERE merchant_id = $1
    AND fix_layer = 'authority'
)
SELECT
  grounded_hits::int,
  grounded_total::int,
  done::int,
  total::int
FROM grounded, listings`

	var groundedHits, groundedTotal, done, total int
	if err := db.QueryRow(ctx, q, merchantID).Scan(
		&groundedHits, &groundedTotal, &done, &total,
	); err != nil {
		return AuthorityScore{}, err
	}

	var groundedRate int
	if groundedTotal > 0 {
		groundedRate = int(float64(groundedHits) / float64(groundedTotal) * 100)
	}

	var listingRate int
	if total > 0 {
		listingRate = int(float64(done) / float64(total) * 100)
	}

	score := int(float64(groundedRate)*0.6 + float64(listingRate)*0.4)

	tier := "none"
	switch {
	case score >= 61:
		tier = "established"
	case score >= 26:
		tier = "building"
	case score >= 1:
		tier = "low"
	}

	return AuthorityScore{
		Score:           score,
		GroundedRate:    groundedRate,
		GroundedQueries: groundedTotal,
		ListingsDone:    done,
		ListingsTotal:   total,
		Tier:            tier,
	}, nil
}
