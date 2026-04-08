-- Per-product audit details: what's missing on each product.
-- Replaces the word-count-only aggregate in merchant_audit with actionable per-item data.
CREATE TABLE IF NOT EXISTS merchant_product_audit (
    id                         BIGSERIAL PRIMARY KEY,
    merchant_id                BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    product_id                 TEXT   NOT NULL, -- Shopify GID, e.g. gid://shopify/Product/123
    product_handle             TEXT   NOT NULL,
    product_title              TEXT   NOT NULL,

    -- Current content state
    current_description_words  INT  DEFAULT 0,

    -- Missing-element flags (detected by keyword scan)
    missing_material_info      BOOLEAN DEFAULT TRUE,
    missing_sizing_info        BOOLEAN DEFAULT TRUE,
    missing_care_instructions  BOOLEAN DEFAULT TRUE,

    -- Media
    image_count                INT DEFAULT 0,
    images_missing_alt_text    INT DEFAULT 0,

    -- Quality score 0.00–1.00
    completeness_score         DECIMAL(3,2),

    -- Workflow
    needs_attention            BOOLEAN DEFAULT TRUE,
    merchant_fixed_at          TIMESTAMP,  -- set by webhook when product is updated

    created_at  TIMESTAMP DEFAULT NOW(),
    updated_at  TIMESTAMP DEFAULT NOW(),

    UNIQUE(merchant_id, product_id)
);

CREATE INDEX idx_product_audit_merchant     ON merchant_product_audit(merchant_id);
CREATE INDEX idx_product_audit_attention    ON merchant_product_audit(merchant_id, needs_attention)
    WHERE needs_attention = TRUE;
