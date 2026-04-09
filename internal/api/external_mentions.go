package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/austinokafor/geo-backend/internal/store"
)

// GetExternalMentions returns tracked external mentions for the authenticated merchant.
// GET /api/v1/external-mentions?limit=50
func (h *Handler) GetExternalMentions(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	limit := queryInt(c, "limit", 50)
	mentions, err := store.GetExternalMentions(c.Request().Context(), h.DB, m.ID, limit)
	if err != nil {
		return err
	}
	if mentions == nil {
		mentions = []store.ExternalMention{}
	}
	return c.JSON(http.StatusOK, mentions)
}

// GetExternalMentionStats returns aggregate stats for the authenticated merchant's mentions.
// GET /api/v1/external-mentions/stats
func (h *Handler) GetExternalMentionStats(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}
	stats, err := store.GetExternalMentionStats(c.Request().Context(), h.DB, m.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, stats)
}

// CreateExternalMention records a new external mention for the authenticated merchant.
// POST /api/v1/external-mentions
func (h *Handler) CreateExternalMention(c echo.Context) error {
	m, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized)
	}

	var body store.ExternalMention
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}

	// Validate required fields
	body.URL = strings.TrimSpace(body.URL)
	body.SourceName = strings.TrimSpace(body.SourceName)
	body.SourceType = strings.TrimSpace(body.SourceType)
	if body.URL == "" || body.SourceName == "" || body.SourceType == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url, source_name, and source_type are required")
	}

	// Validate source_type value
	validTypes := map[string]bool{
		"editorial": true, "review_platform": true, "press": true,
		"social": true, "influencer": true, "other": true,
	}
	if !validTypes[body.SourceType] {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid source_type")
	}

	// Auto-calculate authority score from domain if not provided
	body.MerchantID = m.ID
	if body.AuthorityScore == nil && body.SourceDomain != nil && *body.SourceDomain != "" {
		score := store.CalculateAuthorityScore(*body.SourceDomain)
		body.AuthorityScore = &score
	}

	// Default sentiment to unknown if not set
	if body.Sentiment == nil {
		unknown := "unknown"
		body.Sentiment = &unknown
	}

	if err := store.InsertExternalMention(c.Request().Context(), h.DB, &body); err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, body)
}

// AdminVerifyExternalMention marks a mention as verified by an admin.
// POST /admin/external-mentions/:id/verify
func (h *Handler) AdminVerifyExternalMention(c echo.Context) error {
	mentionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}

	var body struct {
		AdminID int64 `json:"admin_id"`
	}
	// admin_id is optional — 0 is acceptable (nullable in DB)
	_ = c.Bind(&body)

	if err := store.VerifyExternalMention(c.Request().Context(), h.DB, mentionID, body.AdminID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "verified"})
}
