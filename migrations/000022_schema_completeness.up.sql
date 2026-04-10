ALTER TABLE merchant_audit
    ADD COLUMN IF NOT EXISTS schema_completeness_score NUMERIC(4,3) NOT NULL DEFAULT 0;
