package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/yourname/geo-backend/internal/crypto"
	"github.com/yourname/geo-backend/internal/shopify"
	"github.com/yourname/geo-backend/internal/store"
)

// ProductSyncWorker fetches all products from Shopify and upserts them locally.
type ProductSyncWorker struct {
	river.WorkerDefaults[ProductSyncJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
}

func NewProductSyncWorker(db *pgxpool.Pool, encKey []byte) *ProductSyncWorker {
	return &ProductSyncWorker{db: db, encryptionKey: encKey}
}

func (w *ProductSyncWorker) Work(ctx context.Context, job *river.Job[ProductSyncJobArgs]) error {
	merchant, err := store.GetMerchant(ctx, w.db, job.Args.MerchantID)
	if err != nil {
		return fmt.Errorf("sync: load merchant: %w", err)
	}
	if !merchant.Active {
		return nil
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, w.encryptionKey)
	if err != nil {
		return fmt.Errorf("sync: decrypt token for %s: %w", merchant.ShopDomain, err)
	}

	products, err := shopify.FetchAllProducts(ctx, merchant.ShopDomain, token)
	if err != nil {
		return fmt.Errorf("sync: fetch products: %w", err)
	}

	return store.UpsertProducts(ctx, w.db, merchant.ID, products)
}

// DataDeletionWorker removes all data for a shop on GDPR uninstall.
type DataDeletionWorker struct {
	river.WorkerDefaults[DataDeletionJobArgs]
	db *pgxpool.Pool
}

func NewDataDeletionWorker(db *pgxpool.Pool) *DataDeletionWorker {
	return &DataDeletionWorker{db: db}
}

func (w *DataDeletionWorker) Work(ctx context.Context, job *river.Job[DataDeletionJobArgs]) error {
	return store.DeleteMerchantData(ctx, w.db, job.Args.ShopDomain)
}
