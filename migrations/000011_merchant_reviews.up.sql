-- ============================================================
-- Review Schema Detector — store detected review app data
-- and whether review schema has been injected per merchant.
-- ============================================================

ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS review_app              TEXT,
    ADD COLUMN IF NOT EXISTS avg_rating              NUMERIC(3,2),
    ADD COLUMN IF NOT EXISTS total_reviews           INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS review_schema_injected  BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS reviews_last_scanned_at TIMESTAMPTZ;

-- Partial index for fast "merchants with reviews not yet injected" queries.
CREATE INDEX IF NOT EXISTS idx_merchants_reviews_pending
    ON merchants (id)
    WHERE review_app IS NOT NULL AND review_schema_injected = false;
