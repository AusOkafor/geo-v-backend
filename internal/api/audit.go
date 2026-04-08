package api

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/jobs"
)

// AdminTriggerAudit re-runs the onboarding audit for a merchant.
// POST /admin/audit/:merchant_id
// Allows re-auditing after the merchant makes changes (adds descriptions, installs new apps, etc.)
func (h *Handler) AdminTriggerAudit(c echo.Context) error {
	merchantID, err := strconv.ParseInt(c.Param("merchant_id"), 10, 64)
	if err != nil || merchantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid merchant_id")
	}

	if _, err := h.River.Insert(c.Request().Context(),
		jobs.OnboardingAuditJobArgs{MerchantID: merchantID}, nil); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to enqueue audit")
	}

	return c.JSON(http.StatusOK, map[string]any{
		"merchant_id": merchantID,
		"queued":      true,
	})
}
