-- competitor_mentions stores one row per (merchant, scan_date, platform, query, competitor).
-- Normalises the competitors JSONB already in citation_records into a queryable table so
-- "you're losing to X on N queries" dashboards need no JSONB lateral joins at query time.

CREATE TABLE IF NOT EXISTS competitor_mentions (
    id               BIGSERIAL PRIMARY KEY,
    merchant_id      BIGINT  NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    scan_date        DATE    NOT NULL DEFAULT CURRENT_DATE,
    platform         TEXT    NOT NULL,
    query_text       TEXT    NOT NULL,
    competitor_name  TEXT    NOT NULL,
    competitor_pos   INT     NOT NULL DEFAULT 0,
    merchant_mentioned BOOLEAN NOT NULL DEFAULT false,
    merchant_pos     INT     NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One row per competitor per query per platform per day
    CONSTRAINT uq_competitor_mention UNIQUE (merchant_id, scan_date, platform, query_text, competitor_name)
);

CREATE INDEX idx_competitor_mentions_merchant_date ON competitor_mentions (merchant_id, scan_date);
CREATE INDEX idx_competitor_mentions_name          ON competitor_mentions (merchant_id, competitor_name);
