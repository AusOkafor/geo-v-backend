ALTER TABLE merchants
  DROP COLUMN IF EXISTS price_positioning,
  DROP COLUMN IF EXISTS unique_selling_point;
