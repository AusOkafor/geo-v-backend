-- Add brand_name and category to merchants (used for query generation and fix prompts).
ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS brand_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS category   TEXT NOT NULL DEFAULT '';

-- Allow 'manual' status for fixes that require manual merchant action
-- (faq, schema, listing fixes cannot be auto-applied via the Shopify API).
ALTER TABLE pending_fixes DROP CONSTRAINT IF EXISTS pending_fixes_status_check;
ALTER TABLE pending_fixes
    ADD CONSTRAINT pending_fixes_status_check
        CHECK (status IN ('pending','approved','rejected','applied','failed','manual'));
