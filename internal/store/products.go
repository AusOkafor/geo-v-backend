package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/austinokafor/geo-backend/internal/shopify"
)

// UpsertProducts syncs the Shopify product list into the products table.
func UpsertProducts(ctx context.Context, db *pgxpool.Pool, merchantID int64, products []shopify.ProductNode) error {
	if len(products) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, p := range products {
		batch.Queue(`
			INSERT INTO products (merchant_id, shopify_gid, title, description, tags, price_min, price_max)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7::numeric)
			ON CONFLICT (merchant_id, shopify_gid) DO UPDATE SET
				title       = EXCLUDED.title,
				description = EXCLUDED.description,
				tags        = EXCLUDED.tags,
				price_min   = EXCLUDED.price_min,
				price_max   = EXCLUDED.price_max,
				synced_at   = now()
		`, merchantID, p.ID, p.Title, p.DescriptionHTML, p.Tags,
			nullableNumeric(p.PriceMin), nullableNumeric(p.PriceMax),
		)
	}

	br := db.SendBatch(ctx, batch)
	defer br.Close()

	for range products {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("store.UpsertProducts: %w", err)
		}
	}
	return nil
}

// nullableNumeric returns nil for empty/zero price strings.
func nullableNumeric(s string) interface{} {
	if s == "" || s == "0" || s == "0.00" {
		return nil
	}
	return s
}
