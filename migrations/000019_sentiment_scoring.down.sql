ALTER TABLE visibility_scores
    DROP COLUMN IF EXISTS valid_mentions,
    DROP COLUMN IF EXISTS negative_mentions;
