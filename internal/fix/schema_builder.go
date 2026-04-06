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
	Handle      string
	Title       string
	Description string
	MinPrice    string
	Currency    string
	ImageURL    string
}

// SchemaFAQ is a single Q&A pair for FAQPage schema.
type SchemaFAQ struct {
	Question string
	Answer   string
}

// SchemaInput contains all real data needed to build a valid JSON-LD schema.
type SchemaInput struct {
	BrandName        string
	ShopDomain       string
	BrandDescription string      // AI-generated copy only — no structural data
	TopProducts      []SchemaProduct
	SocialLinks      []string    // sameAs links (Instagram, TikTok, etc.) — emitted only if non-empty
	FAQs             []SchemaFAQ // from applied FAQ fix — emitted as FAQPage if non-empty
	// Review data — injected as aggregateRating on each Product node when present.
	// AvgRating == 0 means no review data available; schema is emitted without it.
	AvgRating   float64
	ReviewCount int
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

// excerptDescription returns the first sentence of a plain-text description,
// capped at 160 characters, for use in ItemList product entries.
func excerptDescription(s string) string {
	s = strings.Join(strings.Fields(s), " ") // normalise whitespace
	if idx := strings.IndexAny(s, ".!?"); idx >= 0 && idx < 160 {
		return s[:idx+1]
	}
	if len(s) <= 160 {
		return s
	}
	// Truncate at last space before 160 to avoid cutting mid-word
	cut := s[:160]
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
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

	// Brand — standalone entity so AI can resolve it across the graph.
	// sameAs here and on Organization means both entities point to the same social profiles,
	// maximising the chance that an AI assistant links them to the real-world entity.
	brandEntity := map[string]any{
		"@type": "Brand",
		"@id":   brandID,
		"name":  in.BrandName,
		"url":   storeURL,
	}
	if len(in.SocialLinks) > 0 {
		brandEntity["sameAs"] = in.SocialLinks
	}

	// Organization — references Brand by @id; sameAs lives on Brand only to avoid duplication.
	organization := map[string]any{
		"@type": "Organization",
		"@id":   orgID,
		"name":  in.BrandName,
		"url":   storeURL,
		"brand": map[string]any{"@id": brandID},
	}

	// WebSite — fully defined with SearchAction so AI assistants know the site is navigable.
	website := map[string]any{
		"@type": "WebSite",
		"@id":   websiteID,
		"name":  in.BrandName,
		"url":   storeURL,
		"potentialAction": map[string]any{
			"@type":       "SearchAction",
			"target":      storeURL + "/search?q={search_term_string}",
			"query-input": "required name=search_term_string",
		},
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
		if p.Description != "" {
			product["description"] = excerptDescription(p.Description)
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
		// Inject aggregateRating from real review app data when available.
		// AI platforms explicitly flag missing reviews — this directly addresses that signal.
		if in.AvgRating > 0 && in.ReviewCount > 0 {
			product["aggregateRating"] = map[string]any{
				"@type":       "AggregateRating",
				"ratingValue": fmt.Sprintf("%.2f", in.AvgRating),
				"reviewCount": in.ReviewCount,
				"bestRating":  "5",
				"worstRating": "1",
			}
		}
		items = append(items, map[string]any{
			"@type":    "ListItem",
			"position": i + 1,
			"item":     product,
		})
	}

	collectionPage := map[string]any{
		"@type":    "CollectionPage",
		"@id":      collectionID,
		"name":     in.BrandName,
		"url":      storeURL,
		"isPartOf": map[string]any{"@id": websiteID},
		"about":    map[string]any{"@id": brandID}, // reference — Brand is defined as its own @graph node
	}
	if in.BrandDescription != "" {
		// Normalize whitespace: collapse \r\n, \n, and runs of spaces from AI output
		collectionPage["description"] = strings.Join(strings.Fields(in.BrandDescription), " ")
	}
	if len(items) > 0 {
		collectionPage["mainEntity"] = map[string]any{
			"@type":           "ItemList",
			"name":            in.BrandName + " Products",
			"itemListElement": items,
		}
	}

	graph := []any{brandEntity, organization, website, collectionPage}

	// FAQPage — included when an approved FAQ fix exists.
	// Gives AI a structured Q&A to cite directly.
	if len(in.FAQs) > 0 {
		faqItems := make([]map[string]any, 0, len(in.FAQs))
		for _, faq := range in.FAQs {
			faqItems = append(faqItems, map[string]any{
				"@type": "Question",
				"name":  faq.Question,
				"acceptedAnswer": map[string]any{
					"@type": "Answer",
					"text":  faq.Answer,
				},
			})
		}
		graph = append(graph, map[string]any{
			"@type":          "FAQPage",
			"@id":            storeURL + "/#faq",
			"mainEntity":     faqItems,
		})
	}

	schema := map[string]any{
		"@context": "https://schema.org",
		"@graph":   graph,
	}

	b, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("fix: BuildSchema: %w", err)
	}
	return string(b), nil
}
