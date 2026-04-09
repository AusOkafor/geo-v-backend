ALTER TABLE merchant_audit
    DROP COLUMN IF EXISTS google_merchant_center_connected,
    DROP COLUMN IF EXISTS google_product_feed_active;
