package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/auth"
	"github.com/austinokafor/geo-backend/internal/config"
)

const (
	merchantIDKey = "merchant_id"
	adminKey      = "admin_authed"
)

// adminAuth validates the ADMIN_API_KEY bearer token for internal-only endpoints.
// Returns 401 if the key is missing or wrong, 503 if ADMIN_API_KEY is not configured.
func adminAuth(cfg *config.Config) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if cfg.AdminAPIKey == "" {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "admin endpoints not configured")
			}
			header := c.Request().Header.Get("Authorization")
			if len(header) > 7 && header[:7] == "Bearer " {
				header = header[7:]
			}
			if header == "" || header != cfg.AdminAPIKey {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid admin key")
			}
			c.Set(adminKey, true)
			return next(c)
		}
	}
}

// sessionAuth validates the Bearer JWT and injects the shop domain into context.
func sessionAuth(cfg *config.Config) echo.MiddlewareFunc {
	secret := []byte(cfg.EncryptionKey)
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			header := c.Request().Header.Get("Authorization")
			if len(header) > 7 && header[:7] == "Bearer " {
				header = header[7:]
			}
			if header == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing token")
			}
			shopDomain, err := auth.Verify(header, secret)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}
			c.Set(merchantIDKey, shopDomain)
			return next(c)
		}
	}
}
