package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/austinokafor/geo-backend/internal/scoring"
)

// computeIntegrity re-hashes text and returns (storedHash, integrityValid).
// storedHash comes from the citation_records row; text is the ai_response on the spot check.
func computeIntegrity(storedHash *string, text string) (string, bool) {
	if storedHash == nil || *storedHash == "" {
		return "", false
	}
	h := sha256.Sum256([]byte(text))
	computed := hex.EncodeToString(h[:])
	return *storedHash, computed == *storedHash
}

// SpotCheck is a manual verification record for a single citation_record.
type SpotCheck struct {
	ID                int64      `json:"id"`
	CitationRecordID  int64      `json:"citation_record_id"`
	MerchantID        int64      `json:"merchant_id"`
	QueryText         string     `json:"query_text"`
	Platform          string     `json:"platform"`
	AIResponse        string     `json:"ai_response"`
	ManualBrands      []string   `json:"manual_brands"`
	DetectedBrands    []string   `json:"detected_brands"`
	Precision         *float64   `json:"precision"`
	Recall            *float64   `json:"recall"`
	F1Score           *float64   `json:"f1_score"`
	TruePositives     int        `json:"true_positives"`
	FalsePositives    int        `json:"false_positives"`
	FalseNegatives    int        `json:"false_negatives"`
	Status            string     `json:"status"`
	VerifiedByType    string     `json:"verified_by_type"`
	VerifiedByEmail   *string    `json:"verified_by_email"`
	VerifiedAt        *time.Time `json:"verified_at"`
	CreatedAt         time.Time  `json:"created_at"`
	// Integrity fields — sourced from the originating citation_record.
	// ResponseHash is the SHA256 of AIResponse at capture time. The admin UI
	// recomputes it and sets IntegrityValid = (hash == ResponseHash).
	ResponseHash  string  `json:"response_hash"`
	ModelVersion  string  `json:"model_version"`
	IntegrityValid bool   `json:"integrity_valid"`
}

// AccuracyMetric is a daily rolled-up accuracy record per merchant+platform.
type AccuracyMetric struct {
	Date        string  `json:"date"`
	Platform    string  `json:"platform"`
	AvgPrecision float64 `json:"avg_precision"`
	AvgRecall    float64 `json:"avg_recall"`
	AvgF1        float64 `json:"avg_f1"`
	SampleSize  int     `json:"sample_size"`
}

// CreateSpotCheck initialises a pending spot check from an existing citation_record.
// Reads query, platform, answer_text, and competitors from the record.
// Returns error if the citation_record doesn't belong to merchantID or has no answer_text.
func CreateSpotCheck(ctx context.Context, db *pgxpool.Pool, merchantID, citationRecordID int64) (*SpotCheck, error) {
	var (
		query        string
		platform     string
		answerText   *string
		competJSON   []byte
		responseHash *string
		modelVersion *string
	)
	err := db.QueryRow(ctx, `
		SELECT query, platform, answer_text, competitors, response_hash, model_version
		FROM citation_records
		WHERE id = $1 AND merchant_id = $2
	`, citationRecordID, merchantID).Scan(&query, &platform, &answerText, &competJSON, &responseHash, &modelVersion)
	if err != nil {
		return nil, fmt.Errorf("store: CreateSpotCheck: citation record not found: %w", err)
	}

	aiResponse := ""
	if answerText != nil {
		aiResponse = *answerText
	}

	// Extract competitor names from stored JSONB as the "detected brands"
	var competitors []struct {
		Name string `json:"name"`
	}
	if len(competJSON) > 0 {
		_ = json.Unmarshal(competJSON, &competitors)
	}
	detected := make([]string, 0, len(competitors))
	for _, c := range competitors {
		if c.Name != "" {
			detected = append(detected, c.Name)
		}
	}

	var sc SpotCheck
	err = db.QueryRow(ctx, `
		INSERT INTO spot_checks (
			citation_record_id, merchant_id, query_text, platform,
			ai_response, detected_brands, status, verified_by_type
		) VALUES ($1,$2,$3,$4,$5,$6,'pending','team')
		ON CONFLICT (citation_record_id) DO UPDATE
			SET created_at = spot_checks.created_at
		RETURNING id, citation_record_id, merchant_id, query_text, platform,
			ai_response, manual_brands, detected_brands,
			precision_score, recall_score, f1_score,
			true_positives, false_positives, false_negatives,
			status, verified_by_type, verified_by_email, verified_at, created_at
	`, citationRecordID, merchantID, query, platform, aiResponse, detected).
		Scan(
			&sc.ID, &sc.CitationRecordID, &sc.MerchantID,
			&sc.QueryText, &sc.Platform, &sc.AIResponse,
			&sc.ManualBrands, &sc.DetectedBrands,
			&sc.Precision, &sc.Recall, &sc.F1Score,
			&sc.TruePositives, &sc.FalsePositives, &sc.FalseNegatives,
			&sc.Status, &sc.VerifiedByType, &sc.VerifiedByEmail, &sc.VerifiedAt, &sc.CreatedAt,
		)
	if err != nil {
		return nil, fmt.Errorf("store: CreateSpotCheck: insert: %w", err)
	}

	sc.ResponseHash, sc.IntegrityValid = computeIntegrity(responseHash, sc.AIResponse)
	if modelVersion != nil {
		sc.ModelVersion = *modelVersion
	}
	return &sc, nil
}

