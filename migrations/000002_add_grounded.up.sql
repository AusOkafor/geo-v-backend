-- Add grounded flag to citation_records to track whether a scan result came from
-- a web-grounded API (OpenAI Responses API with web_search_preview, Perplexity sonar)
-- vs a model-memory-only source (Together.ai, ungrounded chat completions).
ALTER TABLE citation_records
    ADD COLUMN IF NOT EXISTS grounded BOOLEAN NOT NULL DEFAULT false;
