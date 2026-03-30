package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/jobs"
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
