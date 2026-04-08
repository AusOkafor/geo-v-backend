package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// HasFAQPage returns true if the merchant has a Shopify page whose title or
// handle contains "faq". Uses the existing read_content OAuth scope.
func HasFAQPage(ctx context.Context, shop, token string) (bool, error) {
	const q = `
query FindFAQPage($query: String!) {
  pages(first: 5, query: $query) {
    edges { node { handle title } }
  }
}`
	raw, err := Query(ctx, shop, token, q, map[string]any{"query": "faq"})
	if err != nil {
		return false, fmt.Errorf("shopify.HasFAQPage: %w", err)
	}

	var resp struct {
		Pages struct {
			Edges []struct {
				Node struct {
					Handle string `json:"handle"`
					Title  string `json:"title"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return false, fmt.Errorf("shopify.HasFAQPage decode: %w", err)
	}
	return len(resp.Pages.Edges) > 0, nil
}
