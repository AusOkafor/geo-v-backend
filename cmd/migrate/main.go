package main

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()

	// River needs a direct connection (not the pooler)
	dbURL := os.Getenv("DATABASE_DIRECT_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		log.Fatal("DATABASE_DIRECT_URL or DATABASE_URL must be set")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatal("db connect:", err)
	}
	defer pool.Close()

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		log.Fatal("create migrator:", err)
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
