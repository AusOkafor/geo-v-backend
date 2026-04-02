package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgxpool.Pool with connection limits tuned for Supabase free tier.
//
// Which URL to pass:
//   - cmd/api     → cfg.DatabaseURL       (Supabase transaction pooler)
//   - cmd/worker  → cfg.DatabaseDirectURL (direct connection — River needs LISTEN/NOTIFY)
//
// simpleProtocol should be true only for the transaction pooler (Supavisor).
// River's internal queries break with simple protocol, so pass false for workers.
func NewPool(ctx context.Context, databaseURL string, simpleProtocol bool) (*pgxpool.Pool, error) {
	return NewPoolWithSize(ctx, databaseURL, simpleProtocol, 5)
}

// NewPoolWithSize creates a pool with an explicit max connection count.
// Use for River/session-mode pools where Supabase limits are tight.
func NewPoolWithSize(ctx context.Context, databaseURL string, simpleProtocol bool, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}

	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	// Supabase's connection pooler (Supavisor) does not support pgx's prepared
	// statement cache — disable it to avoid "prepared statement already exists" errors.
	// Do NOT set this for direct connections used by River workers.
	if simpleProtocol {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}

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
