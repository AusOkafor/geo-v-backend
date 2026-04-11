ALTER TABLE merchants
  ADD COLUMN IF NOT EXISTS price_positioning    TEXT,
  ADD COLUMN IF NOT EXISTS unique_selling_point TEXT;
