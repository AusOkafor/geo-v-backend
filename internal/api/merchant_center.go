package api

import (
	"net/http"

	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
	"github.com/labstack/echo/v4"
)

// GetMerchantCenterStatus returns the merchant's Google Merchant Center
// connection status as detected through their Shopify app installations.
// GET /api/v1/merchant-center/status
func (h *Handler) GetMerchantCenterStatus(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}

	token, err := crypto.Decrypt(m.AccessTokenEnc, []byte(h.Config.EncryptionKey))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "token error")
	}

	status, err := shopify.CheckMerchantCenterStatus(c.Request().Context(), m.ShopDomain, token)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Augment with last-known audit data so the UI doesn't need to wait for a
	// live Shopify API call when we already have the info from the audit.
	if audit, _ := store.GetMerchantAudit(c.Request().Context(), h.DB, m.ID); audit != nil {
		// Audit is the source of truth between calls — only override if the live
		// check returned false but audit says true (stale negative detection).
		if !status.Connected && audit.GoogleMerchantCenterConnected {
			status.Connected = true
			status.ProductFeedActive = audit.GoogleProductFeedActive
			status.RecommendationURL = ""
		}
	}

	return c.JSON(http.StatusOK, status)
}
