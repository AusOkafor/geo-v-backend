package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MerchantFAQ is a single Q&A pair entered or approved by the merchant.
type MerchantFAQ struct {
	ID         int64  `json:"id"`
	MerchantID int64  `json:"merchant_id"`
	Question   string `json:"question"`
	Answer     string `json:"answer"`
	Position   int    `json:"position"`
	Active     bool   `json:"active"`
}

// GetMerchantFAQs returns all active FAQs for a merchant ordered by position.
func GetMerchantFAQs(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]MerchantFAQ, error) {
	rows, err := db.Query(ctx, `
		SELECT id, merchant_id, question, answer, position, active
		FROM merchant_faqs
		WHERE merchant_id = $1 AND active = true
		ORDER BY position, id
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store.GetMerchantFAQs: %w", err)
	}
	defer rows.Close()

	var faqs []MerchantFAQ
	for rows.Next() {
		var f MerchantFAQ
		if err := rows.Scan(&f.ID, &f.MerchantID, &f.Question, &f.Answer, &f.Position, &f.Active); err != nil {
			return nil, err
		}
		faqs = append(faqs, f)
	}
	if faqs == nil {
		faqs = []MerchantFAQ{}
	}
	return faqs, rows.Err()
}

// ReplaceMerchantFAQs replaces all FAQs for a merchant in a single transaction.
// Passing an empty slice clears all FAQs.
func ReplaceMerchantFAQs(ctx context.Context, db *pgxpool.Pool, merchantID int64, faqs []MerchantFAQ) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store.ReplaceMerchantFAQs: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM merchant_faqs WHERE merchant_id = $1`, merchantID); err != nil {
		return fmt.Errorf("store.ReplaceMerchantFAQs: delete: %w", err)
	}

	for i, f := range faqs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO merchant_faqs (merchant_id, question, answer, position)
			VALUES ($1, $2, $3, $4)
		`, merchantID, f.Question, f.Answer, i); err != nil {
			return fmt.Errorf("store.ReplaceMerchantFAQs: insert %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store.ReplaceMerchantFAQs: commit: %w", err)
	}
	return nil
}
