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

// Product mirrors a row from the products table.
type Product struct {
	ID          int64
	MerchantID  int64
	ShopifyGID  string
	Title       string
	Description string
	Tags        []string
}

// GetProducts returns all products for a merchant.
func GetProducts(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]Product, error) {
	rows, err := db.Query(ctx, `
		SELECT id, merchant_id, shopify_gid, title, description, tags
		FROM products WHERE merchant_id = $1 ORDER BY id
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store.GetProducts: %w", err)
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.MerchantID, &p.ShopifyGID, &p.Title, &p.Description, &p.Tags); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

// nullableNumeric returns nil for empty/zero price strings.
func nullableNumeric(s string) interface{} {
	if s == "" || s == "0" || s == "0.00" {
		return nil
	}
	return s
}
