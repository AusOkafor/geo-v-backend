-- Stores the extracted API key / app key for the detected review app.
-- Used to call the review app's public API directly when Shopify metafields
-- are not available (e.g. Yotpo and Judge.me on free plans).
ALTER TABLE merchants
    ADD COLUMN IF NOT EXISTS review_app_key TEXT;
