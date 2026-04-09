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
	"github.com/austinokafor/geo-backend/internal/service"
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
	pool, err := db.NewPoolWithSize(ctx, cfg.DatabaseDirectURL, false, 5) // River needs extended protocol; keep small for Supabase free tier
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

	// Workers that don't need riverClient are registered before client creation.
	workers := river.NewWorkers()
	river.AddWorker(workers, jobs.NewProductSyncWorker(pool, encKey))
	river.AddWorker(workers, jobs.NewDataDeletionWorker(pool))
	river.AddWorker(workers, jobs.NewSchemaRebuildWorker(pool, encKey))
	river.AddWorker(workers, jobs.NewValidationWorker(pool))

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

	// Build services — require riverClient for job enqueueing.
	auditSvc := service.NewAuditService(pool, encKey)
	fixSvc := service.NewFixService(pool, encKey, fixGenerator, riverClient)
	scanSvc := service.NewScanService(pool, aiClients, riverClient)

	// Register workers that need riverClient or services.
	river.AddWorker(workers, jobs.NewScanWorker(scanSvc))
	river.AddWorker(workers, jobs.NewDailyScanScheduler(pool, riverClient))
	river.AddWorker(workers, jobs.NewWeeklyFixScheduler(pool, riverClient))
	river.AddWorker(workers, jobs.NewFixGenerationWorker(fixSvc))
	river.AddWorker(workers, jobs.NewFixApplyWorker(pool, encKey, riverClient))
	river.AddWorker(workers, jobs.NewReviewScanWorker(pool, encKey, riverClient))
	river.AddWorker(workers, jobs.NewOnboardingAuditWorker(auditSvc, riverClient))

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

