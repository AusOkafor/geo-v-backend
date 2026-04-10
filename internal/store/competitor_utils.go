package store

import "strings"

// normalizeCompetitorName normalizes a competitor name for grouping/deduping.
// - trims leading/trailing whitespace
// - collapses internal whitespace
// - lowercases
func normalizeCompetitorName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// looksLikeQueryCompetitor returns true when a "competitor" string appears to be
// a user query/prompt rather than a brand/store name.
func looksLikeQueryCompetitor(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return true
	}

	// Queries tend to be longer phrases.
	if len(strings.Fields(n)) > 4 {
		return true
	}

	lower := strings.ToLower(n)
	// Common query patterns we see leaking into competitor lists.
	queryPatterns := []string{
		"best", "top", "rated",
		"under $", "under$",
		"store", "stores", "brand", "brands",
		"value", "premium", "affordable", "cheap", "quality",
	}
	for _, p := range queryPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

