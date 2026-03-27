package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/yourname/geo-backend/internal/config"
	"github.com/yourname/geo-backend/internal/db"
	"github.com/yourname/geo-backend/internal/jobs"
	"github.com/yourname/geo-backend/internal/platform"
	"github.com/yourname/geo-backend/internal/platform/gemini"
	"github.com/yourname/geo-backend/internal/platform/openai"
	"github.com/yourname/geo-backend/internal/platform/perplexity"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Workers use the DIRECT connection (River needs LISTEN/NOTIFY, pooler breaks it)
	pool, err := db.NewPool(ctx, cfg.DatabaseDirectURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	encKey := []byte(cfg.EncryptionKey)

	// AI clients
	aiClients := []platform.AIClient{
		openai.New(cfg.OpenAIKey),
		perplexity.New(cfg.PerplexityKey),
		gemini.New(cfg.GeminiKey),
	}

	// Build River workers
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewScanWorker(pool, aiClients))
	river.AddWorker(workers, jobs.NewProductSyncWorker(pool, encKey))
	river.AddWorker(workers, jobs.NewDataDeletionWorker(pool))
	river.AddWorker(workers, jobs.NewFixGenerationWorker(pool, cfg.AnthropicKey))
	river.AddWorker(workers, jobs.NewFixApplyWorker(pool, encKey))

	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			"scans":  {MaxWorkers: cfg.ScanWorkerCount},
			"sync":   {MaxWorkers: 5},
			"fixes":  {MaxWorkers: 3},
			"apply":  {MaxWorkers: 2},
		},
		Workers:      workers,
		PeriodicJobs: jobs.BuildPeriodicJobs(),
		ErrorHandler: &jobs.SentryErrorHandler{},
	})
	if err != nil {
		slog.Error("river client init failed", "err", err)
		os.Exit(1)
	}

	// Register scheduler workers (need the riverClient reference)
	river.AddWorker(workers, jobs.NewDailyScanScheduler(pool, riverClient))
	river.AddWorker(workers, jobs.NewWeeklyFixScheduler(pool, riverClient))

	if err := riverClient.Start(ctx); err != nil {
		slog.Error("river start failed", "err", err)
		os.Exit(1)
	}

	slog.Info("worker started")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("worker shutting down...")
	shutCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout())
	defer cancel()
	if err := riverClient.StopAndCancel(shutCtx); err != nil {
		slog.Error("worker shutdown error", "err", err)
	}
}

