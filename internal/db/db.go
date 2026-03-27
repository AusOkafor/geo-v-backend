package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgxpool.Pool with production-tuned settings.
//
// Which URL to pass:
//   - cmd/api     → cfg.DatabaseURL       (Supabase transaction pooler)
//   - cmd/worker  → cfg.DatabaseDirectURL (direct connection — River needs LISTEN/NOTIFY)
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}

	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	// Supabase's connection pooler (Supavisor) does not support pgx's prepared
	// statement cache — disable it to avoid "prepared statement already exists" errors.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Verify connectivity at startup
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}
