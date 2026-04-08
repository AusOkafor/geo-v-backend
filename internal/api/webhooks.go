package api

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/jobs"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// webhookHandler provides idempotent Shopify webhook processing.
// Always returns 200 — Shopify retries non-200 responses up to 19 times over 48h.
func (h *Handler) handleWebhook(c echo.Context, topic string, process func(shopDomain string, body []byte) error) error {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.JSON(http.StatusOK, nil) // 200 even on read error
	}

	// Verify HMAC
	hmacHeader := c.Request().Header.Get("X-Shopify-Hmac-Sha256")
	if !shopify.VerifyWebhookHMAC(body, hmacHeader, h.Config.ShopifyWebhookSecret) {
		return c.JSON(http.StatusOK, nil) // silently ignore invalid
	}

	shopDomain := c.Request().Header.Get("X-Shopify-Shop-Domain")
	shopifyID := c.Request().Header.Get("X-Shopify-Webhook-Id")

	ctx := c.Request().Context()

	// Idempotency: insert webhook_event — skip if already processed
	_, err = h.DB.Exec(ctx, `
		INSERT INTO webhook_events (shopify_id, topic, shop_domain, payload)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (shopify_id) DO NOTHING
	`, shopifyID, topic, shopDomain, body)
	if err != nil {
		return c.JSON(http.StatusOK, nil) // DB error — return 200, let Shopify retry
	}

	if err := process(shopDomain, body); err != nil {
		slog.Error("webhook processing error", "topic", topic, "shop", shopDomain, "err", err)
	}
	return c.JSON(http.StatusOK, nil)
}

func (h *Handler) WebhookAppUninstalled(c echo.Context) error {
	return h.handleWebhook(c, "app/uninstalled", func(shopDomain string, _ []byte) error {
		ctx := c.Request().Context()
		if err := store.DeactivateMerchant(ctx, h.DB, shopDomain); err != nil {
			return err
		}
		_, err := h.River.Insert(ctx, jobs.DataDeletionJobArgs{ShopDomain: shopDomain}, nil)
		return err
	})
}

func (h *Handler) WebhookProductsUpdate(c echo.Context) error {
	return h.handleWebhook(c, "products/update", func(shopDomain string, _ []byte) error {
		ctx := c.Request().Context()
		merchant, err := store.GetMerchantByDomain(ctx, h.DB, shopDomain)
		if err != nil {
			return err
		}
		// Sync the product catalogue and re-run the audit so progress metrics reflect the change.
		if _, err = h.River.Insert(ctx, jobs.ProductSyncJobArgs{MerchantID: merchant.ID, Full: false}, nil); err != nil {
			return err
		}
		_, err = h.River.Insert(ctx, jobs.OnboardingAuditJobArgs{MerchantID: merchant.ID}, nil)
		return err
	})
}

func (h *Handler) WebhookProductsCreate(c echo.Context) error {
	return h.handleWebhook(c, "products/create", func(shopDomain string, _ []byte) error {
		ctx := c.Request().Context()
		merchant, err := store.GetMerchantByDomain(ctx, h.DB, shopDomain)
		if err != nil {
			return err
		}
		if _, err = h.River.Insert(ctx, jobs.ProductSyncJobArgs{MerchantID: merchant.ID, Full: false}, nil); err != nil {
			return err
		}
		_, err = h.River.Insert(ctx, jobs.OnboardingAuditJobArgs{MerchantID: merchant.ID}, nil)
		return err
	})
}

// GDPR webhooks — mandatory for Shopify App Store listing
func (h *Handler) WebhookGDPRDataRequest(c echo.Context) error {
	return h.handleWebhook(c, "customers/data_request", func(_ string, _ []byte) error {
		// No personal data stored beyond shop domain — nothing to export
		return nil
	})
}

func (h *Handler) WebhookGDPRCustomerRedact(c echo.Context) error {
	return h.handleWebhook(c, "customers/redact", func(_ string, _ []byte) error {
		// No customer PII stored
		return nil
	})
}

func (h *Handler) WebhookGDPRShopRedact(c echo.Context) error {
	return h.handleWebhook(c, "shop/redact", func(shopDomain string, _ []byte) error {
		return store.DeleteMerchantData(c.Request().Context(), h.DB, shopDomain)
	})
}
