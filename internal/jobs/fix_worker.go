package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/fix"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// blockedQuestionPhrases are patterns that indicate self-promotional FAQ questions.
// AI assistants penalise schemas containing these as low-trust SEO content.
var blockedQuestionPhrases = []string{
	"best brand", "top brand", "leading brand", "why choose", "why should i choose",
	"why is", "best jewelry", "top jewelry", "standout", "number one", "#1",
}

// validateFAQPairs checks generated FAQ content for patterns AI treats as low-trust.
// Returns an error describing the first violation found.
func validateFAQPairs(faqs []struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}, brandName string) error {
	for i, faq := range faqs {
		q := strings.ToLower(faq.Question)
		for _, blocked := range blockedQuestionPhrases {
			if strings.Contains(q, blocked) {
				return fmt.Errorf("FAQ %d: self-promotional question pattern %q — rewrite as a neutral buyer question", i+1, blocked)
			}
		}
		words := strings.Fields(faq.Answer)
		if len(words) < 5 {
			return fmt.Errorf("FAQ %d: answer too short (%d words, minimum 5)", i+1, len(words))
		}
		// Block answers that are only the brand name + filler with no substance
		answerWithoutBrand := strings.TrimSpace(strings.ReplaceAll(strings.ToLower(faq.Answer), strings.ToLower(brandName), ""))
		if len(strings.Fields(answerWithoutBrand)) < 3 {
			return fmt.Errorf("FAQ %d: answer contains no substance beyond brand name", i+1)
		}
	}
	return nil
}

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

	// Load top query gaps from this merchant's actual scan data.
	// These are the queries AI was asked where the merchant did NOT appear
	// but competitors did — the most concrete signal of what content is missing.
	queryGaps, _ := store.GetQueryGaps(ctx, w.db, merchant.ID)
	missedQueries := make([]string, 0, len(queryGaps))
	for _, g := range queryGaps {
		missedQueries = append(missedQueries, g.Query)
	}

	// If no visibility scores exist yet (first scan, aggregation may not have run),
	// treat all platforms as score=0 so fixes are still generated.
	if len(scores) == 0 {
		scores = []store.VisibilityScore{
			{Platform: "chatgpt", Score: 0},
			{Platform: "perplexity", Score: 0},
			{Platform: "gemini", Score: 0},
		}
	}

	// Check which fix types already exist (any status except rejected)
	existingFixes, _ := store.GetFixes(ctx, w.db, merchant.ID, "")
	existingTypes := map[fix.FixType]bool{}
	existingTargets := map[string]bool{} // GIDs already targeted by a pending description fix
	for _, f := range existingFixes {
		if f.Status == "rejected" {
			continue
		}
		existingTypes[fix.FixType(f.FixType)] = true
		if fix.FixType(f.FixType) == fix.FixDescription && f.TargetGID != "" {
			existingTargets[f.TargetGID] = true
		}
	}

	// Determine if any platform is below threshold
	anyLowScore := false
	for _, s := range scores {
		if s.Score < 80 {
			anyLowScore = true
			break
		}
	}
	if !anyLowScore {
		return nil
	}

	// Fix generation order: structure first (schema → faq), then content (description).
	// Structure fixes are the foundation — without them, content fixes have less impact.
	// Layer 1: Schema — lets AI parse the catalog structure
	if !existingTypes[fix.FixSchema] {
		result, err := w.generator.Generate(ctx, fix.GenerateInput{
			BrandName:  merchant.BrandName,
			Category:   merchant.Category,
			Competitors: competitorNames,
			FixType:    fix.FixSchema,
			QueryGaps:  missedQueries,
		})
		if err == nil {
			_, _ = store.InsertFix(ctx, w.db, store.Fix{
				MerchantID:  merchant.ID,
				FixType:     string(fix.FixSchema),
				FixLayer:    "structure",
				Priority:    "medium",
				Title:       result.Title,
				Explanation: result.Explanation,
				Generated:   result.Generated,
				EstImpact:   fix.EstImpact(fix.FixSchema),
			})
			existingTypes[fix.FixSchema] = true
		}
	}

	// Layer 1: FAQ — matches the exact queries AI is asked
	if !existingTypes[fix.FixFAQ] {
		result, err := w.generator.Generate(ctx, fix.GenerateInput{
			BrandName:  merchant.BrandName,
			Category:   merchant.Category,
			Competitors: competitorNames,
			FixType:    fix.FixFAQ,
			QueryGaps:  missedQueries,
		})
		if err == nil {
			_, err = store.InsertFix(ctx, w.db, store.Fix{
				MerchantID:  merchant.ID,
				FixType:     string(fix.FixFAQ),
				FixLayer:    "structure",
				Priority:    "high",
				Title:       result.Title,
				Explanation: result.Explanation,
				Generated:   result.Generated,
				EstImpact:   fix.EstImpact(fix.FixFAQ),
			})
			if err != nil {
				return fmt.Errorf("fix gen: insert faq fix: %w", err)
			}
			existingTypes[fix.FixFAQ] = true
		}
	}

	// Layer 2: Description — content targeting specific missed queries
	var picked *store.Product
	for i := range products {
		if !existingTargets[products[i].ShopifyGID] {
			picked = &products[i]
			break
		}
	}
	if picked != nil && !existingTypes[fix.FixDescription] {
		result, err := w.generator.Generate(ctx, fix.GenerateInput{
			BrandName:          merchant.BrandName,
			Category:           merchant.Category,
			Competitors:        competitorNames,
			FixType:            fix.FixDescription,
			CurrentDescription: picked.Description,
			Tags:               picked.Tags,
			QueryGaps:          missedQueries,
		})
		if err == nil {
			_, err = store.InsertFix(ctx, w.db, store.Fix{
				MerchantID:  merchant.ID,
				TargetGID:   picked.ShopifyGID,
				FixType:     string(fix.FixDescription),
				FixLayer:    "content",
				Priority:    "high",
				Title:       result.Title,
				Explanation: result.Explanation,
				Generated:   result.Generated,
				EstImpact:   fix.EstImpact(fix.FixDescription),
			})
			if err != nil {
				return fmt.Errorf("fix gen: insert description fix: %w", err)
			}
		}
	}

	return nil
}

