ALTER TABLE citation_records
    DROP COLUMN IF EXISTS response_hash,
    DROP COLUMN IF EXISTS model_version,
    DROP COLUMN IF EXISTS scan_duration_ms;
