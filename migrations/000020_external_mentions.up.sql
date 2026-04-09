CREATE TABLE IF NOT EXISTS external_mentions (
    id               BIGSERIAL PRIMARY KEY,
    merchant_id      BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    url              TEXT NOT NULL,
    source_name      TEXT NOT NULL,
    source_domain    TEXT,
    source_type      TEXT NOT NULL CHECK (source_type IN ('editorial','review_platform','press','social','influencer','other')),
    title            TEXT,
    snippet          TEXT,
    mention_context  TEXT,
    authority_score  DECIMAL(3,2),
    sentiment        TEXT CHECK (sentiment IN ('positive','neutral','negative','unknown')),
    published_date   DATE,
    discovered_date  DATE NOT NULL DEFAULT CURRENT_DATE,
    verified         BOOLEAN NOT NULL DEFAULT FALSE,
    verified_by      BIGINT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_external_mentions_merchant      ON external_mentions(merchant_id);
CREATE INDEX idx_external_mentions_source_type   ON external_mentions(merchant_id, source_type);
CREATE INDEX idx_external_mentions_published     ON external_mentions(merchant_id, published_date DESC NULLS LAST);
