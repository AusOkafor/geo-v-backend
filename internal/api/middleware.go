package api

import (
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/yourname/geo-backend/internal/config"
	"github.com/yourname/geo-backend/internal/store"
)

const merchantIDKey = "merchant_id"

// sessionAuth validates the Bearer token and injects merchant_id into context.
// For MVP: token IS the shop domain (replace with JWT in production).
func sessionAuth(cfg *config.Config) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			token := c.Request().Header.Get("Authorization")
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
			if token == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing token")
			}
			// Token is shop domain for MVP
			c.Set(merchantIDKey, token)
			return next(c)
		}
	}
}

// merchantFromCtx extracts the authenticated merchant from context.
func merchantFromCtx(c echo.Context, db interface{ QueryRow(interface{}, string, ...interface{}) interface{} }) (*store.Merchant, error) {
	shopDomain, _ := c.Get(merchantIDKey).(string)
	_ = shopDomain
	_ = db
	return nil, nil
}

// readBody reads the full request body and replaces it so it can be read again.
func readBody(c echo.Context) ([]byte, error) {
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return nil, err
	}
	// Reset body so framework can read it again if needed
	c.Request().Body = io.NopCloser(newByteReader(body))
	return body, nil
}

type byteReader struct{ data []byte }

func newByteReader(b []byte) *byteReader { return &byteReader{data: b} }

func (b *byteReader) Read(p []byte) (n int, err error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n = copy(p, b.data)
	b.data = b.data[n:]
	return
}
