package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/auth"
	"github.com/austinokafor/geo-backend/internal/crypto"
	"github.com/austinokafor/geo-backend/internal/jobs"
	"github.com/austinokafor/geo-backend/internal/shopify"
	"github.com/austinokafor/geo-backend/internal/store"
)

// OAuthBegin redirects to Shopify's OAuth authorization page.
func (h *Handler) OAuthBegin(c echo.Context) error {
	shop := c.QueryParam("shop")
	if shop == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing shop param")
	}
	// Normalise: strip protocol, strip any path (e.g. store.myshopify.com/admin → store.myshopify.com),
	// ensure exactly one .myshopify.com suffix.
	shop = strings.TrimPrefix(shop, "https://")
	shop = strings.TrimPrefix(shop, "http://")
	shop = strings.TrimSuffix(shop, "/")
	if idx := strings.Index(shop, "/"); idx != -1 {
		shop = shop[:idx]
	}
	if !strings.HasSuffix(shop, ".myshopify.com") {
		shop = shop + ".myshopify.com"
	}

	// Verify HMAC only when Shopify initiates the install (hmac param present).
	// When the merchant clicks "Connect Store" on our own frontend, there is no
	// hmac — we skip the check since we control the redirect ourselves.
	if c.QueryParam("hmac") != "" {
		if !shopify.VerifyOAuthHMAC(c.QueryParams(), h.Config.ShopifySecretKey) {
			return echo.NewHTTPError(http.StatusForbidden, "invalid HMAC")
		}
	}

	// Generate random state for CSRF protection
	stateBuf := make([]byte, 16)
	if _, err := rand.Read(stateBuf); err != nil {
		return err
	}
	state := hex.EncodeToString(stateBuf)

	// Store state in a short-lived cookie
	c.SetCookie(&http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		Secure:   h.Config.IsProd(),
		SameSite: http.SameSiteLaxMode,
		Path:     "/",
	})

	redirectURI := h.Config.BackendURL + "/oauth/callback"

	authURL := shopify.BuildAuthURL(shop, h.Config.ShopifyClientID, redirectURI, state)
	return c.Redirect(http.StatusFound, authURL)
}

// OAuthCallback handles Shopify's redirect after the merchant authorizes the app.
func (h *Handler) OAuthCallback(c echo.Context) error {
	// Verify HMAC
	if !shopify.VerifyOAuthHMAC(c.QueryParams(), h.Config.ShopifySecretKey) {
		return echo.NewHTTPError(http.StatusForbidden, "invalid HMAC")
	}

	// Verify state
	cookie, err := c.Cookie("oauth_state")
	if err != nil || cookie.Value != c.QueryParam("state") {
		return echo.NewHTTPError(http.StatusForbidden, "invalid state")
	}

	shop := c.QueryParam("shop")
	code := c.QueryParam("code")
	if shop == "" || code == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing shop or code")
	}

	// Exchange code for token
	tok, err := shopify.ExchangeCode(c.Request().Context(), shop, code,
		h.Config.ShopifyClientID, h.Config.ShopifySecretKey)
	if err != nil {
		return fmt.Errorf("oauth: token exchange: %w", err)
	}

	// Encrypt token
	enc, err := crypto.Encrypt(tok.AccessToken, []byte(h.Config.EncryptionKey))
	if err != nil {
		return fmt.Errorf("oauth: encrypt token: %w", err)
	}

	// Upsert merchant
	merchant, err := store.UpsertMerchant(c.Request().Context(), h.DB, store.UpsertMerchantParams{
		ShopDomain:     shop,
		AccessTokenEnc: enc,
		Scope:          tok.Scope,
	})
	if err != nil {
		return fmt.Errorf("oauth: upsert merchant: %w", err)
	}

	// Register webhooks (idempotent — safe to call on every install/reinstall)
	webhookTopics := []string{
		"PRODUCTS_UPDATE",
		"PRODUCTS_CREATE",
		"APP_UNINSTALLED",
	}
	for _, topic := range webhookTopics {
		callbackURL := fmt.Sprintf("%s/webhooks/shopify/%s",
			h.Config.BackendURL,
			strings.ToLower(strings.ReplaceAll(topic, "_", "/")),
		)
		if err := shopify.RegisterWebhook(c.Request().Context(), shop, tok.AccessToken, topic, callbackURL); err != nil {
			slog.Warn("oauth: failed to register webhook", "topic", topic, "err", err)
		}
	}

	// Enqueue initial product sync — scan is triggered manually from the dashboard
	if _, err = h.River.Insert(c.Request().Context(),
		jobs.ProductSyncJobArgs{MerchantID: merchant.ID, Full: true}, nil); err != nil {
		// Non-fatal — log so we can diagnose if jobs are missing
		slog.Error("oauth: failed to enqueue product sync", "merchant_id", merchant.ID, "err", err)
	}

	// Issue a signed JWT so the token can't be forged by knowing the shop domain
	jwtToken, err := auth.Issue(shop, []byte(h.Config.EncryptionKey))
	if err != nil {
		return fmt.Errorf("oauth: issue token: %w", err)
	}

	// Redirect merchant into the frontend dashboard with their session token
	return c.Redirect(http.StatusFound,
		fmt.Sprintf("%s/auth/callback?token=%s", h.Config.AppURL, jwtToken))
}

