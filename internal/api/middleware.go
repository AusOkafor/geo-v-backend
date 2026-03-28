package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/auth"
	"github.com/austinokafor/geo-backend/internal/config"
)

const merchantIDKey = "merchant_id"

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
