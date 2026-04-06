DROP INDEX IF EXISTS idx_merchants_reviews_pending;

ALTER TABLE merchants
    DROP COLUMN IF EXISTS reviews_last_scanned_at,
    DROP COLUMN IF EXISTS review_schema_injected,
    DROP COLUMN IF EXISTS total_reviews,
    DROP COLUMN IF EXISTS avg_rating,
    DROP COLUMN IF EXISTS review_app;