// VerifySpotCheck records the human-provided manual brand list, computes metrics,
// and marks the spot check as verified.
func VerifySpotCheck(ctx context.Context, db *pgxpool.Pool, id int64, manualBrands []string, verifiedByType, verifiedByEmail string) (*SpotCheck, error) {
	// Fetch detected brands to compute metrics
	var detectedBrands []string
	err := db.QueryRow(ctx, `SELECT detected_brands FROM spot_checks WHERE id=$1`, id).
		Scan(&detectedBrands)
	if err != nil {
		return nil, fmt.Errorf("store: VerifySpotCheck: fetch: %w", err)
	}

	m := scoring.Calculate(manualBrands, detectedBrands)
	now := time.Now()

	var sc SpotCheck
	err = db.QueryRow(ctx, `
		UPDATE spot_checks SET
			manual_brands      = $1,
			precision_score    = $2,
			recall_score       = $3,
			f1_score           = $4,
			true_positives     = $5,
			false_positives    = $6,
			false_negatives    = $7,
			status             = 'verified',
			verified_by_type   = $8,
			verified_by_email  = $9,
			verified_at        = $10
		WHERE id = $11
		RETURNING id, citation_record_id, merchant_id, query_text, platform,
			ai_response, manual_brands, detected_brands,
			precision_score, recall_score, f1_score,
			true_positives, false_positives, false_negatives,
			status, verified_by_type, verified_by_email, verified_at, created_at
	`, manualBrands, m.Precision, m.Recall, m.F1,
		m.TruePositives, m.FalsePositives, m.FalseNegatives,
		verifiedByType, verifiedByEmail, now, id).
		Scan(
			&sc.ID, &sc.CitationRecordID, &sc.MerchantID,
			&sc.QueryText, &sc.Platform, &sc.AIResponse,
			&sc.ManualBrands, &sc.DetectedBrands,
			&sc.Precision, &sc.Recall, &sc.F1Score,
			&sc.TruePositives, &sc.FalsePositives, &sc.FalseNegatives,
			&sc.Status, &sc.VerifiedByType, &sc.VerifiedByEmail, &sc.VerifiedAt, &sc.CreatedAt,
		)
	if err != nil {
		return nil, fmt.Errorf("store: VerifySpotCheck: update: %w", err)
	}

	// Immediately roll up accuracy metrics for this merchant+platform so the
	// admin dashboard reflects the verification without waiting for the nightly job.
	today := now.UTC().Format("2006-01-02")
	if rollupErr := RollupAccuracyForMerchant(ctx, db, sc.MerchantID, sc.Platform, today); rollupErr != nil {
		// Non-fatal: the spot check is saved; metrics will sync on next nightly run.
		fmt.Printf("store: VerifySpotCheck: rollup warn: %v\n", rollupErr)
	}

	// Fetch integrity fields from citation_records.
	var rh, mv *string
	_ = db.QueryRow(ctx,
		`SELECT response_hash, model_version FROM citation_records WHERE id = $1`,
		sc.CitationRecordID).Scan(&rh, &mv)
	sc.ResponseHash, sc.IntegrityValid = computeIntegrity(rh, sc.AIResponse)
	if mv != nil {
		sc.ModelVersion = *mv
	}

	return &sc, nil
}

