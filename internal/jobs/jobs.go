package jobs

import "github.com/riverqueue/river"

// ─── ScanJobArgs ─────────────────────────────────────────────────────────────

// ScanJobArgs triggers an AI visibility scan for one merchant.
type ScanJobArgs struct {
	MerchantID int64  `json:"merchant_id"`
	Priority   string `json:"priority"` // "high" | "normal"
}

func (ScanJobArgs) Kind() string { return "scan" }

func (a ScanJobArgs) InsertOpts() river.InsertOpts {
	// River priority: 1=highest, 4=default
	p := 4
	if a.Priority == "high" {
		p = 1
	}
	return river.InsertOpts{
		Queue:       "scans",
		MaxAttempts: 3,
		Priority:    p,
	}
}

// ─── ProductSyncJobArgs ───────────────────────────────────────────────────────

// ProductSyncJobArgs syncs a merchant's product catalogue from Shopify.
type ProductSyncJobArgs struct {
	MerchantID int64 `json:"merchant_id"`
	Full       bool  `json:"full"` // true = re-sync all; false = incremental
}

func (ProductSyncJobArgs) Kind() string { return "product_sync" }

func (ProductSyncJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "sync",
		MaxAttempts: 3,
	}
}

// ─── FixGenerationJobArgs ─────────────────────────────────────────────────────

// FixGenerationJobArgs requests Claude to generate fix recommendations.
type FixGenerationJobArgs struct {
	MerchantID int64 `json:"merchant_id"`
}

func (FixGenerationJobArgs) Kind() string { return "fix_generation" }

func (FixGenerationJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "fixes",
		MaxAttempts: 2,
	}
}

// ─── FixApplyJobArgs ──────────────────────────────────────────────────────────

// FixApplyJobArgs applies one approved fix to Shopify.
// Rate limit: must not fire more than 1 mutation/second per store.
type FixApplyJobArgs struct {
	MerchantID int64  `json:"merchant_id"`
	FixID      int64  `json:"fix_id"`
}

func (FixApplyJobArgs) Kind() string { return "fix_apply" }

func (FixApplyJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "apply",
		MaxAttempts: 5,
	}
}

// ─── SchemaRebuildJobArgs ─────────────────────────────────────────────────────

// SchemaRebuildJobArgs rebuilds and re-pushes the shop schema metafield.
// Triggered when merchant settings change (social links, brand name) so the
// live schema stays in sync without requiring a new fix approval.
type SchemaRebuildJobArgs struct {
	MerchantID int64 `json:"merchant_id"`
}

func (SchemaRebuildJobArgs) Kind() string { return "schema_rebuild" }

func (SchemaRebuildJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "apply",
		MaxAttempts: 3,
	}
}

// ─── ValidationJobArgs ───────────────────────────────────────────────────────

// ValidationJobArgs runs a daily accuracy validation pass over yesterday's scans.
type ValidationJobArgs struct{}

func (ValidationJobArgs) Kind() string { return "validation" }

func (ValidationJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "scans",
		MaxAttempts: 2,
	}
}

// ─── DataDeletionJobArgs ──────────────────────────────────────────────────────

// DataDeletionJobArgs deletes all data for a store on GDPR uninstall.
type DataDeletionJobArgs struct {
	ShopDomain string `json:"shop_domain"`
}

func (DataDeletionJobArgs) Kind() string { return "data_deletion" }

func (DataDeletionJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "sync",
		MaxAttempts: 3,
	}
}
