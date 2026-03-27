package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/yourname/geo-backend/internal/api"
	"github.com/yourname/geo-backend/internal/config"
	"github.com/yourname/geo-backend/internal/db"
)

func main() {
	// Structured JSON logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Config
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// DB pool (transaction pooler URL for API)
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// River client (insert-only, no workers in this binary)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		slog.Error("river client init failed", "err", err)
		os.Exit(1)
	}

	// Echo
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus: true,
		LogURI:    true,
		LogMethod: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			slog.Info("request", "method", v.Method, "uri", v.URI, "status", v.Status)
			return nil
		},
	}))
	e.Use(middleware.Recover())
	e.Use(middleware.TimeoutWithConfig(middleware.TimeoutConfig{
		Timeout: 30 * time.Second,
	}))
	corsOrigins := []string{"https://*.myshopify.com", "https://geo-app.vercel.app"}
	if !cfg.IsProd() {
		corsOrigins = []string{"*"}
	}
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: corsOrigins,
		AllowHeaders: []string{echo.HeaderContentType, echo.HeaderAuthorization},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
	}))

	h := &api.Handler{
		DB:     pool,
		River:  riverClient,
		Config: cfg,
	}
	h.RegisterRoutes(e)

	// Start server
	go func() {
		addr := ":" + cfg.Port
		slog.Info("starting api server", "addr", addr)
		if err := e.Start(addr); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	shutCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout())
	defer cancel()
	if err := e.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}
