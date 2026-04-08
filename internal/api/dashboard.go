package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/fix"
	"github.com/austinokafor/geo-backend/internal/jobs"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// getAuthMerchant resolves the authenticated merchant from context.
func (h *Handler) getAuthMerchant(c echo.Context) (*store.Merchant, error) {
	shopDomain, _ := c.Get(merchantIDKey).(string)
	m, err := store.GetMerchantByDomain(c.Request().Context(), h.DB, shopDomain)
	if err != nil {
		slog.Error("getAuthMerchant failed", "shop_domain", shopDomain, "err", err)
	}
	return m, err
}

func (h *Handler) GetMerchant(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "merchant not found")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"id":          m.ID,
		"shop_domain": m.ShopDomain,
		"brand_name":  m.BrandName,
		"category":    m.Category,
		"plan":        m.Plan,
		"active":      m.Active,
		"installed_at": m.InstalledAt,
	})
}

func (h *Handler) UpdateMerchant(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	var body struct {
		BrandName string `json:"brand_name"`
		Category  string `json:"category"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := store.UpdateMerchantProfile(c.Request().Context(), h.DB, m.ID, body.BrandName, body.Category); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) GetSocialLinks(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	return c.JSON(http.StatusOK, map[string]any{"social_links": m.SocialLinks})
}

func (h *Handler) UpdateSocialLinks(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	var body struct {
		SocialLinks []string `json:"social_links"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	// Strip empty strings so we never store blank entries
	clean := make([]string, 0, len(body.SocialLinks))
	for _, l := range body.SocialLinks {
		if l != "" {
			clean = append(clean, l)
		}
	}
	ctx := c.Request().Context()
	if err := store.UpdateSocialLinks(ctx, h.DB, m.ID, clean); err != nil {
		return err
	}
	// Rebuild the schema metafield in the background so sameAs links appear immediately.
	_, _ = h.River.Insert(ctx, jobs.SchemaRebuildJobArgs{MerchantID: m.ID}, nil)
	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handler) GetVisibilityScores(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	ctx := c.Request().Context()
	days := queryInt(c, "days", 30)
	// Always re-aggregate today's citation_records so scores are fresh even if the
	// scan worker failed to call UpsertVisibilityScores before completing.
	_ = store.UpsertVisibilityScores(ctx, h.DB, m.ID)
	scores, err := store.GetVisibilityScores(ctx, h.DB, m.ID, days)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, scores)
}

func (h *Handler) GetDailyScores(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	ctx := c.Request().Context()
	days := queryInt(c, "days", 30)
	// Same on-demand aggregation as GetVisibilityScores.
	_ = store.UpsertVisibilityScores(ctx, h.DB, m.ID)
	scores, err := store.GetDailyScores(ctx, h.DB, m.ID, days)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, scores)
}

func (h *Handler) GetCompetitors(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	comps, err := store.GetCompetitors(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, comps)
}

func (h *Handler) GetFixes(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	status := c.QueryParam("status")
	fixes, err := store.GetFixes(c.Request().Context(), h.DB, m.ID, status)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, fixes)
}

func (h *Handler) GetFix(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	fixID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid fix id")
	}
	fix, err := store.GetFix(c.Request().Context(), h.DB, m.ID, fixID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "fix not found")
	}
	return c.JSON(http.StatusOK, fix)
}

func (h *Handler) ApproveFix(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	fixID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid fix id")
	}
	if err := store.ApproveFix(c.Request().Context(), h.DB, m.ID, fixID); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// Enqueue the apply job
	_, _ = h.River.Insert(c.Request().Context(),
		jobs.FixApplyJobArgs{MerchantID: m.ID, FixID: fixID}, nil)
	return c.JSON(http.StatusOK, map[string]string{"status": "approved"})
}

func (h *Handler) RejectFix(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	fixID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid fix id")
	}
	if err := store.RejectFix(c.Request().Context(), h.DB, m.ID, fixID); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "rejected"})
}

// GetSchemaStatus checks whether the GEO.visibility JSON-LD metafield has been
// set on the merchant's shop — used by the frontend to show "Schema active" state.
func (h *Handler) GetSchemaStatus(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}

	token, err := crypto.Decrypt(m.AccessTokenEnc, []byte(h.Config.EncryptionKey))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "token decrypt failed")
	}

	value, err := shopify.GetShopMetafieldValue(
		c.Request().Context(), m.ShopDomain, token, "geo_visibility", "schema_json",
	)
	if err != nil {
		// Non-fatal — metafield just doesn't exist yet
		return c.JSON(http.StatusOK, map[string]any{"active": false, "value": nil})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"active": value != "",
		"value":  value,
	})
}

func (h *Handler) GetBrandRecognition(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	status, err := store.GetBrandRecognitionStatus(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		// No scan yet — return a zero-value status rather than an error
		return c.JSON(http.StatusOK, store.BrandRecognitionStatus{})
	}
	return c.JSON(http.StatusOK, status)
}

func (h *Handler) GetQueryGaps(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	gaps, err := store.GetQueryGaps(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, gaps)
}

func (h *Handler) GetPlatformSources(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	sources, err := store.GetPlatformSources(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, sources)
}

