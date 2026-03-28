package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/store"
)

// DailyScanArgs is the argument type for the daily scan scheduler periodic job.
type DailyScanArgs struct{}

func (DailyScanArgs) Kind() string { return "daily_scan_scheduler" }

func (DailyScanArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "scans", MaxAttempts: 1}
}

// DailyScanScheduler enqueues ScanJobArgs for every active merchant.
type DailyScanScheduler struct {
	river.WorkerDefaults[DailyScanArgs]
	db          *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
}

func NewDailyScanScheduler(db *pgxpool.Pool, rc *river.Client[pgx.Tx]) *DailyScanScheduler {
	return &DailyScanScheduler{db: db, riverClient: rc}
}

func (w *DailyScanScheduler) Work(ctx context.Context, _ *river.Job[DailyScanArgs]) error {
	merchants, err := store.GetActiveMerchants(ctx, w.db)
	if err != nil {
		return err
	}

	params := make([]river.InsertManyParams, 0, len(merchants))
	for _, m := range merchants {
		params = append(params, river.InsertManyParams{
			Args: ScanJobArgs{MerchantID: m.ID, Priority: "normal"},
		})
	}
	if len(params) == 0 {
		return nil
	}
	_, err = w.riverClient.InsertMany(ctx, params)
	return err
}

// WeeklyFixArgs is the argument type for the weekly fix-generation periodic job.
type WeeklyFixArgs struct{}

func (WeeklyFixArgs) Kind() string { return "weekly_fix_scheduler" }

func (WeeklyFixArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "fixes", MaxAttempts: 1}
}

// WeeklyFixScheduler enqueues FixGenerationJobArgs for every active merchant.
type WeeklyFixScheduler struct {
	river.WorkerDefaults[WeeklyFixArgs]
	db          *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
}

func NewWeeklyFixScheduler(db *pgxpool.Pool, rc *river.Client[pgx.Tx]) *WeeklyFixScheduler {
	return &WeeklyFixScheduler{db: db, riverClient: rc}
}

func (w *WeeklyFixScheduler) Work(ctx context.Context, _ *river.Job[WeeklyFixArgs]) error {
	merchants, err := store.GetActiveMerchants(ctx, w.db)
	if err != nil {
		return err
	}

	params := make([]river.InsertManyParams, 0, len(merchants))
	for _, m := range merchants {
		params = append(params, river.InsertManyParams{
			Args: FixGenerationJobArgs{MerchantID: m.ID},
		})
	}
	if len(params) == 0 {
		return nil
	}
	_, err = w.riverClient.InsertMany(ctx, params)
	return err
}

// BuildPeriodicJobs returns River periodic job configs for the worker.
//
//   - Daily scan: every 24h (scheduled via cron in production to fire at 02:00 UTC)
//   - Weekly fix gen: every 7 days
func BuildPeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(
			river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return DailyScanArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
		river.NewPeriodicJob(
			river.PeriodicInterval(7*24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) {
				return WeeklyFixArgs{}, nil
			},
			&river.PeriodicJobOpts{RunOnStart: true},
		),
	}
}
