package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorityScore holds two independent signals for how trusted a brand is.
// They are intentionally kept separate — grounded_rate is AI-derived data,
// listing completeness is a merchant action. Mixing them into one number
// is misleading, so we expose both and let the UI show them distinctly.
type AuthorityScore struct {
	// GroundedRate: % of web-grounded AI queries (last 30 days) where the brand was mentioned.
	// This is the primary authority signal — it reflects real external citations AI found.
	GroundedRate    int    `json:"grounded_rate"`
	GroundedQueries int    `json:"grounded_queries"` // total grounded queries in last 30 days
	GroundedHits    int    `json:"grounded_hits"`    // how many of those mentioned the brand
	// Tier is derived solely from GroundedRate — the real-data signal.
	Tier string `json:"tier"` // none | low | building | established

	// ListingsDone / ListingsTotal: how many authority-layer fixes the merchant has applied.
	// Shown separately — reflects merchant effort, not AI recognition.
	ListingsDone  int `json:"listings_done"`
	ListingsTotal int `json:"listings_total"`
}

// GetAuthorityScore computes both authority signals for a merchant.
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

	// Tier is derived from the real AI-data signal only.
	tier := "none"
	switch {
	case groundedRate >= 61:
		tier = "established"
	case groundedRate >= 26:
		tier = "building"
	case groundedRate >= 1:
		tier = "low"
	}

	return AuthorityScore{
		GroundedRate:    groundedRate,
		GroundedQueries: groundedTotal,
		GroundedHits:    groundedHits,
		Tier:            tier,
		ListingsDone:    done,
		ListingsTotal:   total,
	}, nil
}