// RollupAccuracyForMerchant recomputes and upserts the accuracy metric for a
// specific merchant+platform+date from all verified spot checks in the DB.
// Called immediately after manual verification so the admin dashboard is live.
func RollupAccuracyForMerchant(ctx context.Context, db *pgxpool.Pool, merchantID int64, platform, date string) error {
	_, err := db.Exec(ctx, `
		INSERT INTO accuracy_metrics (merchant_id, date, platform, avg_precision, avg_recall, avg_f1, sample_size)
		SELECT
			$1, $2, platform,
			AVG(precision_score),
			AVG(recall_score),
			AVG(f1_score),
			COUNT(*)
		FROM spot_checks
		WHERE merchant_id = $1
		  AND platform    = $3
		  AND status      = 'verified'
		  AND precision_score IS NOT NULL
		GROUP BY platform
		ON CONFLICT (merchant_id, date, platform) DO UPDATE SET
			avg_precision = EXCLUDED.avg_precision,
			avg_recall    = EXCLUDED.avg_recall,
			avg_f1        = EXCLUDED.avg_f1,
			sample_size   = EXCLUDED.sample_size
	`, merchantID, date, platform)
	return err
}

const spotCheckSelect = `
	SELECT sc.id, sc.citation_record_id, sc.merchant_id, sc.query_text, sc.platform,
		sc.ai_response, sc.manual_brands, sc.detected_brands,
		sc.precision_score, sc.recall_score, sc.f1_score,
		sc.true_positives, sc.false_positives, sc.false_negatives,
		sc.status, sc.verified_by_type, sc.verified_by_email, sc.verified_at, sc.created_at,
		cr.response_hash, cr.model_version
	FROM spot_checks sc
	LEFT JOIN citation_records cr ON cr.id = sc.citation_record_id`

func scanSpotCheck(row interface {
	Scan(...any) error
}) (SpotCheck, error) {
	var sc SpotCheck
	var responseHash *string
	var modelVersion *string
	if err := row.Scan(
		&sc.ID, &sc.CitationRecordID, &sc.MerchantID,
		&sc.QueryText, &sc.Platform, &sc.AIResponse,
		&sc.ManualBrands, &sc.DetectedBrands,
		&sc.Precision, &sc.Recall, &sc.F1Score,
		&sc.TruePositives, &sc.FalsePositives, &sc.FalseNegatives,
		&sc.Status, &sc.VerifiedByType, &sc.VerifiedByEmail, &sc.VerifiedAt, &sc.CreatedAt,
		&responseHash, &modelVersion,
	); err != nil {
		return sc, err
	}
	sc.ResponseHash, sc.IntegrityValid = computeIntegrity(responseHash, sc.AIResponse)
	if modelVersion != nil {
		sc.ModelVersion = *modelVersion
	}
	return sc, nil
}

