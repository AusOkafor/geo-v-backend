package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Product Audit ────────────────────────────────────────────────────────────

// ProductAudit mirrors a row from merchant_product_audit.
type ProductAudit struct {
	ID                      int64
	MerchantID              int64
	ProductID               string
	ProductHandle           string
	ProductTitle            string
	CurrentDescriptionWords int
	MissingMaterialInfo     bool
	MissingSizingInfo       bool
	MissingCareInstructions bool
	ImageCount              int
	ImagesMissingAltText    int
	CompletenessScore       float64
	NeedsAttention          bool
	MerchantFixedAt         *time.Time
}

// UpsertProductAudit inserts or updates a product audit record.
func UpsertProductAudit(ctx context.Context, db *pgxpool.Pool, a *ProductAudit) error {
	_, err := db.Exec(ctx, `
		INSERT INTO merchant_product_audit (
			merchant_id, product_id, product_handle, product_title,
			current_description_words,
			missing_material_info, missing_sizing_info, missing_care_instructions,
			image_count, images_missing_alt_text,
			completeness_score, needs_attention, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW())
		ON CONFLICT (merchant_id, product_id) DO UPDATE SET
			product_handle            = EXCLUDED.product_handle,
			product_title             = EXCLUDED.product_title,
			current_description_words = EXCLUDED.current_description_words,
			missing_material_info     = EXCLUDED.missing_material_info,
			missing_sizing_info       = EXCLUDED.missing_sizing_info,
			missing_care_instructions = EXCLUDED.missing_care_instructions,
			image_count               = EXCLUDED.image_count,
			images_missing_alt_text   = EXCLUDED.images_missing_alt_text,
			completeness_score        = EXCLUDED.completeness_score,
			needs_attention           = EXCLUDED.needs_attention,
			updated_at                = NOW()
	`, a.MerchantID, a.ProductID, a.ProductHandle, a.ProductTitle,
		a.CurrentDescriptionWords,
		a.MissingMaterialInfo, a.MissingSizingInfo, a.MissingCareInstructions,
		a.ImageCount, a.ImagesMissingAltText,
		a.CompletenessScore, a.NeedsAttention,
	)
	if err != nil {
		return fmt.Errorf("store.UpsertProductAudit: %w", err)
	}
	return nil
}