// GetScanStatus returns the state of the most recent scan job for the merchant.
// State is one of: "none" | "pending" | "running" | "completed" | "failed"
func (h *Handler) GetScanStatus(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}

	// Query River's job table directly for the latest scan job for this merchant.
	// river_jobs stores args as JSONB; we cast merchant_id from the JSON.
	var state string
	var attemptedAt *string
	err = h.DB.QueryRow(c.Request().Context(), `
		SELECT state, attempted_at::text
		FROM river_jobs
		WHERE kind = 'scan'
		  AND (args->>'merchant_id')::bigint = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, m.ID).Scan(&state, &attemptedAt)

	if err != nil {
		// No scan job found — never scanned
		return c.JSON(http.StatusOK, map[string]string{"state": "none"})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"state":        state,
		"attempted_at": attemptedAt,
	})
}

func (h *Handler) TriggerScan(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	ctx := c.Request().Context()
	_, err = h.River.Insert(ctx, jobs.ScanJobArgs{MerchantID: m.ID, Priority: "high"}, nil)
	if err != nil {
		slog.Error("TriggerScan: failed to enqueue", "merchant_id", m.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to queue scan")
	}

	// Also schedule fix generation independently of the scan job's own flow.
	// The scan worker enqueues fix generation too, but if it fails before completing,
	// this guarantees fixes are generated. ScheduledAt is 3 minutes from now to give
	// the scan time to finish first (35 queries × 3 platforms × ~2s each ≈ 3.5 min).
	fixAt := river.InsertOpts{ScheduledAt: time.Now().Add(3 * time.Minute)}
	if _, err := h.River.Insert(ctx, jobs.FixGenerationJobArgs{MerchantID: m.ID}, &fixAt); err != nil {
		slog.Warn("TriggerScan: failed to enqueue fix generation fallback", "merchant_id", m.ID, "err", err)
	}

	slog.Info("TriggerScan: scan queued", "merchant_id", m.ID)
	return c.JSON(http.StatusOK, map[string]string{"status": "queued"})
}

func (h *Handler) TriggerSync(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	_, err = h.River.Insert(c.Request().Context(),
		jobs.ProductSyncJobArgs{MerchantID: m.ID, Full: true}, nil)
	if err != nil {
		slog.Error("TriggerSync: failed to enqueue", "merchant_id", m.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to queue sync")
	}
	slog.Info("TriggerSync: sync queued", "merchant_id", m.ID)
	return c.JSON(http.StatusOK, map[string]string{"status": "queued"})
}

// DeleteMerchantData clears all scan history, fixes, and settings for the
// authenticated merchant while keeping their Shopify connection intact.
// DELETE /api/merchant/data
func (h *Handler) DeleteMerchantData(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	if err := store.ResetMerchantData(c.Request().Context(), h.DB, m.ID); err != nil {
		slog.Error("DeleteMerchantData: failed", "merchant_id", m.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to reset data")
	}
	slog.Info("DeleteMerchantData: reset complete", "merchant_id", m.ID)
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) GetVisibilityPipeline(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	pipeline, err := store.GetVisibilityPipeline(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pipeline)
}

func (h *Handler) GetQuickWins(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	wins, err := store.GetQuickWins(c.Request().Context(), h.DB, m.ID, m.BrandName, m.Category)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, wins)
}

func (h *Handler) GetScanProgress(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	progress, err := store.GetScanProgress(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, progress)
}

func (h *Handler) GetLiveAnswers(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	limit := queryInt(c, "limit", 20)
	answers, err := store.GetLiveAnswers(c.Request().Context(), h.DB, m.ID, limit)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, answers)
}

func (h *Handler) GetAIReadiness(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	score, err := store.GetAIReadinessScore(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, score)
}

func (h *Handler) GetNextActions(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	actions, err := store.GetNextActions(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, actions)
}

func (h *Handler) GetAuthorityScore(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	score, err := store.GetAuthorityScore(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, score)
}

func queryInt(c echo.Context, key string, def int) int {
	v := c.QueryParam(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// GetMerchantFAQs returns all active merchant-provided FAQs.
func (h *Handler) GetMerchantFAQs(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	faqs, err := store.GetMerchantFAQs(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, faqs)
}

// UpdateMerchantFAQs replaces all FAQs for the merchant and triggers a schema rebuild.
func (h *Handler) UpdateMerchantFAQs(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}

	var body []store.MerchantFAQ
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}

	// Sanitise: drop entries with blank question or answer
	clean := body[:0]
	for _, f := range body {
		q := strings.TrimSpace(f.Question)
		a := strings.TrimSpace(f.Answer)
		if q != "" && a != "" {
			f.Question = q
			f.Answer = a
			clean = append(clean, f)
		}
	}

	if err := store.ReplaceMerchantFAQs(c.Request().Context(), h.DB, m.ID, clean); err != nil {
		return err
	}

	// Auto-resolve the pending FAQ action-item fix when the merchant saves real FAQs.
	if len(clean) > 0 {
		_ = store.ApplyPendingFixByType(c.Request().Context(), h.DB, m.ID, "faq")
	}

	// Trigger schema rebuild so FAQPage reflects the new Q&As immediately.
	_, _ = h.River.Insert(c.Request().Context(), jobs.SchemaRebuildJobArgs{MerchantID: m.ID}, nil)

	return c.JSON(http.StatusOK, clean)
}

// GetFAQSuggestions calls Claude to generate 5 neutral, policy-based FAQ suggestions.
func (h *Handler) GetFAQSuggestions(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}

	generator := fix.NewGenerator(h.Config.AnthropicKey)
	suggestions, err := generator.SuggestFAQs(c.Request().Context(), m.BrandName, m.Category)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, suggestions)
}
