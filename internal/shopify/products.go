package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
// If productType is non-empty, filters to that product_type (exact Shopify field match).
// Falls back to all active published products if the typed query returns nothing.
// Used to build real product entries in schema markup — never for display.
func GetTopProducts(ctx context.Context, shop, token string, limit int, productType string) ([]TopProduct, error) {
	const q = `
query GetTopProducts($first: Int!, $filter: String!) {
  products(first: $first, query: $filter, sortKey: TITLE) {
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
	queryFilter := "status:active published_status:published"
	if productType != "" {
		// Use title_contains as a broader match since product_type requires exact values.
		// Strip category qualifiers so "Fine Jewelry" → searches for "jewelry" in title.
		keyword := strings.ToLower(productType)
		for _, strip := range []string{"fine ", "premium ", "luxury ", "boutique "} {
			keyword = strings.ReplaceAll(keyword, strip, "")
		}
		queryFilter = fmt.Sprintf("status:active published_status:published title:%s", keyword)
	}

	out, err := fetchProducts(ctx, shop, token, q, queryFilter, limit)
	if err != nil {
		return nil, err
	}
	// Fallback: if the typed filter returned nothing, retry without filter
	if len(out) == 0 && productType != "" {
		return fetchProducts(ctx, shop, token, q, "status:active published_status:published", limit)
	}
	return out, nil
}

type productQueryResp struct {
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

func fetchProducts(ctx context.Context, shop, token, q, queryFilter string, limit int) ([]TopProduct, error) {
	raw, err := Query(ctx, shop, token, q, map[string]any{"first": limit, "filter": queryFilter})
	if err != nil {
		return nil, fmt.Errorf("shopify: GetTopProducts: %w", err)
	}
	var resp productQueryResp
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
