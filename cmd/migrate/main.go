package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	// Schema migrations use the transaction-pooler URL (simple SQL exec, no prepared statements)
	schemaURL := os.Getenv("DATABASE_URL")
	if schemaURL == "" {
		schemaURL = os.Getenv("DATABASE_DIRECT_URL")
	}
	if schemaURL == "" {
		log.Fatal("DATABASE_URL or DATABASE_DIRECT_URL must be set")
	}

	// River migrations need a connection that supports prepared statements
	riverURL := os.Getenv("DATABASE_SESSION_URL")
	if riverURL == "" {
		riverURL = os.Getenv("DATABASE_DIRECT_URL")
	}
	if riverURL == "" {
		riverURL = schemaURL
	}

	// ── 1. Schema migrations ──────────────────────────────────────────────────
	schemaPool, err := pgxpool.New(ctx, schemaURL)
	if err != nil {
		log.Fatal("schema db connect:", err)
	}
	defer schemaPool.Close()

	migrationsDir := findMigrationsDir()
	log.Printf("Running schema migrations from %s", migrationsDir)

	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		log.Fatal("read migrations dir:", err)
	}

	// Collect and sort .up.sql files
	var upFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, filepath.Join(migrationsDir, e.Name()))
		}
	}
	sort.Strings(upFiles)

	for _, path := range upFiles {
		sql, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s: %v", path, err)
		}
		if _, err := schemaPool.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("exec %s: %v", filepath.Base(path), err)
		}
		log.Printf("Schema: applied %s", filepath.Base(path))
	}

	// ── 2. River migrations ───────────────────────────────────────────────────
	riverPool, err := pgxpool.New(ctx, riverURL)
	if err != nil {
		log.Fatal("river db connect:", err)
	}
	defer riverPool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(riverPool), nil)
	if err != nil {
		log.Fatal("create river migrator:", err)
	}

	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		log.Fatal("river migrate:", err)
	}

	if len(res.Versions) == 0 {
		log.Println("River tables already up to date")
	}
	for _, v := range res.Versions {
		log.Printf("River: migrated to version %d", v.Version)
	}
	log.Println("Done.")
}

// findMigrationsDir returns the path to SQL migration files.
// In Docker (scratch image), files are copied to /migrations.
// Locally, fall back to ./migrations relative to CWD.
func findMigrationsDir() string {
	candidates := []string{"/migrations", "migrations", "../migrations", "../../migrations"}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	panic(fmt.Sprintf("migrations directory not found; tried: %v", candidates))
}
