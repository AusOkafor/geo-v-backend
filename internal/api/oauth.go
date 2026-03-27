package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
	"github.com/riverqueue/river"
	"github.com/yourname/geo-backend/internal/crypto"
	"github.com/yourname/geo-backend/internal/jobs"
	"github.com/yourname/geo-backend/internal/shopify"
	"github.com/yourname/geo-backend/internal/store"
)

// OAuthBegin redirects to Shopify's OAuth authorization page.
func (h *Handler) OAuthBegin(c echo.Context) error {
	shop := c.QueryParam("shop")
	if shop == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "missing shop param")
	}

	// Verify HMAC on the install request
	if !shopify.VerifyOAuthHMAC(c.QueryParams(), h.Config.ShopifySecretKey) {
		return echo.NewHTTPError(http.StatusForbidden, "invalid HMAC")
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

	redirectURI := fmt.Sprintf("%s/oauth/callback", h.Config.ShopifyAppHandle)
	// Use full URL in production
	if h.Config.IsProd() {
		redirectURI = "https://geo-api.onrender.com/oauth/callback"
	} else {
		redirectURI = "http://localhost:8080/oauth/callback"
	}

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

	// Enqueue initial high-priority scan + product sync
	_, err = h.River.InsertMany(c.Request().Context(), []river.InsertManyParams{
		{Args: jobs.ProductSyncJobArgs{MerchantID: merchant.ID, Full: true}},
		{Args: jobs.ScanJobArgs{MerchantID: merchant.ID, Priority: "high"}},
	})
	if err != nil {
		// Non-fatal — jobs can be triggered manually
		_ = err
	}

	// Redirect merchant into the app
	return c.Redirect(http.StatusFound,
		fmt.Sprintf("https://%s/admin/apps/%s", shop, h.Config.ShopifyAppHandle))
}

// insertManyRiver is a type assertion helper for the generic River client.
func insertManyRiver(rc *river.Client[pgx.Tx]) *river.Client[pgx.Tx] { return rc }