// GetProductsNeedingAttention returns products sorted by priority: empty first, then short, then incomplete.
func GetProductsNeedingAttention(ctx context.Context, db *pgxpool.Pool, merchantID int64, limit int) ([]ProductAudit, error) {
	rows, err := db.Query(ctx, `
		SELECT id, merchant_id, product_id, product_handle, product_title,
		       current_description_words,
		       missing_material_info, missing_sizing_info, missing_care_instructions,
		       image_count, images_missing_alt_text, completeness_score, needs_attention
		FROM merchant_product_audit
		WHERE merchant_id = $1 AND needs_attention = TRUE
		ORDER BY
			current_description_words ASC,
			completeness_score ASC NULLS LAST
		LIMIT $2
	`, merchantID, limit)
	if err != nil {
		return nil, fmt.Errorf("store.GetProductsNeedingAttention: %w", err)
	}
	defer rows.Close()

	var out []ProductAudit
	for rows.Next() {
		var a ProductAudit
		if err := rows.Scan(
			&a.ID, &a.MerchantID, &a.ProductID, &a.ProductHandle, &a.ProductTitle,
			&a.CurrentDescriptionWords,
			&a.MissingMaterialInfo, &a.MissingSizingInfo, &a.MissingCareInstructions,
			&a.ImageCount, &a.ImagesMissingAltText, &a.CompletenessScore, &a.NeedsAttention,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkProductFixed sets needs_attention=false when a webhook confirms the merchant updated the product.
func MarkProductFixed(ctx context.Context, db *pgxpool.Pool, merchantID int64, productID string) error {
	_, err := db.Exec(ctx, `
		UPDATE merchant_product_audit
		SET needs_attention = FALSE, merchant_fixed_at = NOW(), updated_at = NOW()
		WHERE merchant_id = $1 AND product_id = $2
	`, merchantID, productID)
	return err
}

// ─── Collection Audit ─────────────────────────────────────────────────────────

// CollectionAudit mirrors a row from merchant_collection_audit.
type CollectionAudit struct {
	ID                     int64
	MerchantID             int64
	CollectionID           string
	CollectionHandle       string
	CollectionTitle        string
	CurrentDescriptionWords int
	ProductCount           int
	AIDescriptionEligible  bool
	NeedsAttention         bool
}

// UpsertCollectionAudit inserts or updates a collection audit record.
func UpsertCollectionAudit(ctx context.Context, db *pgxpool.Pool, a *CollectionAudit) error {
	_, err := db.Exec(ctx, `
		INSERT INTO merchant_collection_audit (
			merchant_id, collection_id, collection_handle, collection_title,
			current_description_words, product_count,
			ai_description_eligible, needs_attention, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (merchant_id, collection_id) DO UPDATE SET
			collection_handle         = EXCLUDED.collection_handle,
			collection_title          = EXCLUDED.collection_title,
			current_description_words = EXCLUDED.current_description_words,
			product_count             = EXCLUDED.product_count,
			ai_description_eligible   = EXCLUDED.ai_description_eligible,
			needs_attention           = EXCLUDED.needs_attention,
			updated_at                = NOW()
	`, a.MerchantID, a.CollectionID, a.CollectionHandle, a.CollectionTitle,
		a.CurrentDescriptionWords, a.ProductCount,
		a.AIDescriptionEligible, a.NeedsAttention,
	)
	if err != nil {
		return fmt.Errorf("store.UpsertCollectionAudit: %w", err)
	}
	return nil
}

// GetCollectionsEligibleForFix returns collections that have no description and enough products
// to warrant an AI-generated description, ordered by product count descending.
func GetCollectionsEligibleForFix(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]CollectionAudit, error) {
	rows, err := db.Query(ctx, `
		SELECT id, merchant_id, collection_id, collection_handle, collection_title,
		       current_description_words, product_count, ai_description_eligible, needs_attention
		FROM merchant_collection_audit
		WHERE merchant_id = $1 AND ai_description_eligible = TRUE AND needs_attention = TRUE
		ORDER BY product_count DESC
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store.GetCollectionsEligibleForFix: %w", err)
	}
	defer rows.Close()

	var out []CollectionAudit
	for rows.Next() {
		var a CollectionAudit
		if err := rows.Scan(
			&a.ID, &a.MerchantID, &a.CollectionID, &a.CollectionHandle, &a.CollectionTitle,
			&a.CurrentDescriptionWords, &a.ProductCount, &a.AIDescriptionEligible, &a.NeedsAttention,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ─── Page Audit ───────────────────────────────────────────────────────────────

// PageAudit mirrors a row from merchant_page_audit.
type PageAudit struct {
	ID               int64
	MerchantID       int64
	PageID           string
	PageHandle       string
	PageTitle        string
	PageType         string
	WordCount        int
	FAQQuestionCount int
	AboutHasStory    bool
	AboutHasTeam     bool
	AIContentEligible bool
	NeedsAttention   bool
	IsPlaceholder    bool
}

// UpsertPageAudit inserts or updates a page audit record (keyed on merchant_id + page_type).
func UpsertPageAudit(ctx context.Context, db *pgxpool.Pool, a *PageAudit) error {
	_, err := db.Exec(ctx, `
		INSERT INTO merchant_page_audit (
			merchant_id, page_id, page_handle, page_title, page_type,
			word_count, faq_question_count, about_has_story, about_has_team,
			ai_content_eligible, needs_attention, is_placeholder, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NOW())
		ON CONFLICT (merchant_id, page_type) DO UPDATE SET
			page_id            = EXCLUDED.page_id,
			page_handle        = EXCLUDED.page_handle,
			page_title         = EXCLUDED.page_title,
			word_count         = EXCLUDED.word_count,
			faq_question_count = EXCLUDED.faq_question_count,
			about_has_story    = EXCLUDED.about_has_story,
			about_has_team     = EXCLUDED.about_has_team,
			ai_content_eligible = EXCLUDED.ai_content_eligible,
			needs_attention    = EXCLUDED.needs_attention,
			is_placeholder     = EXCLUDED.is_placeholder,
			updated_at         = NOW()
	`, a.MerchantID, nullableStr(a.PageID), nullableStr(a.PageHandle), a.PageTitle, a.PageType,
		a.WordCount, a.FAQQuestionCount, a.AboutHasStory, a.AboutHasTeam,
		a.AIContentEligible, a.NeedsAttention, a.IsPlaceholder,
	)
	if err != nil {
		return fmt.Errorf("store.UpsertPageAudit: %w", err)
	}
	return nil
}

// GetPagesEligibleForFix returns pages (including missing placeholders) that AI can generate.
func GetPagesEligibleForFix(ctx context.Context, db *pgxpool.Pool, merchantID int64) ([]PageAudit, error) {
	rows, err := db.Query(ctx, `
		SELECT id, merchant_id, COALESCE(page_id,''), COALESCE(page_handle,''),
		       page_title, page_type,
		       word_count, faq_question_count, about_has_story, about_has_team,
		       ai_content_eligible, needs_attention, is_placeholder
		FROM merchant_page_audit
		WHERE merchant_id = $1 AND ai_content_eligible = TRUE AND needs_attention = TRUE
		ORDER BY page_type
	`, merchantID)
	if err != nil {
		return nil, fmt.Errorf("store.GetPagesEligibleForFix: %w", err)
	}
	defer rows.Close()

	var out []PageAudit
	for rows.Next() {
		var a PageAudit
		if err := rows.Scan(
			&a.ID, &a.MerchantID, &a.PageID, &a.PageHandle, &a.PageTitle, &a.PageType,
			&a.WordCount, &a.FAQQuestionCount, &a.AboutHasStory, &a.AboutHasTeam,
			&a.AIContentEligible, &a.NeedsAttention, &a.IsPlaceholder,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ─── Audit Progress ───────────────────────────────────────────────────────────

// AuditProgress mirrors a row from merchant_audit_progress.
type AuditProgress struct {
	MerchantID                int64
	TotalProducts             int
	ProductsNeedingAttention  int
	ProductsFixed             int
	TotalCollections          int
	CollectionsNeedingAttention int
	CollectionsFixed          int
	TotalPagesAudited         int
	PagesNeedingAttention     int
	PagesFixed                int
	OverallCompletenessScore  float64
	LastCalculatedAt          time.Time
}

// UpsertAuditProgress saves the current progress snapshot.
func UpsertAuditProgress(ctx context.Context, db *pgxpool.Pool, p *AuditProgress) error {
	_, err := db.Exec(ctx, `
		INSERT INTO merchant_audit_progress (
			merchant_id,
			total_products, products_needing_attention, products_fixed,
			total_collections, collections_needing_attention, collections_fixed,
			total_pages_audited, pages_needing_attention, pages_fixed,
			overall_completeness_score,
			last_calculated_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW(),NOW())
		ON CONFLICT (merchant_id) DO UPDATE SET
			total_products                 = EXCLUDED.total_products,
			products_needing_attention     = EXCLUDED.products_needing_attention,
			products_fixed                 = EXCLUDED.products_fixed,
			total_collections              = EXCLUDED.total_collections,
			collections_needing_attention  = EXCLUDED.collections_needing_attention,
			collections_fixed              = EXCLUDED.collections_fixed,
			total_pages_audited            = EXCLUDED.total_pages_audited,
			pages_needing_attention        = EXCLUDED.pages_needing_attention,
			pages_fixed                    = EXCLUDED.pages_fixed,
			overall_completeness_score     = EXCLUDED.overall_completeness_score,
			last_calculated_at             = NOW(),
			updated_at                     = NOW()
	`, p.MerchantID,
		p.TotalProducts, p.ProductsNeedingAttention, p.ProductsFixed,
		p.TotalCollections, p.CollectionsNeedingAttention, p.CollectionsFixed,
		p.TotalPagesAudited, p.PagesNeedingAttention, p.PagesFixed,
		p.OverallCompletenessScore,
	)
	if err != nil {
		return fmt.Errorf("store.UpsertAuditProgress: %w", err)
	}
	return nil
}

// GetAuditProgress returns the latest progress snapshot for a merchant.
func GetAuditProgress(ctx context.Context, db *pgxpool.Pool, merchantID int64) (*AuditProgress, error) {
	var p AuditProgress
	err := db.QueryRow(ctx, `
		SELECT merchant_id,
		       total_products, products_needing_attention, products_fixed,
		       total_collections, collections_needing_attention, collections_fixed,
		       total_pages_audited, pages_needing_attention, pages_fixed,
		       overall_completeness_score, last_calculated_at
		FROM merchant_audit_progress
		WHERE merchant_id = $1
	`, merchantID).Scan(
		&p.MerchantID,
		&p.TotalProducts, &p.ProductsNeedingAttention, &p.ProductsFixed,
		&p.TotalCollections, &p.CollectionsNeedingAttention, &p.CollectionsFixed,
		&p.TotalPagesAudited, &p.PagesNeedingAttention, &p.PagesFixed,
		&p.OverallCompletenessScore, &p.LastCalculatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.GetAuditProgress: %w", err)
	}
	return &p, nil
}

func nullableStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
