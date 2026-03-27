-- ============================================================
-- GeoVisibility — initial schema
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ------------------------------------------------------------
-- merchants
-- ------------------------------------------------------------
CREATE TABLE merchants (
    id                  BIGSERIAL PRIMARY KEY,
    shop_domain         TEXT        NOT NULL UNIQUE,
    access_token_enc    TEXT        NOT NULL,       -- AES-256-GCM encrypted
    scope               TEXT        NOT NULL DEFAULT '',
    plan                TEXT        NOT NULL DEFAULT 'free',
    active              BOOLEAN     NOT NULL DEFAULT true,
    installed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    uninstalled_at      TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------
-- products  (local catalogue cache)
-- ------------------------------------------------------------
CREATE TABLE products (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    shopify_gid     TEXT        NOT NULL,           -- e.g. gid://shopify/Product/123
    title           TEXT        NOT NULL DEFAULT '',
    description     TEXT        NOT NULL DEFAULT '',
    tags            TEXT[]      NOT NULL DEFAULT '{}',
    price_min       NUMERIC(10,2),
    price_max       NUMERIC(10,2),
    synced_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id, shopify_gid)
);

-- ------------------------------------------------------------
-- citation_records  (raw scan output)
-- ------------------------------------------------------------
CREATE TABLE citation_records (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    platform        TEXT        NOT NULL,           -- chatgpt | perplexity | gemini
    query           TEXT        NOT NULL,
    query_type      TEXT        NOT NULL DEFAULT '',
    mentioned       BOOLEAN     NOT NULL DEFAULT false,
    position        INT,
    sentiment       TEXT,                           -- positive | neutral | negative
    competitors     JSONB       NOT NULL DEFAULT '[]',
    tokens_used     INT         NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(10,6) NOT NULL DEFAULT 0,
    scanned_at      DATE        NOT NULL DEFAULT CURRENT_DATE
);

-- ------------------------------------------------------------
-- visibility_scores  (pre-aggregated daily)
-- ------------------------------------------------------------
CREATE TABLE visibility_scores (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    platform        TEXT        NOT NULL,
    score_date      DATE        NOT NULL DEFAULT CURRENT_DATE,
    score           SMALLINT    NOT NULL DEFAULT 0 CHECK (score >= 0 AND score <= 100),
    queries_run     INT         NOT NULL DEFAULT 0,
    queries_hit     INT         NOT NULL DEFAULT 0,
    UNIQUE (merchant_id, platform, score_date)
);

-- ------------------------------------------------------------
-- pending_fixes  (AI-generated recommendations)
-- ------------------------------------------------------------
CREATE TABLE pending_fixes (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    target_gid      TEXT        NOT NULL DEFAULT '',  -- Shopify GID of affected object
    fix_type        TEXT        NOT NULL,             -- description | faq | schema | directory
    priority        TEXT        NOT NULL DEFAULT 'medium', -- high | medium | low
    title           TEXT        NOT NULL DEFAULT '',
    explanation     TEXT        NOT NULL DEFAULT '',
    original        JSONB       NOT NULL DEFAULT '{}',
    generated       JSONB       NOT NULL DEFAULT '{}',
    est_impact      SMALLINT    NOT NULL DEFAULT 0,
    status          TEXT        NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending','approved','rejected','applied','failed')),
    applied_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------
-- scan_costs  (daily cost rollup per merchant+platform)
-- ------------------------------------------------------------
CREATE TABLE scan_costs (
    id              BIGSERIAL PRIMARY KEY,
    merchant_id     BIGINT      NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    cost_date       DATE        NOT NULL DEFAULT CURRENT_DATE,
    platform        TEXT        NOT NULL,
    queries_run     INT         NOT NULL DEFAULT 0,
    tokens_used     INT         NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(10,6) NOT NULL DEFAULT 0,
    UNIQUE (merchant_id, cost_date, platform)
);

-- ------------------------------------------------------------
-- webhook_events  (idempotency log)
-- ------------------------------------------------------------
CREATE TABLE webhook_events (
    id              BIGSERIAL PRIMARY KEY,
    shopify_id      TEXT        NOT NULL UNIQUE,
    topic           TEXT        NOT NULL,
    shop_domain     TEXT        NOT NULL,
    payload         JSONB       NOT NULL DEFAULT '{}',
    processed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ------------------------------------------------------------
-- Indexes
-- ------------------------------------------------------------
CREATE INDEX idx_citations_merchant_date
    ON citation_records(merchant_id, scanned_at DESC);

CREATE INDEX idx_citations_merchant_platform
    ON citation_records(merchant_id, platform, scanned_at DESC);

CREATE INDEX idx_fixes_merchant_status
    ON pending_fixes(merchant_id, status);

CREATE INDEX idx_webhook_events_shop
    ON webhook_events(shop_domain, processed_at DESC);

CREATE INDEX idx_products_merchant
    ON products(merchant_id);

CREATE INDEX idx_visibility_scores_merchant_date
    ON visibility_scores(merchant_id, score_date DESC);
