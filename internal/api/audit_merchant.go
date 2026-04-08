package api

// Merchant-facing audit endpoints — exposed under /api/v1/audit (session auth).
// These let the frontend show product/collection/page audit results and progress.

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/jobs"
	"github.com/austinokafor/geo-backend/internal/store"
)

// GetAuditProgress returns the merchant's overall content completeness snapshot.
func (h *Handler) GetAuditProgress(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	progress, err := store.GetAuditProgress(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		// No progress row yet — return zeroed struct rather than 404
		return c.JSON(http.StatusOK, store.AuditProgress{MerchantID: m.ID})
	}
	return c.JSON(http.StatusOK, progress)
}

// GetAuditProducts returns products that need content attention, paginated.
func (h *Handler) GetAuditProducts(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	products, err := store.GetProductsNeedingAttention(c.Request().Context(), h.DB, m.ID, 50)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load product audit")
	}
	return c.JSON(http.StatusOK, products)
}

// GetAuditCollections returns collections with their audit status.
func (h *Handler) GetAuditCollections(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	collections, err := store.GetCollectionsEligibleForFix(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load collection audit")
	}
	return c.JSON(http.StatusOK, collections)
}

// GetAuditPages returns pages with their audit status.
func (h *Handler) GetAuditPages(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	pages, err := store.GetPagesEligibleForFix(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load page audit")
	}
	return c.JSON(http.StatusOK, pages)
}

// RefreshAudit triggers a fresh audit for the authenticated merchant.
func (h *Handler) RefreshAudit(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	if _, err := h.River.Insert(c.Request().Context(),
		jobs.OnboardingAuditJobArgs{MerchantID: m.ID}, nil); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to queue audit")
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "queued"})
}
