-- Spot check system: manual verification of citation detection accuracy.
-- Validates that auto-detected brand mentions in AI responses are correct.

CREATE TABLE spot_checks (
    id                  BIGSERIAL PRIMARY KEY,
    citation_record_id  BIGINT          NOT NULL REFERENCES citation_records(id) ON DELETE CASCADE,
    merchant_id         BIGINT          NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    query_text          TEXT            NOT NULL,
    platform            VARCHAR(20)     NOT NULL,
    ai_response         TEXT            NOT NULL,       -- answer_text from citation_records
    manual_brands       TEXT[]          NOT NULL DEFAULT '{}',
    detected_brands     TEXT[]          NOT NULL DEFAULT '{}',
    precision_score     NUMERIC(5,4),
    recall_score        NUMERIC(5,4),
    f1_score            NUMERIC(5,4),
    true_positives      INT             NOT NULL DEFAULT 0,
    false_positives     INT             NOT NULL DEFAULT 0,
    false_negatives     INT             NOT NULL DEFAULT 0,
    status              VARCHAR(20)     NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'verified', 'disputed')),
    verified_by_type    VARCHAR(20)     NOT NULL DEFAULT 'team'
                            CHECK (verified_by_type IN ('team', 'merchant')),
    verified_by_email   VARCHAR(255),
    verified_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT now(),
    -- Prevent duplicate spot checks on the same citation record
    UNIQUE (citation_record_id),
    -- Both fields required when verified
    CONSTRAINT check_verified_fields CHECK (
        (status = 'pending') OR
        (status IN ('verified', 'disputed') AND verified_by_email IS NOT NULL AND verified_at IS NOT NULL)
    )
);

CREATE INDEX spot_checks_merchant_id_idx ON spot_checks(merchant_id);
CREATE INDEX spot_checks_status_idx ON spot_checks(status);
CREATE INDEX spot_checks_created_at_idx ON spot_checks(created_at DESC);

-- Per-merchant, per-platform accuracy rolled up daily by the validation worker.
CREATE TABLE accuracy_metrics (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT          NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    date            DATE            NOT NULL,
    platform        VARCHAR(20)     NOT NULL,
    avg_precision   NUMERIC(5,4)    NOT NULL,
    avg_recall      NUMERIC(5,4)    NOT NULL,
    avg_f1          NUMERIC(5,4)    NOT NULL,
    sample_size     INT             NOT NULL,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, date, platform)
);

CREATE INDEX accuracy_metrics_merchant_date_idx ON accuracy_metrics(merchant_id, date DESC);

-- One row per daily validation run (system-wide aggregate).
CREATE TABLE validation_runs (
    id                  BIGSERIAL PRIMARY KEY,
    run_date            DATE            NOT NULL UNIQUE,
    total_queries       INT             NOT NULL DEFAULT 0,
    avg_precision       NUMERIC(5,4),
    avg_recall          NUMERIC(5,4),
    avg_f1              NUMERIC(5,4),
    alerts_triggered    INT             NOT NULL DEFAULT 0,
    completed_at        TIMESTAMPTZ     NOT NULL DEFAULT now()
);

-- Index to support the validation worker's daily sample query efficiently.
CREATE INDEX idx_citation_records_scanned_platform
    ON citation_records(scanned_at DESC, platform);
