package api

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func (h *Handler) Health(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
