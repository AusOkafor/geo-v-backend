package api

// Admin-only Review Detector endpoints.
// All routes live under /admin/reviews and require ADMIN_API_KEY bearer auth.

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/jobs"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// AdminListReviews returns the review status for all active merchants.
// GET /admin/reviews
func (h *Handler) AdminListReviews(c echo.Context) error {
	statuses, err := store.GetAllMerchantReviewStatuses(c.Request().Context(), h.DB)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if statuses == nil {
		statuses = []store.MerchantReviewStatus{}
	}
	return c.JSON(http.StatusOK, statuses)
}

// AdminScanMerchantReviews enqueues a ReviewScanJob for a single merchant.
// POST /admin/reviews/scan/:merchant_id
func (h *Handler) AdminScanMerchantReviews(c echo.Context) error {
	merchantID, err := strconv.ParseInt(c.Param("merchant_id"), 10, 64)
	if err != nil || merchantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid merchant_id")
	}

	// Verify the merchant exists before enqueuing.
	if _, err := store.GetMerchant(c.Request().Context(), h.DB, merchantID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "merchant not found")
	}

	if _, err := h.River.Insert(c.Request().Context(), jobs.ReviewScanJobArgs{MerchantID: merchantID}, nil); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusAccepted, map[string]any{
		"queued":      true,
		"merchant_id": merchantID,
	})
}

// AdminDebugProductMetafields returns every metafield on the first 3 products
// for a merchant. Used to identify unknown review app namespaces/keys.
// GET /admin/reviews/debug/:merchant_id
func (h *Handler) AdminDebugProductMetafields(c echo.Context) error {
	merchantID, err := strconv.ParseInt(c.Param("merchant_id"), 10, 64)
	if err != nil || merchantID == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid merchant_id")
	}

	merchant, err := store.GetMerchant(c.Request().Context(), h.DB, merchantID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "merchant not found")
	}

	token, err := crypto.Decrypt(merchant.AccessTokenEnc, []byte(h.Config.EncryptionKey))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not decrypt token")
	}

	entries, err := shopify.FetchAllProductMetafields(c.Request().Context(), merchant.ShopDomain, token, 3)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"merchant_id": merchantID,
		"shop_domain": merchant.ShopDomain,
		"metafields":  entries,
		"count":       len(entries),
	})
}

// AdminScanAllReviews enqueues a ReviewScanJob for every active merchant.
// POST /admin/reviews/scan-all
func (h *Handler) AdminScanAllReviews(c echo.Context) error {
	ctx := c.Request().Context()

	merchants, err := store.GetActiveMerchants(ctx, h.DB)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	queued := 0
	for _, m := range merchants {
		if _, err := h.River.Insert(ctx, jobs.ReviewScanJobArgs{MerchantID: m.ID}, nil); err == nil {
			queued++
		}
	}

	return c.JSON(http.StatusAccepted, map[string]any{
		"queued": queued,
		"total":  len(merchants),
	})
}
