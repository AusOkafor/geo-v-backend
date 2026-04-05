package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
)

// VerifyResponseIntegrity lets any authenticated merchant verify that a stored
// AI response hasn't been tampered with since it was captured.
//
// GET /api/v1/verify-response?id=123
//
// Re-hashes the stored answer_text and compares it to response_hash.
// Matching means the response is provably identical to what the AI returned.
func (h *Handler) VerifyResponseIntegrity(c echo.Context) error {
	merchant, err := h.getAuthMerchant(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "merchant not found")
	}

	id, parseErr := strconv.ParseInt(c.QueryParam("id"), 10, 64)
	if parseErr != nil || id == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "id required")
	}

	var (
		answerText   *string
		responseHash *string
		modelVersion *string
		scannedAt    string
	)
	if err := h.DB.QueryRow(c.Request().Context(), `
		SELECT answer_text, response_hash, model_version, scanned_at::text
		FROM citation_records
		WHERE id = $1 AND merchant_id = $2
	`, id, merchant.ID).Scan(&answerText, &responseHash, &modelVersion, &scannedAt); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "record not found")
	}

	text := ""
	if answerText != nil {
		text = *answerText
	}

	// Pre-migration record — no hash stored yet.
	if responseHash == nil || *responseHash == "" {
		return c.JSON(http.StatusOK, map[string]any{
			"valid":         false,
			"message":       "No integrity hash stored for this record (captured before hashing was enabled).",
			"captured_at":   scannedAt,
			"model_version": nilStr(modelVersion),
			"response_hash": "",
		})
	}

	h256 := sha256.Sum256([]byte(text))
	computed := hex.EncodeToString(h256[:])
	valid := computed == *responseHash

	msg := fmt.Sprintf("Response matches original captured on %s", scannedAt)
	if !valid {
		msg = "Hash mismatch — this response may have been modified after capture"
	}

	return c.JSON(http.StatusOK, map[string]any{
		"valid":         valid,
		"message":       msg,
		"captured_at":   scannedAt,
		"model_version": nilStr(modelVersion),
		"response_hash": *responseHash,
	})
}

func nilStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
