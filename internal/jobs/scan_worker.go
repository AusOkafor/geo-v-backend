package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/service"
)

// ScanWorker executes AI visibility scans for a single merchant.
type ScanWorker struct {
	river.WorkerDefaults[ScanJobArgs]
	scanService *service.ScanService
}

// Timeout overrides the default River job timeout.
// Real LLM calls are slow; Gemini 429 waits can add significant latency.
func (w *ScanWorker) Timeout(_ *river.Job[ScanJobArgs]) time.Duration {
	return 35 * time.Minute
}

func NewScanWorker(scanService *service.ScanService) *ScanWorker {
	return &ScanWorker{scanService: scanService}
}

func (w *ScanWorker) Work(ctx context.Context, job *river.Job[ScanJobArgs]) error {
	merchantID := job.Args.MerchantID
	slog.Info("scan: starting", "merchant_id", merchantID)

	result, err := w.scanService.RunScan(ctx, merchantID)
	if err != nil {
		return err
	}

	slog.Info("scan: complete",
		"merchant_id", merchantID,
		"queries_run", result.QueriesRun,
		"mentions", result.TotalMentions,
		"platforms", result.Platforms,
		"duration_ms", result.DurationMs,
	)
	return nil
}
