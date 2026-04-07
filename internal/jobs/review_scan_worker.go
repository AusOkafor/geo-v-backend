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

	// Phase 1: detect app by inspecting the active theme's snippet files.
	// Uses read_content scope — no additional OAuth permissions required.
	themeApp, err := shopify.DetectReviewAppFromTheme(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("review scan: theme detection failed",
			"merchant_id", merchantID, "err", err)
	}

	slog.Info("review scan: theme detection",
		"merchant_id", merchantID,
		"detected_app", themeApp,
	)

	// Phase 2: fetch product metafields for rating data.
	// Legacy review apps (that write to public namespaces) will populate this.
	metafields, err := shopify.FetchProductReviewMetafields(ctx, merchant.ShopDomain, token, 5)
	if err != nil {
		slog.Warn("review scan: metafield fetch failed",
			"merchant_id", merchantID, "err", err)
	}

	data := reviews.Detect(metafields)

	// If theme detection found an app but metafields had no data,
	// use the theme result for the app name with zero ratings.
	if data.App == reviews.AppNone && themeApp != "" {
		data.App = reviews.App(themeApp)
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
		false,
	); err != nil {
		return fmt.Errorf("review scan: save: %w", err)
	}

	if data.HasReviews {
		if _, err := w.riverClient.Insert(ctx, SchemaRebuildJobArgs{MerchantID: merchantID}, nil); err != nil {
			slog.Warn("review scan: could not enqueue schema rebuild", "err", err)
		}
	}

	return nil
}
