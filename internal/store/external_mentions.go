package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExternalMention represents a tracked external mention of a merchant's brand.
type ExternalMention struct {
	ID             int64    `json:"id"`
	MerchantID     int64    `json:"merchant_id"`
	URL            string   `json:"url"`
	SourceName     string   `json:"source_name"`
	SourceDomain   *string  `json:"source_domain"`
	SourceType     string   `json:"source_type"`
	Title          *string  `json:"title"`
	Snippet        *string  `json:"snippet"`
	MentionContext *string  `json:"mention_context"`
	AuthorityScore *float64 `json:"authority_score"`
	Sentiment      *string  `json:"sentiment"`
	PublishedDate  *string  `json:"published_date"`
	DiscoveredDate string   `json:"discovered_date"`
	Verified       bool     `json:"verified"`
	VerifiedBy     *int64   `json:"verified_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ExternalMentionStats summarises mentions for a merchant.
type ExternalMentionStats struct {
	Total            int     `json:"total"`
	Verified         int     `json:"verified"`
	AvgAuthorityScore float64 `json:"avg_authority_score"`
	EditorialCount   int     `json:"editorial_count"`
	PressCount       int     `json:"press_count"`
	ReviewCount      int     `json:"review_count"`
	SocialCount      int     `json:"social_count"`
	InfluencerCount  int     `json:"influencer_count"`
	OtherCount       int     `json:"other_count"`
}

// SourceAuthorityPresets maps well-known domains to a baseline authority score (0–1).
var SourceAuthorityPresets = map[string]float64{
	"techcrunch.com":    0.95,
	"forbes.com":        0.93,
	"businessinsider.com": 0.88,
	"wired.com":         0.90,
	"theverge.com":      0.88,
	"cnet.com":          0.85,
	"mashable.com":      0.80,
	"entrepreneur.com":  0.82,
	"inc.com":           0.82,
	"wsj.com":           0.96,
	"nytimes.com":       0.96,
	"producthunt.com":   0.75,
	"reddit.com":        0.60,
	"trustpilot.com":    0.70,
	"g2.com":            0.72,
	"capterra.com":      0.72,
	"yelp.com":          0.65,
	"instagram.com":     0.50,
	"twitter.com":       0.50,
	"x.com":             0.50,
	"tiktok.com":        0.48,
	"youtube.com":       0.55,
	"medium.com":        0.60,
	"substack.com":      0.58,
}

// CalculateAuthorityScore returns the preset score for a domain, or 0.40 for unknown domains.
// The domain should be lowercase and stripped of any "www." prefix.
func CalculateAuthorityScore(domain string) float64 {
	domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
	if score, ok := SourceAuthorityPresets[domain]; ok {
		return score
	}
	return 0.40
}

// InsertExternalMention inserts a new mention and fills in id, created_at, updated_at.
func InsertExternalMention(ctx context.Context, db *pgxpool.Pool, m *ExternalMention) error {
	const q = `
INSERT INTO external_mentions (
    merchant_id, url, source_name, source_domain, source_type,
    title, snippet, mention_context, authority_score, sentiment,
    published_date, discovered_date, verified, verified_by
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14
)
RETURNING id, created_at, updated_at`

	return db.QueryRow(ctx, q,
		m.MerchantID, m.URL, m.SourceName, m.SourceDomain, m.SourceType,
		m.Title, m.Snippet, m.MentionContext, m.AuthorityScore, m.Sentiment,
		m.PublishedDate, m.DiscoveredDate, m.Verified, m.VerifiedBy,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
}

// GetExternalMentions returns mentions for a merchant, ordered by authority then recency.
func GetExternalMentions(ctx context.Context, db *pgxpool.Pool, merchantID int64, limit int) ([]ExternalMention, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
SELECT id, merchant_id, url, source_name, source_domain, source_type,
       title, snippet, mention_context, authority_score, sentiment,
       published_date::text, discovered_date::text, verified, verified_by,
       created_at, updated_at
FROM external_mentions
WHERE merchant_id = $1
ORDER BY authority_score DESC NULLS LAST, published_date DESC NULLS LAST
LIMIT $2`

	rows, err := db.Query(ctx, q, merchantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExternalMention
	for rows.Next() {
		var m ExternalMention
		if err := rows.Scan(
			&m.ID, &m.MerchantID, &m.URL, &m.SourceName, &m.SourceDomain, &m.SourceType,
			&m.Title, &m.Snippet, &m.MentionContext, &m.AuthorityScore, &m.Sentiment,
			&m.PublishedDate, &m.DiscoveredDate, &m.Verified, &m.VerifiedBy,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetExternalMentionStats returns aggregate stats for a merchant's mentions.
func GetExternalMentionStats(ctx context.Context, db *pgxpool.Pool, merchantID int64) (ExternalMentionStats, error) {
	const q = `
SELECT
    COUNT(*)                                                          AS total,
    COUNT(*) FILTER (WHERE verified)                                  AS verified,
    COALESCE(AVG(authority_score), 0)                                 AS avg_authority_score,
    COUNT(*) FILTER (WHERE source_type = 'editorial')                 AS editorial_count,
    COUNT(*) FILTER (WHERE source_type = 'press')                     AS press_count,
    COUNT(*) FILTER (WHERE source_type = 'review_platform')           AS review_count,
    COUNT(*) FILTER (WHERE source_type = 'social')                    AS social_count,
    COUNT(*) FILTER (WHERE source_type = 'influencer')                AS influencer_count,
    COUNT(*) FILTER (WHERE source_type = 'other')                     AS other_count
FROM external_mentions
WHERE merchant_id = $1`

	var s ExternalMentionStats
	err := db.QueryRow(ctx, q, merchantID).Scan(
		&s.Total, &s.Verified, &s.AvgAuthorityScore,
		&s.EditorialCount, &s.PressCount, &s.ReviewCount,
		&s.SocialCount, &s.InfluencerCount, &s.OtherCount,
	)
	return s, err
}

// VerifyExternalMention marks a mention as verified by an admin user.
func VerifyExternalMention(ctx context.Context, db *pgxpool.Pool, mentionID, adminID int64) error {
	_, err := db.Exec(ctx, `
UPDATE external_mentions
SET verified = TRUE, verified_by = $2, updated_at = NOW()
WHERE id = $1`, mentionID, adminID)
	return err
}
