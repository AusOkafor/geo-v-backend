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

	// ── Phase 1: detect app + extract API key from active theme ──────────────
	themeResult, err := shopify.DetectReviewAppFromTheme(ctx, merchant.ShopDomain, token)
	if err != nil {
		slog.Warn("review scan: theme detection failed",
			"merchant_id", merchantID, "err", err)
		themeResult = &shopify.ThemeDetectionResult{}
	}

	slog.Info("review scan: theme detection",
		"merchant_id", merchantID,
		"detected_app", themeResult.App,
		"has_app_key", themeResult.AppKey != "",
	)

	// ── Phase 2: fetch rating data ────────────────────────────────────────────
	var avgRating float64
	var totalCount int
	var detectedApp reviews.App

	// 2a. Try Yotpo direct API if app key was extracted.
	if themeResult.App == "yotpo" && themeResult.AppKey != "" {
		productMetafields, _ := shopify.FetchProductReviewMetafields(ctx, merchant.ShopDomain, token, 10)
		productGIDs := make([]string, 0, len(productMetafields))
		for _, p := range productMetafields {
			productGIDs = append(productGIDs, p.ProductGID)
		}

		r, c, apiErr := reviews.FetchYotpoRatings(ctx, themeResult.AppKey, productGIDs)
		if apiErr != nil {
			slog.Warn("review scan: yotpo api failed",
				"merchant_id", merchantID, "err", apiErr)
		} else {
			avgRating = r
			totalCount = c
			slog.Info("review scan: yotpo api result",
				"merchant_id", merchantID,
				"avg_rating", avgRating,
				"total_reviews", totalCount,
			)
		}
		detectedApp = reviews.AppYotpo
	} else {
		// 2b. Fallback: read product metafields (works for apps that write public namespaces).
		metafields, metaErr := shopify.FetchProductReviewMetafields(ctx, merchant.ShopDomain, token, 5)
		if metaErr != nil {
			slog.Warn("review scan: metafield fetch failed",
				"merchant_id", merchantID, "err", metaErr)
		}
		data := reviews.Detect(metafields)
		avgRating = data.AvgRating
		totalCount = data.TotalCount

		// Use theme detection result for app name when metafields found nothing.
		detectedApp = data.App
		if detectedApp == reviews.AppNone && themeResult.App != "" {
			detectedApp = reviews.App(themeResult.App)
		}
	}

	slog.Info("review scan: complete",
		"merchant_id", merchantID,
		"app", detectedApp,
		"avg_rating", avgRating,
		"total_reviews", totalCount,
	)

	if err := store.SaveMerchantReviews(
		ctx, w.db, merchantID,
		string(detectedApp),
		themeResult.AppKey,
		avgRating,
		totalCount,
		false,
	); err != nil {
		return fmt.Errorf("review scan: save: %w", err)
	}

	// Trigger schema rebuild whenever an app is detected, even with zero ratings.
	// SchemaRebuildWorker will inject aggregateRating only when avg_rating > 0.
	if detectedApp != reviews.AppNone {
		if _, err := w.riverClient.Insert(ctx, SchemaRebuildJobArgs{MerchantID: merchantID}, nil); err != nil {
			slog.Warn("review scan: could not enqueue schema rebuild", "err", err)
		}
	}

	return nil
}
