package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CostGuardrails maps plan name to the monthly spend threshold (60% of plan revenue).
var CostGuardrails = map[string]float64{
	"free":    0,     // no scans on free plan
	"starter": 17.40, // 60% of $29
	"growth":  47.40, // 60% of $79
	"pro":     107.40, // 60% of $179
}

// GetMonthlyCostByMerchant returns the total scan cost for a merchant in the current calendar month.
func GetMonthlyCostByMerchant(ctx context.Context, db *pgxpool.Pool, merchantID int64) (float64, error) {
	var cost float64
	err := db.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0)
		FROM scan_costs
		WHERE merchant_id = $1
		  AND date_trunc('month', cost_date) = date_trunc('month', CURRENT_DATE)
	`, merchantID).Scan(&cost)
	if err != nil {
		return 0, fmt.Errorf("store.GetMonthlyCostByMerchant: %w", err)
	}
	return cost, nil
}

// ExceedsGuardrail returns true if the merchant's monthly cost has reached
// 60% of their plan revenue — scans should be skipped.
func ExceedsGuardrail(monthlyCost float64, plan string) bool {
	threshold, ok := CostGuardrails[plan]
	if !ok {
		return false
	}
	return monthlyCost >= threshold
}
