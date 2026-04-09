package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/fix"
	"github.com/austinokafor/geo-backend/internal/service"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// FixGenerationWorker calls FixService to generate all eligible fixes for a merchant.
type FixGenerationWorker struct {
	river.WorkerDefaults[FixGenerationJobArgs]
	fixService *service.FixService
}

func NewFixGenerationWorker(fixService *service.FixService) *FixGenerationWorker {
	return &FixGenerationWorker{fixService: fixService}
}

func (w *FixGenerationWorker) Work(ctx context.Context, job *river.Job[FixGenerationJobArgs]) error {
	result, err := w.fixService.GenerateFixes(ctx, job.Args.MerchantID)
	if err != nil {
		return err
	}
	slog.Info("fix generation: complete",
		"merchant_id", job.Args.MerchantID,
		"total", result.TotalGenerated,
		"collection", result.CollectionFixes,
		"page", result.PageFixes,
		"schema", result.SchemaFixes,
	)
	return nil
}

// FixApplyWorker calls FixService to apply one approved fix to Shopify.
type FixApplyWorker struct {
	river.WorkerDefaults[FixApplyJobArgs]
	fixService *service.FixService
}

func NewFixApplyWorker(db *pgxpool.Pool, encKey []byte, riverClient *river.Client[pgx.Tx]) *FixApplyWorker {
	return &FixApplyWorker{
		fixService: service.NewFixService(db, encKey, nil, riverClient),
	}
}

func (w *FixApplyWorker) Work(ctx context.Context, job *river.Job[FixApplyJobArgs]) error {
	if err := w.fixService.ApplyFix(ctx, job.Args.MerchantID, job.Args.FixID); err != nil {
		return err
	}
	slog.Info("fix apply: complete", "merchant_id", job.Args.MerchantID, "fix_id", job.Args.FixID)
	return nil
}

// SchemaRebuildWorker rebuilds the shop schema metafield from current merchant data.
// Called when settings change (social links, brand name) so the live schema stays fresh.
// Kept as a standalone worker — schema rebuild reads Shopify + store directly and doesn't
// belong in FixService; it would be part of a future SchemaService (Phase 3+).
type SchemaRebuildWorker struct {
	river.WorkerDefaults[SchemaRebuildJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
}

func NewSchemaRebuildWorker(db *pgxpool.Pool, encKey []byte) *SchemaRebuildWorker {
	return &SchemaRebuildWorker{db: db, encryptionKey: encKey}
}

func (w *SchemaRebuildWorker) Work(ctx context.Context, job *river.Job[SchemaRebuildJobArgs]) error {
	merchant, err := store.GetMerchant(ctx, w.db, job.Args.MerchantID)
	if err != nil {
		return fmt.Errorf("schema rebuild: load merchant: %w", err)
	}
	if !merchant.Active {
		return nil
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, w.encryptionKey)
	if err != nil {
		return fmt.Errorf("schema rebuild: decrypt token: %w", err)
	}

	shopifyProducts, err := shopify.GetTopProducts(ctx, merchant.ShopDomain, token, 5, merchant.Category)
	if err != nil {
		slog.Warn("schema rebuild: could not fetch products (non-fatal)", "err", err)
	}
	schemaProducts := make([]fix.SchemaProduct, 0, len(shopifyProducts))
	for _, p := range shopifyProducts {
		schemaProducts = append(schemaProducts, fix.SchemaProduct{
			Handle:   p.Handle,
			Title:    p.Title,
			MinPrice: p.MinPrice,
			Currency: p.Currency,
			ImageURL: p.ImageURL,
		})
	}

	// Carry over the existing brand description from the applied schema fix.
	var brandDescription string
	if appliedFixes, err := store.GetFixes(ctx, w.db, merchant.ID, "applied"); err == nil {
		for _, af := range appliedFixes {
			if fix.FixType(af.FixType) != fix.FixSchema {
				continue
			}
			var gen struct {
				BrandDescription string `json:"brand_description"`
			}
			if json.Unmarshal(af.Generated, &gen) == nil {
				brandDescription = gen.BrandDescription
			}
			break
		}
	}

	// Pull FAQs: merchant-provided rows take priority over applied FAQ fix.
	var faqs []fix.SchemaFAQ
	if merchantFAQs, err := store.GetMerchantFAQs(ctx, w.db, merchant.ID); err == nil && len(merchantFAQs) > 0 {
		for _, mf := range merchantFAQs {
			faqs = append(faqs, fix.SchemaFAQ{Question: mf.Question, Answer: mf.Answer})
		}
	} else {
		if appliedFixes, err := store.GetFixes(ctx, w.db, merchant.ID, "applied"); err == nil {
			for _, af := range appliedFixes {
				if fix.FixType(af.FixType) != fix.FixFAQ {
					continue
				}
				var faqGen struct {
					FAQs []struct {
						Question string `json:"question"`
						Answer   string `json:"answer"`
					} `json:"faqs"`
				}
				if json.Unmarshal(af.Generated, &faqGen) == nil {
					limit := len(faqGen.FAQs)
					if limit > 5 {
						limit = 5
					}
					for _, q := range faqGen.FAQs[:limit] {
						faqs = append(faqs, fix.SchemaFAQ{Question: q.Question, Answer: q.Answer})
					}
				}
				break
			}
		}
	}

	// Include review data — injects aggregateRating into each Product node when available.
	var avgRating float64
	var reviewCount int
	if rs, err := store.GetMerchantReviewStatus(ctx, w.db, merchant.ID); err == nil && rs.SafeAvgRating() > 0 {
		avgRating = rs.SafeAvgRating()
		reviewCount = rs.TotalReviews
	}

	schemaJSON, err := fix.BuildSchema(fix.SchemaInput{
		BrandName:        merchant.BrandName,
		ShopDomain:       merchant.ShopDomain,
		BrandDescription: brandDescription,
		TopProducts:      schemaProducts,
		SocialLinks:      merchant.SocialLinks,
		FAQs:             faqs,
		AvgRating:        avgRating,
		ReviewCount:      reviewCount,
	})
	if err != nil {
		return fmt.Errorf("schema rebuild: build schema: %w", err)
	}

	if err := shopify.SetShopMetafield(
		ctx, merchant.ShopDomain, token,
		"geo_visibility", "schema_json", "json", schemaJSON,
	); err != nil {
		return fmt.Errorf("schema rebuild: set metafield: %w", err)
	}
	if err := shopify.GrantStorefrontMetafieldAccess(
		ctx, merchant.ShopDomain, token, "geo_visibility", "schema_json",
	); err != nil {
		slog.Warn("schema rebuild: storefront access grant failed (non-fatal)", "err", err)
	}

	if avgRating > 0 {
		_ = store.SaveMerchantReviews(ctx, w.db, merchant.ID, "", "", avgRating, reviewCount, true)
	}

	slog.Info("schema rebuild: complete",
		"merchant_id", job.Args.MerchantID,
		"review_injected", avgRating > 0,
	)
	return nil
}
