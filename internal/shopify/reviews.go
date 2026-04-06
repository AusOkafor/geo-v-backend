package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// ProductReviewMetafields holds all known review-app metafield values for a single product.
// Fields are nil when the metafield is absent (app not installed / no data yet).
type ProductReviewMetafields struct {
	ProductGID string
	ProductTitle string

	// Judge.me
	JMRating *string
	JMCount  *string

	// Yotpo
	YotpoRating *string
	YotpoCount  *string

	// Stamped
	StampedRating *string
	StampedCount  *string

	// Loox
	LooxRating *string
	LooxCount  *string

	// Okendo — single JSON field: {"ratingAverage":4.8,"ratingCount":127}
	OkendoSummary *string

	// Growave
	GrowaveRating *string
	GrowaveCount  *string

	// Fera
	FeraRating *string
	FeraCount  *string

	// Ryviu
	RyviuRating *string
	RyviuCount  *string
}

// FetchProductReviewMetafields fetches review metafields for the first `limit` products
// (max 10) in a single GraphQL round-trip using field aliases.
// Returns an empty slice (not an error) when no products exist or no metafields are set.
func FetchProductReviewMetafields(ctx context.Context, shop, token string, limit int) ([]ProductReviewMetafields, error) {
	if limit < 1 || limit > 10 {
		limit = 5
	}

	// All known review-app metafield namespaces/keys fetched in one query via aliases.
	const q = `
query ReviewMetafields($first: Int!) {
  products(first: $first, query: "status:active") {
    nodes {
      id
      title
      jm_rating:     metafield(namespace: "judge_me_reviews", key: "rating")       { value }
      jm_count:      metafield(namespace: "judge_me_reviews", key: "rating_count") { value }
      yotpo_rating:  metafield(namespace: "yotpo",            key: "reviews_average") { value }
      yotpo_count:   metafield(namespace: "yotpo",            key: "reviews_count")   { value }
      stamped_rating:metafield(namespace: "stamped",          key: "reviews_average") { value }
      stamped_count: metafield(namespace: "stamped",          key: "reviews_count")   { value }
      loox_rating:   metafield(namespace: "loox",             key: "avg_rating")      { value }
      loox_count:    metafield(namespace: "loox",             key: "num_reviews")     { value }
      okendo:        metafield(namespace: "okendo",           key: "reviews_rating_summary") { value }
      growave_rating:metafield(namespace: "growave",          key: "reviews_rating")  { value }
      growave_count: metafield(namespace: "growave",          key: "reviews_count")   { value }
      fera_rating:   metafield(namespace: "fera_reviews",     key: "product_reviews_rating") { value }
      fera_count:    metafield(namespace: "fera_reviews",     key: "product_reviews_count")  { value }
      ryviu_rating:  metafield(namespace: "ryviu",            key: "rating")          { value }
      ryviu_count:   metafield(namespace: "ryviu",            key: "review_count")    { value }
    }
  }
}`

	raw, err := Query(ctx, shop, token, q, map[string]any{"first": limit})
	if err != nil {
		return nil, fmt.Errorf("shopify: FetchProductReviewMetafields: %w", err)
	}

	// Decode using a loose structure so absent metafields stay nil.
	var resp struct {
		Products struct {
			Nodes []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
				JMRating      *struct{ Value string `json:"value"` } `json:"jm_rating"`
				JMCount       *struct{ Value string `json:"value"` } `json:"jm_count"`
				YotpoRating   *struct{ Value string `json:"value"` } `json:"yotpo_rating"`
				YotpoCount    *struct{ Value string `json:"value"` } `json:"yotpo_count"`
				StampedRating *struct{ Value string `json:"value"` } `json:"stamped_rating"`
				StampedCount  *struct{ Value string `json:"value"` } `json:"stamped_count"`
				LooxRating    *struct{ Value string `json:"value"` } `json:"loox_rating"`
				LooxCount     *struct{ Value string `json:"value"` } `json:"loox_count"`
				Okendo        *struct{ Value string `json:"value"` } `json:"okendo"`
				GrowaveRating *struct{ Value string `json:"value"` } `json:"growave_rating"`
				GrowaveCount  *struct{ Value string `json:"value"` } `json:"growave_count"`
				FeraRating    *struct{ Value string `json:"value"` } `json:"fera_rating"`
				FeraCount     *struct{ Value string `json:"value"` } `json:"fera_count"`
				RyviuRating   *struct{ Value string `json:"value"` } `json:"ryviu_rating"`
				RyviuCount    *struct{ Value string `json:"value"` } `json:"ryviu_count"`
			} `json:"nodes"`
		} `json:"products"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("shopify: FetchProductReviewMetafields decode: %w", err)
	}

	out := make([]ProductReviewMetafields, 0, len(resp.Products.Nodes))
	for _, n := range resp.Products.Nodes {
		p := ProductReviewMetafields{
			ProductGID:   n.ID,
			ProductTitle: n.Title,
		}
		pv := func(s *struct{ Value string `json:"value"` }) *string {
			if s == nil || s.Value == "" {
				return nil
			}
			v := s.Value
			return &v
		}
		p.JMRating      = pv(n.JMRating)
		p.JMCount       = pv(n.JMCount)
		p.YotpoRating   = pv(n.YotpoRating)
		p.YotpoCount    = pv(n.YotpoCount)
		p.StampedRating = pv(n.StampedRating)
		p.StampedCount  = pv(n.StampedCount)
		p.LooxRating    = pv(n.LooxRating)
		p.LooxCount     = pv(n.LooxCount)
		p.OkendoSummary = pv(n.Okendo)
		p.GrowaveRating = pv(n.GrowaveRating)
		p.GrowaveCount  = pv(n.GrowaveCount)
		p.FeraRating    = pv(n.FeraRating)
		p.FeraCount     = pv(n.FeraCount)
		p.RyviuRating   = pv(n.RyviuRating)
		p.RyviuCount    = pv(n.RyviuCount)
		out = append(out, p)
	}
	return out, nil
}
