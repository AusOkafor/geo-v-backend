-- Add Google Merchant Center detection columns to merchant_audit.
-- Populated by the onboarding audit worker (and refresh audit).
ALTER TABLE merchant_audit
    ADD COLUMN IF NOT EXISTS google_merchant_center_connected BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS google_product_feed_active        BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN merchant_audit.google_merchant_center_connected IS 'True when Google & YouTube (Merchant Center) app is detected as installed';
COMMENT ON COLUMN merchant_audit.google_product_feed_active        IS 'True when the Google product feed appears to be syncing';
