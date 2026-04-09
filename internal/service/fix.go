package service

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

// GenerateFixesResult contains counts of fixes produced in a single run.
type GenerateFixesResult struct {
	TotalGenerated  int
	CollectionFixes int
	PageFixes       int
	SchemaFixes     int
}

// FixService handles all fix-related business logic — generation, application,
// and status management. Workers and handlers call the service; neither talks to
// the store or Shopify directly for fix operations.
type FixService struct {
	db            *pgxpool.Pool
	encryptionKey []byte               // required only by ApplyFix (worker path)
	generator     fix.Generatable      // required only by GenerateFixes (worker path)
	riverClient   *river.Client[pgx.Tx] // required by ApproveFix and ApplyFix
}

func NewFixService(
	db *pgxpool.Pool,
	encKey []byte,
	generator fix.Generatable,
	riverClient *river.Client[pgx.Tx],
) *FixService {
	return &FixService{
		db:            db,
		encryptionKey: encKey,
		generator:     generator,
		riverClient:   riverClient,
	}
}

// ─── Read methods ─────────────────────────────────────────────────────────────

// GetFixes returns fixes for a merchant, optionally filtered by status.
func (s *FixService) GetFixes(ctx context.Context, merchantID int64, status string) ([]store.Fix, error) {
	return store.GetFixes(ctx, s.db, merchantID, status)
}

// GetFix returns a single fix scoped to the merchant.
func (s *FixService) GetFix(ctx context.Context, merchantID, fixID int64) (*store.Fix, error) {
	return store.GetFix(ctx, s.db, merchantID, fixID)
}

// ─── Status actions ───────────────────────────────────────────────────────────

// ApproveFix marks the fix as approved and enqueues the apply worker.
func (s *FixService) ApproveFix(ctx context.Context, merchantID, fixID int64) error {
	if err := store.ApproveFix(ctx, s.db, merchantID, fixID); err != nil {
		return err
	}
	if s.riverClient != nil {
		_, _ = s.riverClient.Insert(ctx, applyJobArgs{MerchantID: merchantID, FixID: fixID}, fixApplyInsertOpts)
	}
	return nil
}

// RejectFix marks the fix as rejected.
func (s *FixService) RejectFix(ctx context.Context, merchantID, fixID int64) error {
	return store.RejectFix(ctx, s.db, merchantID, fixID)
}

// ─── Fix generation ───────────────────────────────────────────────────────────

