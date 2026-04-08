-- Aggregate progress snapshot recalculated after each audit run.
CREATE TABLE IF NOT EXISTS merchant_audit_progress (
    merchant_id                     BIGINT PRIMARY KEY REFERENCES merchants(id) ON DELETE CASCADE,

    total_products                  INT DEFAULT 0,
    products_needing_attention      INT DEFAULT 0,
    products_fixed                  INT DEFAULT 0,

    total_collections               INT DEFAULT 0,
    collections_needing_attention   INT DEFAULT 0,
    collections_fixed               INT DEFAULT 0,

    total_pages_audited             INT DEFAULT 0,
    pages_needing_attention         INT DEFAULT 0,
    pages_fixed                     INT DEFAULT 0,

    overall_completeness_score      DECIMAL(5,2) DEFAULT 0,

    last_calculated_at  TIMESTAMP DEFAULT NOW(),
    updated_at          TIMESTAMP DEFAULT NOW()
);
