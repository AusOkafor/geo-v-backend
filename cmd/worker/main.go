package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/austinokafor/geo-backend/internal/config"
	"github.com/austinokafor/geo-backend/internal/db"
	"github.com/austinokafor/geo-backend/internal/fix"
	"github.com/austinokafor/geo-backend/internal/jobs"
	"github.com/austinokafor/geo-backend/internal/platform"
	"github.com/austinokafor/geo-backend/internal/platform/gemini"
	"github.com/austinokafor/geo-backend/internal/platform/mock"
	"github.com/austinokafor/geo-backend/internal/platform/openai"
	"github.com/austinokafor/geo-backend/internal/platform/perplexity"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// Sentry (optional — only active when SENTRY_DSN is set)
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:         cfg.SentryDSN,
			Environment: cfg.Environment,
		}); err != nil {
			slog.Warn("sentry init failed", "err", err)
		} else {
			defer sentry.Flush(2 * time.Second)
		}
	}

	ctx := context.Background()

	// Workers use the DIRECT connection (River needs LISTEN/NOTIFY, pooler breaks it)
	pool, err := db.NewPool(ctx, cfg.DatabaseDirectURL, false) // false = direct connection, River needs extended protocol
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	encKey := []byte(cfg.EncryptionKey)

	// AI clients — always use real APIs; MOCK_AI=true only for local dev
	var aiClients []platform.AIClient
	var fixGenerator fix.Generatable
	if cfg.MockAI {
		slog.Info("MOCK_AI enabled — using mock AI clients (no real API calls)")
		aiClients = []platform.AIClient{
			mock.New("chatgpt"),
			mock.New("perplexity"),
			mock.New("gemini"),
		}
		fixGenerator = fix.NewMockGenerator()
	} else {
		slog.Info("using real AI APIs",
			"openai", cfg.OpenAIKey != "",
			"perplexity", cfg.PerplexityKey != "",
			"gemini", cfg.GeminiKey != "",
			"anthropic", cfg.AnthropicKey != "",
		)
		aiClients = []platform.AIClient{
			openai.New(cfg.OpenAIKey),
			perplexity.New(cfg.PerplexityKey),
			gemini.New(cfg.GeminiKey),
		}
		fixGenerator = fix.NewGenerator(cfg.AnthropicKey)
	}

	// Build River workers — scan worker needs the riverClient to enqueue fix
	// generation after each scan, so it is registered after the client is created.
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewProductSyncWorker(pool, encKey))
	river.AddWorker(workers, jobs.NewDataDeletionWorker(pool))
	river.AddWorker(workers, jobs.NewFixGenerationWorker(pool, fixGenerator))
	river.AddWorker(workers, jobs.NewFixApplyWorker(pool, encKey))
	river.AddWorker(workers, jobs.NewSchemaRebuildWorker(pool, encKey))

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

	// Register workers that need the riverClient reference
	river.AddWorker(workers, jobs.NewScanWorker(pool, aiClients, riverClient))
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

