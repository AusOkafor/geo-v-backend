package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// TopProduct holds product fields needed for JSON-LD schema generation.
type TopProduct struct {
	Handle   string
	Title    string
	MinPrice string
	Currency string
	ImageURL string
}

// GetTopProducts fetches the top N active published products from Shopify.
// Used to build real product entries in schema markup — never for display.
func GetTopProducts(ctx context.Context, shop, token string, limit int) ([]TopProduct, error) {
	const q = `
query GetTopProducts($first: Int!) {
  products(first: $first, query: "status:active published_status:published") {
    edges {
      node {
        handle
        title
        priceRange {
          minVariantPrice { amount currencyCode }
        }
        featuredImage { url }
      }
    }
  }
}`
	raw, err := Query(ctx, shop, token, q, map[string]any{"first": limit})
	if err != nil {
		return nil, fmt.Errorf("shopify: GetTopProducts: %w", err)
	}

	var resp struct {
		Products struct {
			Edges []struct {
				Node struct {
					Handle     string `json:"handle"`
					Title      string `json:"title"`
					PriceRange struct {
						Min struct {
							Amount   string `json:"amount"`
							Currency string `json:"currencyCode"`
						} `json:"minVariantPrice"`
					} `json:"priceRange"`
					FeaturedImage *struct {
						URL string `json:"url"`
					} `json:"featuredImage"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"products"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("shopify: GetTopProducts decode: %w", err)
	}

	out := make([]TopProduct, 0, len(resp.Products.Edges))
	for _, e := range resp.Products.Edges {
		n := e.Node
		p := TopProduct{
			Handle:   n.Handle,
			Title:    n.Title,
			MinPrice: n.PriceRange.Min.Amount,
			Currency: n.PriceRange.Min.Currency,
		}
		if n.FeaturedImage != nil {
			p.ImageURL = n.FeaturedImage.URL
		}
		out = append(out, p)
	}
	return out, nil
}
