package fix

import (
	"encoding/json"
	"fmt"
)

// SchemaProduct holds the real product data needed for JSON-LD generation.
// Populated from Shopify API — never AI-generated.
type SchemaProduct struct {
	Handle   string
	Title    string
	MinPrice string
	Currency string
	ImageURL string
}

// SchemaInput contains all real data needed to build a valid JSON-LD schema.
type SchemaInput struct {
	BrandName        string
	ShopDomain       string
	BrandDescription string   // AI-generated copy only — no structural data
	TopProducts      []SchemaProduct
}

// BuildSchema produces a valid CollectionPage → ItemList → Product JSON-LD string.
// All URLs, prices, and identifiers come from real Shopify/merchant data.
// AI is only responsible for BrandDescription.
func BuildSchema(in SchemaInput) (string, error) {
	storeURL := "https://" + in.ShopDomain
	brandID := storeURL + "/#brand"

	brand := map[string]any{
		"@type": "Brand",
		"@id":   brandID,
		"name":  in.BrandName,
		"url":   storeURL,
	}

	items := make([]map[string]any, 0, len(in.TopProducts))
	for i, p := range in.TopProducts {
		productURL := storeURL + "/products/" + p.Handle
		product := map[string]any{
			"@type": "Product",
			"@id":   productURL,
			"name":  p.Title,
			"url":   productURL,
			"brand": map[string]any{"@id": brandID},
		}
		if p.ImageURL != "" {
			product["image"] = p.ImageURL
		}
		if p.MinPrice != "" && p.Currency != "" {
			product["offers"] = map[string]any{
				"@type":         "Offer",
				"price":         p.MinPrice,
				"priceCurrency": p.Currency,
				"availability":  "https://schema.org/InStock",
			}
		}
		items = append(items, map[string]any{
			"@type":    "ListItem",
			"position": i + 1,
			"item":     product,
		})
	}

	schema := map[string]any{
		"@context": "https://schema.org",
		"@type":    "CollectionPage",
		"@id":      storeURL + "/#collection",
		"name":     in.BrandName,
		"url":      storeURL,
		"isPartOf": map[string]any{
			"@type": "WebSite",
			"@id":   storeURL + "/#website",
			"url":   storeURL,
			"name":  in.BrandName,
		},
		"about": brand,
	}

	if in.BrandDescription != "" {
		schema["description"] = in.BrandDescription
	}

	if len(items) > 0 {
		schema["mainEntity"] = map[string]any{
			"@type":           "ItemList",
			"name":            in.BrandName + " Products",
			"itemListElement": items,
		}
	}

	b, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("fix: BuildSchema: %w", err)
	}
	return string(b), nil
}
