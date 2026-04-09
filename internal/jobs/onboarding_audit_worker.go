package jobs

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/service"
)

// OnboardingAuditWorker reads the merchant's live Shopify store state and
// persists results to merchant_audit, merchant_product_audit, merchant_collection_audit,
// merchant_page_audit, and merchant_audit_progress.
// After completing it queues fix generation so the merchant sees fixes immediately
// after install without needing to trigger a full AI scan first.
type OnboardingAuditWorker struct {
	river.WorkerDefaults[OnboardingAuditJobArgs]
	auditService *service.AuditService
	riverClient  *river.Client[pgx.Tx]
}

func NewOnboardingAuditWorker(auditService *service.AuditService, riverClient *river.Client[pgx.Tx]) *OnboardingAuditWorker {
	return &OnboardingAuditWorker{
		auditService: auditService,
		riverClient:  riverClient,
	}
}

func (w *OnboardingAuditWorker) Work(ctx context.Context, job *river.Job[OnboardingAuditJobArgs]) error {
	merchantID := job.Args.MerchantID

	if err := w.auditService.RunFullAudit(ctx, merchantID); err != nil {
		return err
	}

	// Queue fix generation so the merchant sees actionable fixes immediately after
	// install — without needing to trigger a full AI scan first.
	// The fix worker will retry automatically if product sync is still in progress.
	if _, err := w.riverClient.Insert(ctx, FixGenerationJobArgs{MerchantID: merchantID}, nil); err != nil {
		slog.Warn("onboarding audit: failed to queue fix generation (non-fatal)",
			"merchant_id", merchantID, "err", err)
	} else {
		slog.Info("onboarding audit: fix generation queued", "merchant_id", merchantID)
	}

	return nil
}
