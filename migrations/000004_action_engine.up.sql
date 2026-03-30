-- Store the AI's answer text so we can surface "Live AI Answers" in the dashboard.
ALTER TABLE citation_records ADD COLUMN IF NOT EXISTS answer_text TEXT;

-- Populate query_type where it was left blank by older scan runs (best-effort).
-- New scans will have it set correctly by the worker.
UPDATE citation_records SET query_type = 'unknown' WHERE query_type = '';
