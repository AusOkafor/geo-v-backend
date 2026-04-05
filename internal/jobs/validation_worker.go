package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/detection"
	"github.com/austinokafor/geo-backend/internal/scoring"
	"github.com/austinokafor/geo-backend/internal/store"
)

const (
	alertThresholdF1    = 0.80 // WARN log if avg F1 drops below this
	criticalThresholdF1 = 0.70 // ERROR log if avg F1 drops below this
	limitPerPlatform    = 50   // max records sampled per platform per run
)

// ValidationWorker runs a daily accuracy pass over yesterday's citation records.
// It does NOT make any external API calls — operates entirely on stored answer_text.
// For each sampled record it re-extracts brands using the regex BrandDetector,
// compares against the stored competitors list, and writes per-platform F1 metrics
// to accuracy_metrics. Fires alerts if accuracy drops below thresholds.
type ValidationWorker struct {
	river.WorkerDefaults[ValidationJobArgs]
	db *pgxpool.Pool
}

func NewValidationWorker(db *pgxpool.Pool) *ValidationWorker {
	return &ValidationWorker{db: db}
}

func (w *ValidationWorker) Timeout(_ *river.Job[ValidationJobArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *ValidationWorker) Work(ctx context.Context, _ *river.Job[ValidationJobArgs]) error {
	runDate := time.Now().UTC().Format("2006-01-02")
	slog.Info("validation: starting daily run", "date", runDate)

	// Sample citation records from yesterday across all platforms
	records, err := store.SampleCitationRecords(ctx, w.db, limitPerPlatform)
	if err != nil {
		return fmt.Errorf("validation: sample records: %w", err)
	}
	if len(records) == 0 {
		slog.Info("validation: no records to validate", "date", runDate)
		return w.writeRunSummary(ctx, runDate, 0, scoring.Metrics{}, 0)
	}

	slog.Info("validation: sampled records", "count", len(records))

	// Group by merchant so we can build per-merchant detectors efficiently
	byMerchant := make(map[int64][]store.CitationSample)
	for _, r := range records {
		byMerchant[r.MerchantID] = append(byMerchant[r.MerchantID], r)
	}

	// Load merchant profiles (brand name + category) for detector construction
	merchantProfiles, err := store.GetActiveMerchants(ctx, w.db)
	if err != nil {
		return fmt.Errorf("validation: load merchants: %w", err)
	}
	profileByID := make(map[int64]store.Merchant, len(merchantProfiles))
	for _, m := range merchantProfiles {
		profileByID[m.ID] = m
	}

	// Per-merchant per-platform metric aggregation
	type platformAgg struct {
		totalPrecision float64
		totalRecall    float64
		totalF1        float64
		count          int
	}
	// merchantPlatformMetrics[merchantID][platform]
	merchantPlatformMetrics := make(map[int64]map[string]*platformAgg)
	alertsTriggered := 0

	for merchantID, samples := range byMerchant {
		profile, ok := profileByID[merchantID]
		if !ok {
			continue
		}

		// Build a single detector per merchant from all known competitor names
		// across their samples (union of competitor lists seen in the sample)
		competitorSet := make(map[string]bool)
		for _, s := range samples {
			for _, c := range s.Competitors {
				competitorSet[c] = true
			}
		}
		competitors := make([]string, 0, len(competitorSet))
		for c := range competitorSet {
			competitors = append(competitors, c)
		}
		detector := detection.New(profile.BrandName, competitors)

		// Score each record
		for _, s := range samples {
			if s.AnswerText == "" {
				continue
			}

			redetected := detector.BrandNames(s.AnswerText)
			m := scoring.Calculate(s.Competitors, redetected)

			if merchantPlatformMetrics[merchantID] == nil {
				merchantPlatformMetrics[merchantID] = make(map[string]*platformAgg)
			}
			if merchantPlatformMetrics[merchantID][s.Platform] == nil {
				merchantPlatformMetrics[merchantID][s.Platform] = &platformAgg{}
			}
			agg := merchantPlatformMetrics[merchantID][s.Platform]
			agg.totalPrecision += m.Precision
			agg.totalRecall += m.Recall
			agg.totalF1 += m.F1
			agg.count++

			slog.Debug("validation: record scored",
				"merchant_id", merchantID,
				"platform", s.Platform,
				"f1", fmt.Sprintf("%.3f", m.F1),
				"tp", m.TruePositives,
				"fp", m.FalsePositives,
				"fn", m.FalseNegatives,
			)
		}
	}

	// Write per-merchant per-platform accuracy metrics and fire alerts
	// Also accumulate a system-wide aggregate (merchant_id=0) for monitoring.
	systemByPlatform := make(map[string]*platformAgg)
	var overallPrecision, overallRecall, overallF1 float64
	var overallCount int

	for merchantID, platforms := range merchantPlatformMetrics {
		for platform, agg := range platforms {
			if agg.count == 0 {
				continue
			}

			avgMetrics := scoring.Metrics{
				Precision: agg.totalPrecision / float64(agg.count),
				Recall:    agg.totalRecall / float64(agg.count),
				F1:        agg.totalF1 / float64(agg.count),
			}

			// Write per-merchant metric — this is what the admin UI queries.
			if err := store.UpsertAccuracyMetrics(ctx, w.db, merchantID, runDate, platform, avgMetrics, agg.count); err != nil {
				slog.Warn("validation: failed to write merchant accuracy metrics",
					"merchant_id", merchantID, "platform", platform, "err", err)
			}

			// Accumulate into system-wide aggregate.
			if systemByPlatform[platform] == nil {
				systemByPlatform[platform] = &platformAgg{}
			}
			systemByPlatform[platform].totalPrecision += agg.totalPrecision
			systemByPlatform[platform].totalRecall += agg.totalRecall
			systemByPlatform[platform].totalF1 += agg.totalF1
			systemByPlatform[platform].count += agg.count
		}
	}

	// Write system-wide aggregate (merchant_id=0) and fire threshold alerts.
	for platform, agg := range systemByPlatform {
		if agg.count == 0 {
			continue
		}

		avgMetrics := scoring.Metrics{
			Precision: agg.totalPrecision / float64(agg.count),
			Recall:    agg.totalRecall / float64(agg.count),
			F1:        agg.totalF1 / float64(agg.count),
		}

		if err := store.UpsertAccuracyMetrics(ctx, w.db, 0, runDate, platform, avgMetrics, agg.count); err != nil {
			slog.Warn("validation: failed to write system accuracy metrics",
				"platform", platform, "err", err)
		}

		overallPrecision += avgMetrics.Precision
		overallRecall += avgMetrics.Recall
		overallF1 += avgMetrics.F1
		overallCount++

		// Threshold alerts
		if avgMetrics.F1 < criticalThresholdF1 {
			slog.Error("validation: CRITICAL — F1 below threshold",
				"platform", platform,
				"f1", fmt.Sprintf("%.3f", avgMetrics.F1),
				"precision", fmt.Sprintf("%.3f", avgMetrics.Precision),
				"recall", fmt.Sprintf("%.3f", avgMetrics.Recall),
				"sample_size", agg.count,
				"threshold", criticalThresholdF1,
			)
			alertsTriggered++
		} else if avgMetrics.F1 < alertThresholdF1 {
			slog.Warn("validation: F1 below warning threshold",
				"platform", platform,
				"f1", fmt.Sprintf("%.3f", avgMetrics.F1),
				"precision", fmt.Sprintf("%.3f", avgMetrics.Precision),
				"recall", fmt.Sprintf("%.3f", avgMetrics.Recall),
				"sample_size", agg.count,
				"threshold", alertThresholdF1,
			)
			alertsTriggered++
		} else {
			slog.Info("validation: platform accuracy OK",
				"platform", platform,
				"f1", fmt.Sprintf("%.3f", avgMetrics.F1),
				"sample_size", agg.count,
			)
		}
	}

	// Compute overall average across platforms
	var summary scoring.Metrics
	if overallCount > 0 {
		summary = scoring.Metrics{
			Precision: overallPrecision / float64(overallCount),
			Recall:    overallRecall / float64(overallCount),
			F1:        overallF1 / float64(overallCount),
		}
	}

	slog.Info("validation: run complete",
		"date", runDate,
		"records_sampled", len(records),
		"platforms", overallCount,
		"avg_f1", fmt.Sprintf("%.3f", summary.F1),
		"alerts", alertsTriggered,
	)

	return w.writeRunSummary(ctx, runDate, len(records), summary, alertsTriggered)
}

func (w *ValidationWorker) writeRunSummary(ctx context.Context, runDate string, total int, m scoring.Metrics, alerts int) error {
	_, err := w.db.Exec(ctx, `
		INSERT INTO validation_runs (run_date, total_queries, avg_precision, avg_recall, avg_f1, alerts_triggered)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (run_date) DO UPDATE SET
			total_queries     = EXCLUDED.total_queries,
			avg_precision     = EXCLUDED.avg_precision,
			avg_recall        = EXCLUDED.avg_recall,
			avg_f1            = EXCLUDED.avg_f1,
			alerts_triggered  = EXCLUDED.alerts_triggered,
			completed_at      = now()
	`, runDate, total, m.Precision, m.Recall, m.F1, alerts)
	if err != nil {
		return fmt.Errorf("validation: write run summary: %w", err)
	}
	return nil
}
