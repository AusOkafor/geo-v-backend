-- Add sentiment tracking columns to visibility_scores.
-- valid_mentions: queries where brand was mentioned with positive or neutral sentiment.
-- negative_mentions: queries where brand appeared but in a negative context.
-- The score column now reflects valid_mentions / queries_run (negative excluded).

ALTER TABLE visibility_scores
    ADD COLUMN IF NOT EXISTS valid_mentions    INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS negative_mentions INT NOT NULL DEFAULT 0;

COMMENT ON COLUMN visibility_scores.valid_mentions    IS 'Mentions with positive or neutral sentiment — used for score calculation';
COMMENT ON COLUMN visibility_scores.negative_mentions IS 'Mentions with negative sentiment — stored for dashboard display but excluded from score';
