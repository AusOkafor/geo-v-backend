ALTER TABLE merchants
    DROP COLUMN IF EXISTS brand_name,
    DROP COLUMN IF EXISTS category;

ALTER TABLE pending_fixes DROP CONSTRAINT IF EXISTS pending_fixes_status_check;
ALTER TABLE pending_fixes
    ADD CONSTRAINT pending_fixes_status_check
        CHECK (status IN ('pending','approved','rejected','applied','failed'));
