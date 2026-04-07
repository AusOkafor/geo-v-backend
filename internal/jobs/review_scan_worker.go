package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/reviews"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// ReviewScanWorker detects which review app a merchant uses and stores the
// aggregated rating data. When reviews are found it triggers a SchemaRebuildJob
// so aggregateRating is injected into the live JSON-LD schema immediately.
type ReviewScanWorker struct {
	river.WorkerDefaults[ReviewScanJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
	riverClient   *river.Client[pgx.Tx]
}

func NewReviewScanWorker(db *pgxpool.Pool, encKey []byte, rc *river.Client[pgx.Tx]) *ReviewScanWorker {
	return &ReviewScanWorker{db: db, encryptionKey: encKey, riverClient: rc}
}

func (w *ReviewScanWorker) Work(ctx context.Context, job *river.Job[ReviewScanJobArgs]) error {
	merchantID := job.Args.MerchantID

	merchant, err := store.GetMerchant(ctx, w.db, merchantID)
	if err != nil {
		return fmt.Errorf("review scan: load merchant %d: %w", merchantID, err)
	}
	if !merchant.Active {
		return nil
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, w.encryptionKey)
	if err != nil {
		return fmt.Errorf("review scan: decrypt token: %w", err)
	}

	// Phase 1: detect app via installed apps list (does not rely on metafields).
	installedApps, err := shopify.FetchInstalledApps(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("review scan: installed apps fetch failed",
			"merchant_id", merchantID, "err", err)
	}

	detectedApp := reviews.AppNone
	if len(installedApps) > 0 {
		detectedApp = reviews.DetectAppFromInstalled(installedApps)
	}

	slog.Info("review scan: app detection",
		"merchant_id", merchantID,
		"app", detectedApp,
		"installed_apps_count", len(installedApps),
	)

	// Phase 2: fetch rating data from product metafields.
	// Some review apps still write to legacy (non-app-owned) metafields.
	// If this returns no data, we still record the detected app.
	metafields, err := shopify.FetchProductReviewMetafields(ctx, merchant.ShopDomain, token, 5)
	if err != nil {
		slog.Warn("review scan: metafield fetch failed",
			"merchant_id", merchantID, "err", err)
	}

	data := reviews.Detect(metafields)

	// If app detection via installed apps found something but metafields didn't,
	// use the installed-apps result for the app name with zero ratings.
	if data.App == reviews.AppNone && detectedApp != reviews.AppNone {
		data.App = detectedApp
	}

	slog.Info("review scan: complete",
		"merchant_id", merchantID,
		"app", data.App,
		"avg_rating", data.AvgRating,
		"total_reviews", data.TotalCount,
		"products_hit", data.ProductsHit,
	)

	if err := store.SaveMerchantReviews(
		ctx, w.db, merchantID,
		string(data.App),
		data.AvgRating,
		data.TotalCount,
		false, // schema_injected — SchemaRebuildWorker marks this true after rebuild
	); err != nil {
		return fmt.Errorf("review scan: save: %w", err)
	}

	// If reviews found, immediately rebuild the schema so aggregateRating goes live.
	if data.HasReviews {
		if _, err := w.riverClient.Insert(ctx, SchemaRebuildJobArgs{MerchantID: merchantID}, nil); err != nil {
			slog.Warn("review scan: could not enqueue schema rebuild", "err", err)
		}
	}

	return nil
}