// GetSpotChecks returns the most recent spot checks for a merchant.
func GetSpotChecks(ctx context.Context, db *pgxpool.Pool, merchantID int64, limit int) ([]SpotCheck, error) {
	rows, err := db.Query(ctx,
		spotCheckSelect+` WHERE sc.merchant_id = $1 ORDER BY sc.created_at DESC LIMIT $2`,
		merchantID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: GetSpotChecks: %w", err)
	}
	defer rows.Close()

	var out []SpotCheck
	for rows.Next() {
		sc, err := scanSpotCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// GetSpotCheckByID fetches a single spot check, scoped to the merchant.
func GetSpotCheckByID(ctx context.Context, db *pgxpool.Pool, id, merchantID int64) (*SpotCheck, error) {
	sc, err := scanSpotCheck(db.QueryRow(ctx,
		spotCheckSelect+` WHERE sc.id=$1 AND sc.merchant_id=$2`,
		id, merchantID))
	if err != nil {
		return nil, fmt.Errorf("store: GetSpotCheckByID: %w", err)
	}
	return &sc, nil
}

// UpsertAccuracyMetrics inserts or replaces the daily accuracy row for merchant+platform.
func UpsertAccuracyMetrics(ctx context.Context, db *pgxpool.Pool, merchantID int64, date, platform string, m scoring.Metrics, sampleSize int) error {
	_, err := db.Exec(ctx, `
		INSERT INTO accuracy_metrics (merchant_id, date, platform, avg_precision, avg_recall, avg_f1, sample_size)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (merchant_id, date, platform) DO UPDATE SET
			avg_precision = EXCLUDED.avg_precision,
			avg_recall    = EXCLUDED.avg_recall,
			avg_f1        = EXCLUDED.avg_f1,
			sample_size   = EXCLUDED.sample_size
	`, merchantID, date, platform, m.Precision, m.Recall, m.F1, sampleSize)
	return err
}

// GetAccuracyMetrics returns the last 30 days of accuracy metrics for a merchant.
func GetAccuracyMetrics(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]AccuracyMetric, error) {
	rows, err := db.Query(ctx, `
		SELECT date, platform, avg_precision, avg_recall, avg_f1, sample_size
		FROM accuracy_metrics
		WHERE merchant_id = $1 AND date >= CURRENT_DATE - INTERVAL '30 days'
		ORDER BY date DESC, platform
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store: GetAccuracyMetrics: %w", err)
	}
	defer rows.Close()

	var out []AccuracyMetric
	for rows.Next() {
		var m AccuracyMetric
		if err := rows.Scan(&m.Date, &m.Platform, &m.AvgPrecision, &m.AvgRecall, &m.AvgF1, &m.SampleSize); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CitationSample is a minimal view of citation_records for the validation worker.
type CitationSample struct {
	ID          int64
	MerchantID  int64
	Platform    string
	Query       string
	AnswerText  string
	Competitors []string
}

// SampleCitationRecords returns a stratified sample of yesterday's citation records
// across all platforms for the daily validation job.
// limitPerPlatform caps per-platform records (default 50 if <= 0).
func SampleCitationRecords(ctx context.Context, db *pgxpool.Pool, limitPerPlatform int) ([]CitationSample, error) {
	if limitPerPlatform <= 0 {
		limitPerPlatform = 50
	}
	rows, err := db.Query(ctx, `
		SELECT id, merchant_id, platform, query, answer_text, competitors
		FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY platform ORDER BY RANDOM()) AS rn
			FROM citation_records
			WHERE scanned_at = CURRENT_DATE - 1
			  AND answer_text IS NOT NULL
			  AND answer_text <> ''
		) ranked
		WHERE rn <= $1
	`, limitPerPlatform)
	if err != nil {
		return nil, fmt.Errorf("store: SampleCitationRecords: %w", err)
	}
	defer rows.Close()

	var out []CitationSample
	for rows.Next() {
		var s CitationSample
		var answerText *string
		var competJSON []byte
		if err := rows.Scan(&s.ID, &s.MerchantID, &s.Platform, &s.Query, &answerText, &competJSON); err != nil {
			return nil, err
		}
		if answerText != nil {
			s.AnswerText = *answerText
		}
		var competitors []struct {
			Name string `json:"name"`
		}
		if len(competJSON) > 0 {
			_ = json.Unmarshal(competJSON, &competitors)
		}
		for _, c := range competitors {
			if c.Name != "" {
				s.Competitors = append(s.Competitors, c.Name)
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

