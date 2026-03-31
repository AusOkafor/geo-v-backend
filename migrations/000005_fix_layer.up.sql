-- Add fix_layer to pending_fixes so merchants understand the sequence:
-- structure (schema/faq) → content (description/listing) → authority (external)
ALTER TABLE pending_fixes ADD COLUMN IF NOT EXISTS fix_layer TEXT NOT NULL DEFAULT 'content';

-- Back-fill existing rows based on fix_type
UPDATE pending_fixes SET fix_layer = 'structure' WHERE fix_type IN ('schema', 'faq');
UPDATE pending_fixes SET fix_layer = 'authority' WHERE fix_type = 'listing';
-- description stays 'content' (default)
