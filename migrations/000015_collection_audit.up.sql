-- Per-collection audit: AI can generate descriptions here (less brand-sensitive than products).
CREATE TABLE IF NOT EXISTS merchant_collection_audit (
    id                          BIGSERIAL PRIMARY KEY,
    merchant_id                 BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    collection_id               TEXT   NOT NULL, -- Shopify GID
    collection_handle           TEXT   NOT NULL,
    collection_title            TEXT   NOT NULL,

    -- Current state
    current_description_words   INT  DEFAULT 0,
    product_count               INT  DEFAULT 0,

    -- AI can propose a description for this collection
    ai_description_eligible     BOOLEAN DEFAULT FALSE,

    -- Workflow
    needs_attention             BOOLEAN DEFAULT TRUE,

    created_at  TIMESTAMP DEFAULT NOW(),
    updated_at  TIMESTAMP DEFAULT NOW(),

    UNIQUE(merchant_id, collection_id)
);

CREATE INDEX idx_collection_audit_merchant    ON merchant_collection_audit(merchant_id);
CREATE INDEX idx_collection_audit_eligible    ON merchant_collection_audit(merchant_id, ai_description_eligible)
    WHERE ai_description_eligible = TRUE;
