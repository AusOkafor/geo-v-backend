-- Response integrity columns on citation_records.
-- These allow the validation team to prove a stored AI response is authentic
-- and hasn't been tampered with since it was captured.

ALTER TABLE citation_records
    ADD COLUMN IF NOT EXISTS response_hash    CHAR(64),   -- SHA256 hex of answer_text
    ADD COLUMN IF NOT EXISTS model_version    TEXT,       -- e.g. "gpt-4o-mini", "sonar", "gemini-2.5-flash"
    ADD COLUMN IF NOT EXISTS scan_duration_ms INT;        -- wall-clock time for the API call

-- Backfill hash for any existing rows that already have answer_text.
UPDATE citation_records
SET response_hash = encode(digest(answer_text, 'sha256'), 'hex')
WHERE answer_text IS NOT NULL
  AND answer_text <> ''
  AND response_hash IS NULL;