// GenerateFixes generates all eligible fix types for a merchant and returns counts.
// This is the main entry point called by FixGenerationWorker.
func (s *FixService) GenerateFixes(ctx context.Context, merchantID int64) (*GenerateFixesResult, error) {
	merchant, err := store.GetMerchant(ctx, s.db, merchantID)
	if err != nil {
		return nil, err
	}
	if !merchant.Active {
		return &GenerateFixesResult{}, nil
	}

	// Get latest visibility scores
	scores, err := store.GetVisibilityScores(ctx, s.db, merchant.ID, 30)
	if err != nil {
		return nil, err
	}

	// Competitor names from citation records
	comps, err := store.GetCompetitors(ctx, s.db, merchant.ID)
	if err != nil {
		comps = nil // non-fatal
	}
	competitorNames := make([]string, 0, len(comps))
	for _, c := range comps {
		competitorNames = append(competitorNames, c.Name)
	}

	// Query gaps from actual scan data
	queryGaps, _ := store.GetQueryGaps(ctx, s.db, merchant.ID)
	missedQueries := make([]string, 0, len(queryGaps))
	for _, g := range queryGaps {
		missedQueries = append(missedQueries, g.Query)
	}

	// If no visibility scores yet, treat all platforms as score=0 so fixes are still generated.
	if len(scores) == 0 {
		scores = []store.VisibilityScore{
			{Platform: "chatgpt", Score: 0},
			{Platform: "perplexity", Score: 0},
			{Platform: "gemini", Score: 0},
		}
	}

	// Load store audit — nil if audit hasn't run yet.
	audit, _ := store.GetMerchantAudit(ctx, s.db, merchant.ID)

	// Check which fix types already have a pending/applied fix (skip re-generating).
	existingFixes, _ := store.GetFixes(ctx, s.db, merchant.ID, "")
	existingTypes := map[fix.FixType]bool{}
	existingCollectionTargets := map[string]bool{}
	for _, f := range existingFixes {
		if f.Status == "rejected" {
			continue
		}
		existingTypes[fix.FixType(f.FixType)] = true
		if fix.FixType(f.FixType) == fix.FixCollectionDescription && f.TargetGID != "" {
			existingCollectionTargets[f.TargetGID] = true
		}
	}

	// Only generate fixes if at least one platform score is below threshold.
	anyLowScore := false
	for _, sc := range scores {
		if sc.Score < 80 {
			anyLowScore = true
			break
		}
	}
	if !anyLowScore {
		return &GenerateFixesResult{}, nil
	}

	result := &GenerateFixesResult{}

	// ── Schema ────────────────────────────────────────────────────────────────
	schemaAlreadyLive := audit != nil && audit.SchemaLive
	schemaQualityOK := audit != nil && audit.SchemaCompletenessScore >= 0.8
	if !existingTypes[fix.FixSchema] && (!schemaAlreadyLive || !schemaQualityOK) {
		r, err := s.generator.Generate(ctx, fix.GenerateInput{
			BrandName:   merchant.BrandName,
			Category:    merchant.Category,
			Competitors: competitorNames,
			FixType:     fix.FixSchema,
			QueryGaps:   missedQueries,
		})
		if err == nil {
			_, _ = store.InsertFix(ctx, s.db, store.Fix{
				MerchantID:  merchant.ID,
				FixType:     string(fix.FixSchema),
				FixLayer:    "structure",
				Priority:    "medium",
				Title:       r.Title,
				Explanation: r.Explanation,
				Generated:   r.Generated,
				EstImpact:   fix.EstImpact(fix.FixSchema),
			})
			existingTypes[fix.FixSchema] = true
			result.SchemaFixes++
			result.TotalGenerated++
		}
	}

	// ── FAQ action item ───────────────────────────────────────────────────────
	if !existingTypes[fix.FixFAQ] {
		merchantFAQs, _ := store.GetMerchantFAQs(ctx, s.db, merchant.ID)
		hasFAQPage := audit != nil && audit.HasFAQPage
		if len(merchantFAQs) == 0 && !hasFAQPage {
			actionPayload, _ := json.Marshal(map[string]string{
				"action": "Add your store FAQs in Settings → FAQs. FAQs help AI assistants answer buyer questions about your shipping, returns, materials, and sizing — improving citation rates significantly.",
			})
			_, err := store.InsertFix(ctx, s.db, store.Fix{
				MerchantID:  merchant.ID,
				FixType:     string(fix.FixFAQ),
				FixLayer:    "structure",
				Priority:    "high",
				Title:       "Add store FAQs to improve AI citation rates",
				Explanation: "AI assistants frequently answer questions about shipping, returns, materials, and sizing. Stores with factual FAQ schema are cited up to 2× more often. Go to Settings → FAQs to add your real store policies.",
				Generated:   actionPayload,
				EstImpact:   fix.EstImpact(fix.FixFAQ),
			})
			if err != nil {
				return nil, fmt.Errorf("fix service: insert faq action fix: %w", err)
			}
			existingTypes[fix.FixFAQ] = true
			result.TotalGenerated++
		}
	}

	// ── Collection descriptions ───────────────────────────────────────────────
	collections, _ := store.GetCollectionsEligibleForFix(ctx, s.db, merchant.ID)
	const maxCollectionFixes = 3
	for _, c := range collections {
		if result.CollectionFixes >= maxCollectionFixes {
			break
		}
		if existingCollectionTargets[c.CollectionID] {
			continue
		}
		r, err := s.generator.Generate(ctx, fix.GenerateInput{
			BrandName:              merchant.BrandName,
			Category:               merchant.Category,
			Competitors:            competitorNames,
			FixType:                fix.FixCollectionDescription,
			CollectionTitle:        c.CollectionTitle,
			CollectionProductCount: c.ProductCount,
			QueryGaps:              missedQueries,
		})
		if err != nil {
			slog.Warn("fix service: collection description generation failed (non-fatal)",
				"merchant_id", merchant.ID, "collection_id", c.CollectionID, "err", err)
			continue
		}
		_, err = store.InsertFix(ctx, s.db, store.Fix{
			MerchantID:  merchant.ID,
			TargetGID:   c.CollectionID,
			FixType:     string(fix.FixCollectionDescription),
			FixLayer:    "content",
			Priority:    "high",
			Title:       r.Title,
			Explanation: r.Explanation,
			Generated:   r.Generated,
			EstImpact:   fix.EstImpact(fix.FixCollectionDescription),
		})
		if err != nil {
			return nil, fmt.Errorf("fix service: insert collection fix for %s: %w", c.CollectionID, err)
		}
		existingCollectionTargets[c.CollectionID] = true
		result.CollectionFixes++
		result.TotalGenerated++
	}

	// ── Google Merchant Center ────────────────────────────────────────────────
	if !existingTypes[fix.FixMerchantCenter] {
		if audit == nil || !audit.GoogleMerchantCenterConnected {
			generated, _ := json.Marshal(map[string]string{
				"action_url":  "https://apps.shopify.com/google",
				"app_name":    "Google & YouTube",
				"description": "Install the Google & YouTube app from the Shopify App Store, connect your Google account, and set up a product feed. Once connected, Gemini can verify your business and access accurate product data.",
			})
			_, err := store.InsertFix(ctx, s.db, store.Fix{
				MerchantID:  merchant.ID,
				FixType:     string(fix.FixMerchantCenter),
				FixLayer:    "authority",
				Priority:    "high",
				Title:       "Connect Google Merchant Center",
				Explanation: "Gemini (Google's AI) uses Merchant Center to verify that your store is a legitimate business. Connected stores are cited more frequently in Google AI Overviews and shopping recommendations. Setup takes under 15 minutes.",
				Generated:   generated,
				EstImpact:   fix.EstImpact(fix.FixMerchantCenter),
			})
			if err != nil {
				return nil, fmt.Errorf("fix service: insert merchant_center fix: %w", err)
			}
			existingTypes[fix.FixMerchantCenter] = true
			result.TotalGenerated++
		}
	}

	// ── Page content: About, Size Guide ──────────────────────────────────────
	pageCandidates, _ := store.GetPagesEligibleForFix(ctx, s.db, merchant.ID)
	for _, pg := range pageCandidates {
		var ft fix.FixType
		switch pg.PageType {
		case "about":
			ft = fix.FixAboutPage
		case "size_guide":
			ft = fix.FixSizeGuide
		default:
			continue // faq handled above via action item
		}
		if existingTypes[ft] {
			continue
		}
		r, err := s.generator.Generate(ctx, fix.GenerateInput{
			BrandName: merchant.BrandName,
			Category:  merchant.Category,
			FixType:   ft,
			PageType:  pg.PageType,
			QueryGaps: missedQueries,
		})
		if err != nil {
			slog.Warn("fix service: page content generation failed (non-fatal)",
				"merchant_id", merchant.ID, "page_type", pg.PageType, "err", err)
			continue
		}
		_, err = store.InsertFix(ctx, s.db, store.Fix{
			MerchantID:  merchant.ID,
			FixType:     string(ft),
			FixLayer:    "content",
			Priority:    "medium",
			Title:       r.Title,
			Explanation: r.Explanation,
			Generated:   r.Generated,
			EstImpact:   fix.EstImpact(ft),
		})
		if err != nil {
			return nil, fmt.Errorf("fix service: insert %s fix: %w", ft, err)
		}
		existingTypes[ft] = true
		result.PageFixes++
		result.TotalGenerated++
	}

	return result, nil
}

