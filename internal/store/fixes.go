package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Fix mirrors the pending_fixes table.
type Fix struct {
	ID          int64      `json:"id"`
	MerchantID  int64      `json:"merchant_id"`
	TargetGID   string     `json:"target_gid"`
	FixType     string     `json:"fix_type"`
	Priority    string     `json:"priority"`
	Title       string     `json:"title"`
	Explanation string     `json:"explanation"`
	Original    json.RawMessage `json:"original"`
	Generated   json.RawMessage `json:"generated"`
	EstImpact   int        `json:"est_impact"`
	Status      string     `json:"status"`
	AppliedAt   *time.Time `json:"applied_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// GetFixes returns fixes for a merchant, optionally filtered by status.
func GetFixes(ctx context.Context, db *pgxpool.Pool, merchantID int64, status string) ([]Fix, error) {
	query := `
		SELECT id, merchant_id, target_gid, fix_type, priority, title, explanation,
		       original, generated, est_impact, status, applied_at, created_at, updated_at
		FROM pending_fixes
		WHERE merchant_id = $1`
	args := []any{merchantID}

	if status != "" {
		query += " AND status = $2"
		args = append(args, status)
	}
	query += " ORDER BY est_impact DESC, created_at DESC"

	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store.GetFixes: %w", err)
	}
	defer rows.Close()

	var fixes []Fix
	for rows.Next() {
		var f Fix
		if err := rows.Scan(
			&f.ID, &f.MerchantID, &f.TargetGID, &f.FixType, &f.Priority,
			&f.Title, &f.Explanation, &f.Original, &f.Generated,
			&f.EstImpact, &f.Status, &f.AppliedAt, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			return nil, err
		}
		fixes = append(fixes, f)
	}
	return fixes, rows.Err()
}

// GetFix fetches a single fix by ID, scoped to a merchant.
func GetFix(ctx context.Context, db *pgxpool.Pool, merchantID, fixID int64) (*Fix, error) {
	var f Fix
	err := db.QueryRow(ctx, `
		SELECT id, merchant_id, target_gid, fix_type, priority, title, explanation,
		       original, generated, est_impact, status, applied_at, created_at, updated_at
		FROM pending_fixes WHERE id = $1 AND merchant_id = $2
	`, fixID, merchantID).Scan(
		&f.ID, &f.MerchantID, &f.TargetGID, &f.FixType, &f.Priority,
		&f.Title, &f.Explanation, &f.Original, &f.Generated,
		&f.EstImpact, &f.Status, &f.AppliedAt, &f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.GetFix: %w", err)
	}
	return &f, nil
}

// ApproveFix sets fix status to approved.
func ApproveFix(ctx context.Context, db *pgxpool.Pool, merchantID, fixID int64) error {
	tag, err := db.Exec(ctx, `
		UPDATE pending_fixes SET status = 'approved', updated_at = now()
		WHERE id = $1 AND merchant_id = $2 AND status = 'pending'
	`, fixID, merchantID)
	if err != nil {
		return fmt.Errorf("store.ApproveFix: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store.ApproveFix: fix not found or not pending")
	}
	return nil
}

// RejectFix sets fix status to rejected.
func RejectFix(ctx context.Context, db *pgxpool.Pool, merchantID, fixID int64) error {
	tag, err := db.Exec(ctx, `
		UPDATE pending_fixes SET status = 'rejected', updated_at = now()
		WHERE id = $1 AND merchant_id = $2 AND status = 'pending'
	`, fixID, merchantID)
	if err != nil {
		return fmt.Errorf("store.RejectFix: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("store.RejectFix: fix not found or not pending")
	}
	return nil
}

// InsertFix creates a new pending fix.
func InsertFix(ctx context.Context, db *pgxpool.Pool, f Fix) (int64, error) {
	if f.Original == nil {
		f.Original = []byte("{}")
	}
	if f.Generated == nil {
		f.Generated = []byte("{}")
	}
	var id int64
	err := db.QueryRow(ctx, `
		INSERT INTO pending_fixes
			(merchant_id, target_gid, fix_type, priority, title, explanation, original, generated, est_impact)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, f.MerchantID, f.TargetGID, f.FixType, f.Priority, f.Title, f.Explanation,
		f.Original, f.Generated, f.EstImpact,
	).Scan(&id)
	return id, err
}

// SetFixStatus updates the status (and optionally applied_at) of a fix.
func SetFixStatus(ctx context.Context, db *pgxpool.Pool, fixID int64, status string) error {
	if status == "applied" {
		_, err := db.Exec(ctx, `
			UPDATE pending_fixes SET status = $1, applied_at = now(), updated_at = now() WHERE id = $2
		`, status, fixID)
		return err
	}
	_, err := db.Exec(ctx, `
		UPDATE pending_fixes SET status = $1, updated_at = now() WHERE id = $2
	`, status, fixID)
	return err
}
