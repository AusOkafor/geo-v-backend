-- ============================================================
-- Citation Verifier — re-query validation, hallucination
-- detection, cross-platform consistency, drift tracking.
-- ============================================================

-- Track every re-query verification run against a stored citation record.
CREATE TABLE citation_verifications (
    id                     BIGSERIAL PRIMARY KEY,
    citation_record_id     BIGINT      NOT NULL REFERENCES citation_records(id) ON DELETE CASCADE,
    merchant_id            BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    verified_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Snapshot of what we started with (denormalised for auditability)
    original_query         TEXT        NOT NULL DEFAULT '',
    original_platform      TEXT        NOT NULL DEFAULT '',
    original_response      TEXT        NOT NULL DEFAULT '',

    -- What the AI returned when we re-queried
    re_query_response      TEXT        NOT NULL DEFAULT '',

    -- Jaccard similarity between original and new response (word-set overlap)
    similarity_score       NUMERIC(5,4),          -- 0.0000 – 1.0000
    response_changed       BOOLEAN     NOT NULL DEFAULT false,

    -- Hallucinated brands: [{"brand":"Xyz","occurrences":0,"reason":"no evidence"}]
    hallucination_flags    JSONB       NOT NULL DEFAULT '[]',
    hallucination_count    INT         NOT NULL DEFAULT 0,

    -- Per-platform live results: {"chatgpt":{brands:[],mentioned:bool,response:""},...}
    cross_platform_results JSONB       NOT NULL DEFAULT '{}',
    consistency_score      NUMERIC(5,4),          -- % of brands cited on 2+ platforms

    -- Verdict
    is_authentic           BOOLEAN     NOT NULL DEFAULT true,
    verification_notes     TEXT        NOT NULL DEFAULT ''
);

-- Track response stability for a query+platform+merchant over time.
CREATE TABLE response_stability (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    query_text      TEXT        NOT NULL,
    platform        TEXT        NOT NULL,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    check_count     INT         NOT NULL DEFAULT 1,
    avg_similarity  NUMERIC(5,4) NOT NULL DEFAULT 1.0000,
    min_similarity  NUMERIC(5,4) NOT NULL DEFAULT 1.0000,
    drift_detected  BOOLEAN     NOT NULL DEFAULT false,   -- true when avg_similarity < 0.75
    UNIQUE (merchant_id, query_text, platform)
);

CREATE INDEX idx_citation_verifications_merchant
    ON citation_verifications(merchant_id, verified_at DESC);

CREATE INDEX idx_citation_verifications_record
    ON citation_verifications(citation_record_id);

CREATE INDEX idx_response_stability_merchant
    ON response_stability(merchant_id, drift_detected DESC, avg_similarity ASC);