// ─── Fix application ──────────────────────────────────────────────────────────

// ApplyFix applies an approved fix directly to Shopify and marks it applied.
// Called by FixApplyWorker; should not be called directly from HTTP handlers.
func (s *FixService) ApplyFix(ctx context.Context, merchantID, fixID int64) error {
	f, err := store.GetFix(ctx, s.db, merchantID, fixID)
	if err != nil {
		return fmt.Errorf("fix service: load fix: %w", err)
	}
	if f.Status != "approved" {
		return nil // already applied or rejected
	}

	merchant, err := store.GetMerchant(ctx, s.db, merchantID)
	if err != nil {
		return fmt.Errorf("fix service: load merchant: %w", err)
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("fix service: decrypt token: %w", err)
	}

	var applyErr error
	switch fix.FixType(f.FixType) {
	case fix.FixDescription:
		var gen struct {
			Description string `json:"description"`
		}
		if err := unmarshalFixJSON(f.Generated, &gen); err != nil || gen.Description == "" {
			applyErr = fmt.Errorf("fix service: no description in generated content")
			break
		}
		applyErr = shopify.UpdateDescription(ctx, merchant.ShopDomain, token, f.TargetGID, gen.Description)

	case fix.FixSchema:
		var gen struct {
			BrandDescription string `json:"brand_description"`
		}
		_ = unmarshalFixJSON(f.Generated, &gen)

		shopifyProducts, err := shopify.GetTopProducts(ctx, merchant.ShopDomain, token, 5, merchant.Category)
		if err != nil {
			slog.Warn("fix service: could not fetch products for schema (non-fatal)", "err", err)
		}
		schemaProducts := make([]fix.SchemaProduct, 0, len(shopifyProducts))
		for _, p := range shopifyProducts {
			schemaProducts = append(schemaProducts, fix.SchemaProduct{
				Handle:      p.Handle,
				Title:       p.Title,
				Description: p.Description,
				MinPrice:    p.MinPrice,
				Currency:    p.Currency,
				ImageURL:    p.ImageURL,
			})
		}

		var faqs []fix.SchemaFAQ
		if appliedFixes, err := store.GetFixes(ctx, s.db, merchant.ID, "applied"); err == nil {
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
			applyErr = fmt.Errorf("fix service: build schema: %w", err)
			break
		}
		if err := shopify.SetShopMetafield(
			ctx, merchant.ShopDomain, token,
			"geo_visibility", "schema_json", "json", schemaJSON,
		); err != nil {
			applyErr = fmt.Errorf("fix service: set schema metafield: %w", err)
			break
		}
		if err := shopify.GrantStorefrontMetafieldAccess(
			ctx, merchant.ShopDomain, token, "geo_visibility", "schema_json",
		); err != nil {
			slog.Warn("fix service: storefront metafield access grant failed (non-fatal)",
				"fix_id", f.ID, "err", err)
		}

	case fix.FixFAQ:
		var actionCheck struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(f.Generated, &actionCheck); err == nil && actionCheck.Action != "" {
			// Action-item fix — mark applied, trigger schema rebuild.
			if err := store.SetFixStatus(ctx, s.db, f.ID, "applied"); err != nil {
				return err
			}
			if s.riverClient != nil {
				_, _ = s.riverClient.Insert(ctx, schemaRebuildJobArgs{MerchantID: merchantID}, nil)
			}
			return nil
		}
		// Legacy path: fix contains generated FAQ pairs — validate and embed.
		var faqGen struct {
			FAQs []struct {
				Question string `json:"question"`
				Answer   string `json:"answer"`
			} `json:"faqs"`
		}
		if err := json.Unmarshal(f.Generated, &faqGen); err != nil {
			_ = store.SetFixStatus(ctx, s.db, f.ID, "failed")
			return fmt.Errorf("fix service: FAQ parse: %w", err)
		}
		if err := validateFAQPairs(faqGen.FAQs, merchant.BrandName); err != nil {
			slog.Warn("fix service: FAQ validation failed — marking failed so merchant can regenerate",
				"fix_id", f.ID, "err", err)
			return store.SetFixStatus(ctx, s.db, f.ID, "failed")
		}
		if err := store.SetFixStatus(ctx, s.db, f.ID, "applied"); err != nil {
			return err
		}
		if s.riverClient != nil {
			_, _ = s.riverClient.Insert(ctx, schemaRebuildJobArgs{MerchantID: merchantID}, schemaRebuildInsertOpts)
		}
		return nil

	case fix.FixCollectionDescription:
		var gen struct {
			Description string `json:"description"`
		}
		if err := unmarshalFixJSON(f.Generated, &gen); err != nil || gen.Description == "" {
			applyErr = fmt.Errorf("fix service: no description in collection fix generated content")
			break
		}
		if f.TargetGID == "" {
			applyErr = fmt.Errorf("fix service: collection_description fix missing target_gid")
			break
		}
		applyErr = shopify.UpdateCollectionDescription(ctx, merchant.ShopDomain, token, f.TargetGID, gen.Description)

	case fix.FixAboutPage, fix.FixSizeGuide:
		var gen struct {
			Content string `json:"content"`
		}
		if err := unmarshalFixJSON(f.Generated, &gen); err != nil || gen.Content == "" {
			applyErr = fmt.Errorf("fix service: no content in page fix generated content")
			break
		}
		pageTitle := "About Us"
		if fix.FixType(f.FixType) == fix.FixSizeGuide {
			pageTitle = "Size Guide"
		}
		if f.TargetGID != "" {
			applyErr = shopify.UpdatePage(ctx, merchant.ShopDomain, token, f.TargetGID, gen.Content)
		} else {
			_, applyErr = shopify.CreatePage(ctx, merchant.ShopDomain, token, pageTitle, gen.Content)
		}

	default:
		slog.Info("fix service: manual action required",
			"fix_id", f.ID, "fix_type", f.FixType, "merchant_id", merchantID)
		return store.SetFixStatus(ctx, s.db, f.ID, "manual")
	}

	if applyErr != nil {
		_ = store.SetFixStatus(ctx, s.db, f.ID, "failed")
		return applyErr
	}
	return store.SetFixStatus(ctx, s.db, f.ID, "applied")
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// validateFAQPairs checks generated FAQ content for patterns AI treats as low-trust.
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
		answerWithoutBrand := strings.TrimSpace(strings.ReplaceAll(strings.ToLower(faq.Answer), strings.ToLower(brandName), ""))
		if len(strings.Fields(answerWithoutBrand)) < 3 {
			return fmt.Errorf("FAQ %d: answer contains no substance beyond brand name", i+1)
		}
	}
	return nil
}

func unmarshalFixJSON(data []byte, v any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty JSON")
	}
	return json.Unmarshal(data, v)
}

// applyJobArgs and schemaRebuildJobArgs mirror the River job arg types from the jobs package.
// We cannot import jobs here (jobs → service → jobs would be a cycle), so we re-declare the
// minimal types and pass InsertOpts explicitly to route them to the correct queues.
type applyJobArgs struct {
	MerchantID int64 `json:"merchant_id"`
	FixID      int64 `json:"fix_id"`
}

func (applyJobArgs) Kind() string { return "fix_apply" }

var fixApplyInsertOpts = &river.InsertOpts{Queue: "apply", MaxAttempts: 5}

type schemaRebuildJobArgs struct {
	MerchantID int64 `json:"merchant_id"`
}

func (schemaRebuildJobArgs) Kind() string { return "schema_rebuild" }

var schemaRebuildInsertOpts = &river.InsertOpts{Queue: "apply", MaxAttempts: 3}
