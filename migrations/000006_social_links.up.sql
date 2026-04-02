ALTER TABLE merchants
  ADD COLUMN IF NOT EXISTS social_links text[] NOT NULL DEFAULT '{}';
