package fix

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
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
	BrandDescription string // AI-generated copy only — no structural data
	TopProducts      []SchemaProduct
}

// formatPrice converts a raw Shopify price string to a clean display value.
// "12000.0" → "12000", "57.50" → "57.50", "120.00" → "120"
func formatPrice(raw string) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || f <= 0 {
		return raw
	}
	if f == math.Trunc(f) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// BuildSchema produces a valid JSON-LD string using @graph with Organization +
// CollectionPage → ItemList → Product hierarchy.
// All URLs, prices, and identifiers come from real Shopify/merchant data.
// AI is only responsible for BrandDescription.
func BuildSchema(in SchemaInput) (string, error) {
	storeURL := "https://" + in.ShopDomain
	brandID := storeURL + "/#brand"
	orgID := storeURL + "/#organization"
	websiteID := storeURL + "/#website"
	collectionID := storeURL + "/#collection"

	brand := map[string]any{
		"@type": "Brand",
		"@id":   brandID,
		"name":  in.BrandName,
		"url":   storeURL,
	}

	// Organization ties brand + website together for entity consistency.
	// AI platforms use Organization to establish brand authority signals.
	organization := map[string]any{
		"@type": "Organization",
		"@id":   orgID,
		"name":  in.BrandName,
		"url":   storeURL,
		"brand": map[string]any{"@id": brandID},
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
				"price":         formatPrice(p.MinPrice),
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

	collectionPage := map[string]any{
		"@type": "CollectionPage",
		"@id":   collectionID,
		"name":  in.BrandName,
		"url":   storeURL,
		"isPartOf": map[string]any{
			"@type": "WebSite",
			"@id":   websiteID,
			"url":   storeURL,
			"name":  in.BrandName,
		},
		"about": brand,
	}

	if in.BrandDescription != "" {
		collectionPage["description"] = in.BrandDescription
	}

	if len(items) > 0 {
		collectionPage["mainEntity"] = map[string]any{
			"@type":           "ItemList",
			"name":            in.BrandName + " Products",
			"itemListElement": items,
		}
	}

	schema := map[string]any{
		"@context": "https://schema.org",
		"@graph":   []any{organization, collectionPage},
	}

	b, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("fix: BuildSchema: %w", err)
	}
	return string(b), nil
}
