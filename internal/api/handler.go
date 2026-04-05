package api

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/riverqueue/river"
	"github.com/austinokafor/geo-backend/internal/config"
	"github.com/austinokafor/geo-backend/internal/verification"
)

// Handler holds all dependencies for HTTP handlers.
type Handler struct {
	DB       *pgxpool.Pool
	River    *river.Client[pgx.Tx]
	Config   *config.Config
	Verifier *verification.Verifier
}

// RegisterRoutes registers all API routes on the Echo instance.
func (h *Handler) RegisterRoutes(e *echo.Echo) {
	// Public
	e.GET("/health", h.Health)

	// Shopify OAuth
	e.GET("/oauth/begin", h.OAuthBegin)
	e.GET("/oauth/callback", h.OAuthCallback)

	// Shopify webhooks (must be public — Shopify can't send auth headers)
	wh := e.Group("/webhooks/shopify")
	wh.POST("/app/uninstalled", h.WebhookAppUninstalled)
	wh.POST("/products/update", h.WebhookProductsUpdate)
	wh.POST("/products/create", h.WebhookProductsCreate)
	wh.POST("/customers/data_request", h.WebhookGDPRDataRequest)
	wh.POST("/customers/redact", h.WebhookGDPRCustomerRedact)
	wh.POST("/shop/redact", h.WebhookGDPRShopRedact)

	// Authenticated dashboard API
	api := e.Group("/api/v1", sessionAuth(h.Config))
	api.GET("/merchant", h.GetMerchant)
	api.PATCH("/merchant", h.UpdateMerchant)
	api.GET("/merchant/social", h.GetSocialLinks)
	api.PATCH("/merchant/social", h.UpdateSocialLinks)
	api.GET("/merchant/faqs", h.GetMerchantFAQs)
	api.PUT("/merchant/faqs", h.UpdateMerchantFAQs)
	api.GET("/merchant/faqs/suggestions", h.GetFAQSuggestions)
	api.GET("/visibility/scores", h.GetVisibilityScores)
	api.GET("/visibility/daily", h.GetDailyScores)
	api.GET("/visibility/sources", h.GetPlatformSources)
	api.GET("/visibility/gaps", h.GetQueryGaps)
	api.GET("/visibility/recognition", h.GetBrandRecognition)
	api.GET("/visibility/answers", h.GetLiveAnswers)
	api.GET("/visibility/readiness", h.GetAIReadiness)
	api.GET("/visibility/actions", h.GetNextActions)
	api.GET("/visibility/pipeline", h.GetVisibilityPipeline)
	api.GET("/visibility/quickwins", h.GetQuickWins)
	api.GET("/visibility/progress", h.GetScanProgress)
	api.GET("/competitors", h.GetCompetitors)
	api.GET("/fixes", h.GetFixes)
	api.GET("/fixes/:id", h.GetFix)
	api.POST("/fixes/:id/approve", h.ApproveFix)
	api.POST("/fixes/:id/reject", h.RejectFix)
	api.GET("/authority/score", h.GetAuthorityScore)
	api.GET("/schema/status", h.GetSchemaStatus)
	api.GET("/scans/status", h.GetScanStatus)
	api.POST("/scans", h.TriggerScan)
	api.POST("/sync", h.TriggerSync)
	api.GET("/verify-response", h.VerifyResponseIntegrity)

	// Internal admin routes — require ADMIN_API_KEY bearer token, never exposed to merchants
	admin := e.Group("/admin", adminAuth(h.Config))
	admin.POST("/spot-checks", h.AdminCreateSpotCheck)
	admin.GET("/spot-checks", h.AdminListSpotChecks)
	admin.PUT("/spot-checks/:id/verify", h.AdminVerifySpotCheck)
	admin.GET("/spot-checks/accuracy", h.AdminGetAccuracy)

	// Citation Verifier — re-query AI platforms, detect hallucinations, track drift.
	// These make live AI calls; the 90s timeout is applied per-handler (not here).
	admin.POST("/verifier/citations/:id", h.AdminVerifyCitation)
	admin.POST("/verifier/cross-platform", h.AdminCrossPlatform)
	admin.GET("/verifier/history", h.AdminListVerifications)
	admin.GET("/verifier/stability", h.AdminGetStability)
}
