package api

// Admin-only spot check endpoints.
// All routes are under /admin/spot-checks and require ADMIN_API_KEY bearer auth.
// Merchants never see or interact with this tooling.

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/store"
)

// AdminCreateSpotCheck initialises a pending spot check from an existing citation record.
// POST /admin/spot-checks
// Body: { "merchant_id": 1, "citation_record_id": 123 }
func (h *Handler) AdminCreateSpotCheck(c echo.Context) error {
	var body struct {
		MerchantID       int64 `json:"merchant_id"`
		CitationRecordID int64 `json:"citation_record_id"`
	}
	if err := c.Bind(&body); err != nil || body.MerchantID == 0 || body.CitationRecordID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "merchant_id and citation_record_id required")
	}

	sc, err := store.CreateSpotCheck(c.Request().Context(), h.DB, body.MerchantID, body.CitationRecordID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusCreated, sc)
}

// AdminListSpotChecks returns spot checks for a merchant, or all if merchant_id=0.
// GET /admin/spot-checks?merchant_id=1&limit=50
func (h *Handler) AdminListSpotChecks(c echo.Context) error {
	merchantID, _ := strconv.ParseInt(c.QueryParam("merchant_id"), 10, 64)
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	checks, err := store.GetSpotChecks(c.Request().Context(), h.DB, merchantID, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if checks == nil {
		checks = []store.SpotCheck{}
	}
	return c.JSON(http.StatusOK, checks)
}

// AdminVerifySpotCheck records manual brand list and computes accuracy metrics.
// PUT /admin/spot-checks/:id/verify
// Body: { "manual_brands": ["West Elm", "Wayfair"], "verified_by_email": "team@geo-visibility.com" }
// verified_by_type is always "team" — admin-only endpoint.
func (h *Handler) AdminVerifySpotCheck(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}

	var body struct {
		ManualBrands    []string `json:"manual_brands"`
		VerifiedByEmail string   `json:"verified_by_email"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if body.VerifiedByEmail == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "verified_by_email required")
	}

	sc, err := store.VerifySpotCheck(c.Request().Context(), h.DB, id, body.ManualBrands, "team", body.VerifiedByEmail)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, sc)
}

// AdminGetAccuracy returns per-platform accuracy metrics for a merchant.
// GET /admin/spot-checks/accuracy?merchant_id=1
func (h *Handler) AdminGetAccuracy(c echo.Context) error {
	merchantID, _ := strconv.ParseInt(c.QueryParam("merchant_id"), 10, 64)
	if merchantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "merchant_id required")
	}

	metrics, err := store.GetAccuracyMetrics(c.Request().Context(), h.DB, merchantID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if metrics == nil {
		metrics = []store.AccuracyMetric{}
	}
	return c.JSON(http.StatusOK, metrics)
}
