package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VerificationRecord is a row from citation_verifications.
type VerificationRecord struct {
	ID                   int64             `json:"id"`
	CitationRecordID     int64             `json:"citation_record_id"`
	MerchantID           int64             `json:"merchant_id"`
	VerifiedAt           time.Time         `json:"verified_at"`
	OriginalQuery        string            `json:"original_query"`
	OriginalPlatform     string            `json:"original_platform"`
	OriginalResponse     string            `json:"original_response"`
	ReQueryResponse      string            `json:"re_query_response"`
	SimilarityScore      *float64          `json:"similarity_score"`
	ResponseChanged      bool              `json:"response_changed"`
	HallucinationFlags   json.RawMessage   `json:"hallucination_flags"`
	HallucinationCount   int               `json:"hallucination_count"`
	CrossPlatformResults json.RawMessage   `json:"cross_platform_results"`
	ConsistencyScore     *float64          `json:"consistency_score"`
	IsAuthentic          bool              `json:"is_authentic"`
	VerificationNotes    string            `json:"verification_notes"`
}

// StabilityRecord is a row from response_stability.
type StabilityRecord struct {
	ID             int64    `json:"id"`
	MerchantID     int64    `json:"merchant_id"`
	QueryText      string   `json:"query_text"`
	Platform       string   `json:"platform"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastCheckedAt  time.Time `json:"last_checked_at"`
	CheckCount     int      `json:"check_count"`
	AvgSimilarity  float64  `json:"avg_similarity"`
	MinSimilarity  float64  `json:"min_similarity"`
	DriftDetected  bool     `json:"drift_detected"`
}

// GetVerifications returns recent verification records for a merchant.
// If merchantID == 0, returns all (admin overview). Capped at limit.
func GetVerifications(ctx context.Context, db *pgxpool.Pool, merchantID int64, limit int) ([]VerificationRecord, error) {
	q := `
		SELECT id, citation_record_id, merchant_id, verified_at,
		       original_query, original_platform, original_response,
		       re_query_response, similarity_score, response_changed,
		       hallucination_flags, hallucination_count,
		       cross_platform_results, consistency_score,
		       is_authentic, verification_notes
		FROM citation_verifications`

	var args []any
	if merchantID != 0 {
		q += ` WHERE merchant_id = $1 ORDER BY verified_at DESC LIMIT $2`
		args = []any{merchantID, limit}
	} else {
		q += ` ORDER BY verified_at DESC LIMIT $1`
		args = []any{limit}
	}

	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VerificationRecord
	for rows.Next() {
		var r VerificationRecord
		if err := rows.Scan(
			&r.ID, &r.CitationRecordID, &r.MerchantID, &r.VerifiedAt,
			&r.OriginalQuery, &r.OriginalPlatform, &r.OriginalResponse,
			&r.ReQueryResponse, &r.SimilarityScore, &r.ResponseChanged,
			&r.HallucinationFlags, &r.HallucinationCount,
			&r.CrossPlatformResults, &r.ConsistencyScore,
			&r.IsAuthentic, &r.VerificationNotes,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetStabilityRecords returns response_stability rows for a merchant, ordered by drift first.
func GetStabilityRecords(ctx context.Context, db *pgxpool.Pool, merchantID int64, limit int) ([]StabilityRecord, error) {
	q := `
		SELECT id, merchant_id, query_text, platform,
		       first_seen_at, last_checked_at, check_count,
		       avg_similarity, min_similarity, drift_detected
		FROM response_stability`

	var args []any
	if merchantID != 0 {
		q += ` WHERE merchant_id = $1
		       ORDER BY drift_detected DESC, avg_similarity ASC
		       LIMIT $2`
		args = []any{merchantID, limit}
	} else {
		q += ` ORDER BY drift_detected DESC, avg_similarity ASC LIMIT $1`
		args = []any{limit}
	}

	rows, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StabilityRecord
	for rows.Next() {
		var r StabilityRecord
		if err := rows.Scan(
			&r.ID, &r.MerchantID, &r.QueryText, &r.Platform,
			&r.FirstSeenAt, &r.LastCheckedAt, &r.CheckCount,
			&r.AvgSimilarity, &r.MinSimilarity, &r.DriftDetected,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
