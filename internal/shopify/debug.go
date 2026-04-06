package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// ProductMetafieldEntry is a single metafield found on a product.
type ProductMetafieldEntry struct {
	ProductGID   string `json:"product_id"`
	ProductTitle string `json:"product_title"`
	Namespace    string `json:"namespace"`
	Key          string `json:"key"`
	Type         string `json:"type"`
	Value        string `json:"value"`
}

// DebugProductsResult holds products found and their metafields.
type DebugProductsResult struct {
	Products  []DebugProduct        `json:"products"`
	Metafields []ProductMetafieldEntry `json:"metafields"`
}

// DebugProduct is a product summary for debug output.
type DebugProduct struct {
	GID           string `json:"gid"`
	Title         string `json:"title"`
	MetafieldCount int   `json:"metafield_count"`
}

// FetchAllProductMetafields returns every metafield on the first `limit` products.
// Used by the admin debug endpoint to identify unknown review app namespaces.
func FetchAllProductMetafields(ctx context.Context, shop, token string, limit int) (*DebugProductsResult, error) {
	if limit < 1 || limit > 10 {
		limit = 3
	}

	const q = `
query AllProductMetafields($first: Int!) {
  products(first: $first) {
    nodes {
      id
      title
      metafields(first: 50) {
        nodes {
          namespace
          key
          type
          value
        }
      }
    }
  }
}`

	raw, err := Query(ctx, shop, token, q, map[string]any{"first": limit})
	if err != nil {
		return nil, fmt.Errorf("shopify: FetchAllProductMetafields: %w", err)
	}

	var resp struct {
		Products struct {
			Nodes []struct {
				ID         string `json:"id"`
				Title      string `json:"title"`
				Metafields struct {
					Nodes []struct {
						Namespace string `json:"namespace"`
						Key       string `json:"key"`
						Type      string `json:"type"`
						Value     string `json:"value"`
					} `json:"nodes"`
				} `json:"metafields"`
			} `json:"nodes"`
		} `json:"products"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("shopify: FetchAllProductMetafields decode: %w", err)
	}

	result := &DebugProductsResult{
		Products:   make([]DebugProduct, 0, len(resp.Products.Nodes)),
		Metafields: []ProductMetafieldEntry{},
	}

	for _, p := range resp.Products.Nodes {
		result.Products = append(result.Products, DebugProduct{
			GID:           p.ID,
			Title:         p.Title,
			MetafieldCount: len(p.Metafields.Nodes),
		})
		for _, mf := range p.Metafields.Nodes {
			result.Metafields = append(result.Metafields, ProductMetafieldEntry{
				ProductGID:   p.ID,
				ProductTitle: p.Title,
				Namespace:    mf.Namespace,
				Key:          mf.Key,
				Type:         mf.Type,
				Value:        mf.Value,
			})
		}
	}
	return result, nil
}
