package api

// Admin-only Citation Verifier endpoints.
// All routes live under /admin/verifier and require ADMIN_API_KEY bearer auth.
// These endpoints make live AI calls, so they carry a 90-second per-route timeout
// (overriding the global 30-second Echo middleware timeout).

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/store"
)

// AdminVerifyCitation re-queries the original platform for a stored citation record
// and returns a full VerificationResult (similarity, hallucinations, authenticity).
// POST /admin/verifier/citations/:id?merchant_id=X
func (h *Handler) AdminVerifyCitation(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
	defer cancel()

	citationID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid citation id")
	}
	merchantID, err := strconv.ParseInt(c.QueryParam("merchant_id"), 10, 64)
	if err != nil || merchantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "merchant_id query param required")
	}

	result, err := h.Verifier.VerifyCitation(ctx, citationID, merchantID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, result)
}

// AdminCrossPlatform runs the same query on all configured AI platforms concurrently
// and returns per-platform results plus a cross-platform consistency score.
// POST /admin/verifier/cross-platform
// Body: { "query": "...", "brand_name": "...", "merchant_id": 1 }
func (h *Handler) AdminCrossPlatform(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 90*time.Second)
	defer cancel()

	var body struct {
		Query      string `json:"query"`
		BrandName  string `json:"brand_name"`
		MerchantID int64  `json:"merchant_id"`
	}
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if body.Query == "" || body.BrandName == "" || body.MerchantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "query, brand_name, and merchant_id required")
	}

	result := h.Verifier.CrossPlatform(ctx, body.Query, body.BrandName, body.MerchantID)
	return c.JSON(http.StatusOK, result)
}

// AdminListVerifications returns past verification runs for a merchant.
// GET /admin/verifier/history?merchant_id=1&limit=50
func (h *Handler) AdminListVerifications(c echo.Context) error {
	merchantID, _ := strconv.ParseInt(c.QueryParam("merchant_id"), 10, 64)
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	records, err := store.GetVerifications(c.Request().Context(), h.DB, merchantID, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if records == nil {
		records = []store.VerificationRecord{}
	}
	return c.JSON(http.StatusOK, records)
}

// AdminGetStability returns response_stability rows for a merchant, drifting queries first.
// GET /admin/verifier/stability?merchant_id=1&limit=50
func (h *Handler) AdminGetStability(c echo.Context) error {
	merchantID, _ := strconv.ParseInt(c.QueryParam("merchant_id"), 10, 64)
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	records, err := store.GetStabilityRecords(c.Request().Context(), h.DB, merchantID, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if records == nil {
		records = []store.StabilityRecord{}
	}
	return c.JSON(http.StatusOK, records)
}
