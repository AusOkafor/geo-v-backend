-- Page audit: tracks About, FAQ, Size Guide, and policy pages.
-- Missing essential pages become AI-eligible fix candidates.
CREATE TABLE IF NOT EXISTS merchant_page_audit (
    id                      BIGSERIAL PRIMARY KEY,
    merchant_id             BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    page_id                 TEXT,          -- Shopify GID, NULL for missing/placeholder records
    page_handle             TEXT,
    page_title              TEXT NOT NULL,
    page_type               TEXT NOT NULL, -- about | faq | size_guide | shipping | returns | contact | other

    -- Content state
    word_count              INT DEFAULT 0,
    faq_question_count      INT DEFAULT 0,  -- for faq pages
    about_has_story         BOOLEAN DEFAULT FALSE,
    about_has_team          BOOLEAN DEFAULT FALSE,

    -- AI can generate content for this page type
    ai_content_eligible     BOOLEAN DEFAULT FALSE,

    -- Workflow
    needs_attention         BOOLEAN DEFAULT TRUE,
    is_placeholder          BOOLEAN DEFAULT FALSE, -- TRUE = page doesn't exist yet

    created_at  TIMESTAMP DEFAULT NOW(),
    updated_at  TIMESTAMP DEFAULT NOW(),

    UNIQUE(merchant_id, page_type) -- one record per type per merchant
);

CREATE INDEX idx_page_audit_merchant    ON merchant_page_audit(merchant_id);
CREATE INDEX idx_page_audit_eligible    ON merchant_page_audit(merchant_id, ai_content_eligible)
    WHERE ai_content_eligible = TRUE;