// FixApplyWorker applies an approved fix directly to Shopify.
type FixApplyWorker struct {
	river.WorkerDefaults[FixApplyJobArgs]
	db            *pgxpool.Pool
	encryptionKey []byte
	riverClient   *river.Client[pgx.Tx]
}

func NewFixApplyWorker(db *pgxpool.Pool, encKey []byte, riverClient *river.Client[pgx.Tx]) *FixApplyWorker {
	return &FixApplyWorker{db: db, encryptionKey: encKey, riverClient: riverClient}
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
		// Extract new description from generated JSONB and push via Shopify product API.
		var gen struct {
			Description string `json:"description"`
		}
		if err := unmarshalJSON(f.Generated, &gen); err != nil || gen.Description == "" {
			applyErr = fmt.Errorf("fix apply: no description in generated content")
			break
		}
		applyErr = shopify.UpdateDescription(ctx, merchant.ShopDomain, token, f.TargetGID, gen.Description)

	case fix.FixSchema:
		// Build schema programmatically from real Shopify data.
		// AI only contributes the brand description text — all structural fields
		// (URLs, prices, product handles) come from the Shopify API.
		var gen struct {
			BrandDescription string `json:"brand_description"`
		}
		_ = unmarshalJSON(f.Generated, &gen) // non-fatal if missing — description is optional

		shopifyProducts, err := shopify.GetTopProducts(ctx, merchant.ShopDomain, token, 5, merchant.Category)
		if err != nil {
			slog.Warn("fix apply: could not fetch products for schema (non-fatal)", "err", err)
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

		// Pull applied FAQ fix to include Q&A pairs in FAQPage schema entity.
		var faqs []fix.SchemaFAQ
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

		schemaJSON, err := fix.BuildSchema(fix.SchemaInput{
			BrandName:        merchant.BrandName,
			ShopDomain:       merchant.ShopDomain,
			BrandDescription: gen.BrandDescription,
			TopProducts:      schemaProducts,
			SocialLinks:      merchant.SocialLinks,
			FAQs:             faqs,
		})
		if err != nil {
			applyErr = fmt.Errorf("fix apply: build schema: %w", err)
			break
		}

		if err := shopify.SetShopMetafield(
			ctx, merchant.ShopDomain, token,
			"geo_visibility", "schema_json", "json", schemaJSON,
		); err != nil {
			applyErr = fmt.Errorf("fix apply: set schema metafield: %w", err)
			break
		}
		// Grant storefront read access so the Liquid theme extension can read it.
		// Non-fatal if this fails — merchant can still enable manually.
		if err := shopify.GrantStorefrontMetafieldAccess(
			ctx, merchant.ShopDomain, token, "geo_visibility", "schema_json",
		); err != nil {
			slog.Warn("fix apply: storefront metafield access grant failed (non-fatal)",
				"fix_id", f.ID, "err", err)
		}

	case fix.FixFAQ:
		// Validate FAQ content before embedding in schema.
		// Self-promotional or thin answers are flagged as low-trust by AI assistants.
		var faqGen struct {
			FAQs []struct {
				Question string `json:"question"`
				Answer   string `json:"answer"`
			} `json:"faqs"`
		}
		if err := json.Unmarshal(f.Generated, &faqGen); err != nil {
			_ = store.SetFixStatus(ctx, w.db, f.ID, "failed")
			return fmt.Errorf("fix apply: FAQ parse: %w", err)
		}
		if err := validateFAQPairs(faqGen.FAQs, merchant.BrandName); err != nil {
			slog.Warn("fix apply: FAQ validation failed — marking failed so merchant can regenerate",
				"fix_id", f.ID, "err", err)
			return store.SetFixStatus(ctx, w.db, f.ID, "failed")
		}
		// FAQ Q&A pairs are embedded into the schema as a FAQPage entity.
		// Mark as applied and rebuild schema so the FAQPage appears in @graph immediately.
		if err := store.SetFixStatus(ctx, w.db, f.ID, "applied"); err != nil {
			return err
		}
		_, _ = w.riverClient.Insert(ctx, SchemaRebuildJobArgs{MerchantID: job.Args.MerchantID}, nil)
		return nil

	default:
		// listing cannot be auto-applied — mark as manual.
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

// SchemaRebuildWorker rebuilds the shop schema metafield from current merchant data.
// Called when settings change (social links, brand name) so the live schema stays fresh.
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

	// Pull applied FAQ fix.
	var faqs []fix.SchemaFAQ
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

	schemaJSON, err := fix.BuildSchema(fix.SchemaInput{
		BrandName:        merchant.BrandName,
		ShopDomain:       merchant.ShopDomain,
		BrandDescription: brandDescription,
		TopProducts:      schemaProducts,
		SocialLinks:      merchant.SocialLinks,
		FAQs:             faqs,
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

	slog.Info("schema rebuild: complete", "merchant_id", job.Args.MerchantID)
	return nil
}

func unmarshalJSON(data []byte, v any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty JSON")
	}
	return json.Unmarshal(data, v)
}
