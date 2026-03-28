package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/fix"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// FixGenerationWorker creates pending_fixes from scan gaps using Claude (or mock).
type FixGenerationWorker struct {
	river.WorkerDefaults[FixGenerationJobArgs]
	db        *pgxpool.Pool
	generator fix.Generatable
}

func NewFixGenerationWorker(db *pgxpool.Pool, generator fix.Generatable) *FixGenerationWorker {
	return &FixGenerationWorker{db: db, generator: generator}
}

func (w *FixGenerationWorker) Work(ctx context.Context, job *river.Job[FixGenerationJobArgs]) error {
	merchant, err := store.GetMerchant(ctx, w.db, job.Args.MerchantID)
	if err != nil {
		return err
	}
	if !merchant.Active {
		return nil
	}

	// Get latest visibility scores
	scores, err := store.GetVisibilityScores(ctx, w.db, merchant.ID, 30)
	if err != nil {
		return err
	}

	// Get competitor names from citation records
	comps, err := store.GetCompetitors(ctx, w.db, merchant.ID)
	if err != nil {
		comps = nil // non-fatal
	}
	competitorNames := make([]string, 0, len(comps))
	for _, c := range comps {
		competitorNames = append(competitorNames, c.Name)
	}

	// Load synced products so description fixes can target a real product GID
	products, err := store.GetProducts(ctx, w.db, merchant.ID)
	if err != nil {
		products = nil // non-fatal — fixes without a GID are still useful
	}

	// Find platforms with score < 15% and no pending fix of that type
	existingFixes, _ := store.GetFixes(ctx, w.db, merchant.ID, "pending")
	existingTypes := map[fix.FixType]bool{}
	existingTargets := map[string]bool{} // GIDs already targeted by a pending description fix
	for _, f := range existingFixes {
		existingTypes[fix.FixType(f.FixType)] = true
		if fix.FixType(f.FixType) == fix.FixDescription && f.TargetGID != "" {
			existingTargets[f.TargetGID] = true
		}
	}

	for _, score := range scores {
		if score.Score >= 15 {
			continue
		}

		// Generate one fix per gap type (avoid duplicates)
		for _, fixType := range []fix.FixType{fix.FixDescription, fix.FixFAQ, fix.FixSchema} {
			if fixType != fix.FixDescription && existingTypes[fixType] {
				continue
			}

			// For description fixes, pick a product that isn't already targeted
			targetGID := ""
			currentDesc := ""
			var tags []string
			if fixType == fix.FixDescription {
				var picked *store.Product
				for i := range products {
					if !existingTargets[products[i].ShopifyGID] {
						picked = &products[i]
						break
					}
				}
				if picked == nil {
					continue // all products already have a pending description fix
				}
				targetGID = picked.ShopifyGID
				currentDesc = picked.Description
				tags = picked.Tags
				existingTargets[targetGID] = true
			}

			result, err := w.generator.Generate(ctx, fix.GenerateInput{
				BrandName:          merchant.BrandName,
				Category:           merchant.Category,
				Competitors:        competitorNames,
				FixType:            fixType,
				CurrentDescription: currentDesc,
				Tags:               tags,
			})
			if err != nil {
				continue // skip failed generation
			}

			_, err = store.InsertFix(ctx, w.db, store.Fix{
				MerchantID:  merchant.ID,
				TargetGID:   targetGID,
				FixType:     string(fixType),
				Priority:    priorityForType(fixType),
				Title:       result.Title,
				Explanation: result.Explanation,
				Generated:   result.Generated,
				EstImpact:   fix.EstImpact(fixType),
			})
			if err != nil {
				return fmt.Errorf("fix gen: insert fix: %w", err)
			}
			existingTypes[fixType] = true
		}
	}

	return nil
}

// FixApplyWorker applies an approved fix directly to Shopify.
type FixApplyWorker struct {
	river.WorkerDefaults[FixApplyJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
}

func NewFixApplyWorker(db *pgxpool.Pool, encKey []byte) *FixApplyWorker {
	return &FixApplyWorker{db: db, encryptionKey: encKey}
}

func (w *FixApplyWorker) Work(ctx context.Context, job *river.Job[FixApplyJobArgs]) error {
	f, err := store.GetFix(ctx, w.db, job.Args.MerchantID, job.Args.FixID)
	if err != nil {
		return fmt.Errorf("fix apply: load fix: %w", err)
	}
	if f.Status != "approved" {
		return nil // already applied or rejected
	}

	merchant, err := store.GetMerchant(ctx, w.db, job.Args.MerchantID)
	if err != nil {
		return fmt.Errorf("fix apply: load merchant: %w", err)
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, w.encryptionKey)
	if err != nil {
		return fmt.Errorf("fix apply: decrypt token: %w", err)
	}

	var applyErr error
	switch fix.FixType(f.FixType) {
	case fix.FixDescription:
		// Extract new description from generated JSONB
		var gen struct {
			Description string `json:"description"`
		}
		if err := unmarshalJSON(f.Generated, &gen); err != nil || gen.Description == "" {
			applyErr = fmt.Errorf("fix apply: no description in generated content")
			break
		}
		applyErr = shopify.UpdateDescription(ctx, merchant.ShopDomain, token, f.TargetGID, gen.Description)
	default:
		// faq, schema, listing cannot be auto-applied via API — mark as manual
		// so the merchant knows the fix was acknowledged but needs manual action.
		slog.Info("fix apply: manual action required",
			"fix_id", f.ID, "fix_type", f.FixType, "merchant_id", job.Args.MerchantID)
		return store.SetFixStatus(ctx, w.db, f.ID, "manual")
	}

	if applyErr != nil {
		_ = store.SetFixStatus(ctx, w.db, f.ID, "failed")
		return applyErr
	}

	return store.SetFixStatus(ctx, w.db, f.ID, "applied")
}

func priorityForType(t fix.FixType) string {
	switch t {
	case fix.FixDescription, fix.FixFAQ:
		return "high"
	default:
		return "medium"
	}
}

func unmarshalJSON(data []byte, v any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty JSON")
	}
	return json.Unmarshal(data, v)
}
