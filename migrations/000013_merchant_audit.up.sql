CREATE TABLE merchant_audit (
    id                              BIGSERIAL PRIMARY KEY,
    merchant_id                     BIGINT  NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    schema_live                     BOOLEAN NOT NULL DEFAULT false,
    avg_description_words           INT     NOT NULL DEFAULT 0,
    products_with_no_description    INT     NOT NULL DEFAULT 0,
    products_with_short_description INT     NOT NULL DEFAULT 0,
    has_faq_page                    BOOLEAN NOT NULL DEFAULT false,
    review_app                      TEXT    NOT NULL DEFAULT '',
    audited_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (merchant_id)
);
