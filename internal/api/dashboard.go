package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
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
	days := queryInt(c, "days", 30)
	scores, err := store.GetVisibilityScores(c.Request().Context(), h.DB, m.ID, days)
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
	days := queryInt(c, "days", 30)
	scores, err := store.GetDailyScores(c.Request().Context(), h.DB, m.ID, days)
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

func (h *Handler) TriggerScan(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	_, err = h.River.Insert(c.Request().Context(),
		jobs.ScanJobArgs{MerchantID: m.ID, Priority: "high"}, nil)
	if err != nil {
		slog.Error("TriggerScan: failed to enqueue", "merchant_id", m.ID, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to queue scan")
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
