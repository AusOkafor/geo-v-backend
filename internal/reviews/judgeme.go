package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/austinokafor/geo-backend/internal/shopify"
)

// FetchJudgeMeRatings reads Judge.me's aggregate rating data directly from
// Shopify shop-level and product-level metafields using the merchant's own
// access token — no Judge.me API key required.
//
// Judge.me writes:
//   - shop.metafields.judgeme.shop_reviews_count  (total reviews)
//   - shop.metafields.judgeme.shop_reviews_rating (average rating, if present)
//   - product.metafields.judgeme.rating           (per-product avg)
//   - product.metafields.judgeme.rating_count     (per-product count)
func FetchJudgeMeRatings(ctx context.Context, shop_, token string) (avgRating float64, totalCount int, err error) {
	const q = `
query JudgeMeShopStats {
  shop {
    reviews_count:  metafield(namespace: "judgeme", key: "shop_reviews_count")  { value }
    reviews_rating: metafield(namespace: "judgeme", key: "shop_reviews_rating") { value }
  }
}`
	raw, apiErr := shopify.Query(ctx, shop_, token, q, nil)
	if apiErr != nil {
		return 0, 0, fmt.Errorf("judge.me shop metafields: %w", apiErr)
	}

	var resp struct {
		Shop struct {
			ReviewsCount  *struct{ Value string `json:"value"` } `json:"reviews_count"`
			ReviewsRating *struct{ Value string `json:"value"` } `json:"reviews_rating"`
		} `json:"shop"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, 0, fmt.Errorf("judge.me shop metafields decode: %w", err)
	}

	if resp.Shop.ReviewsCount != nil && resp.Shop.ReviewsCount.Value != "" {
		val := strings.TrimSpace(resp.Shop.ReviewsCount.Value)
		if c, parseErr := strconv.Atoi(val); parseErr == nil {
			totalCount = c
		}
	}
	if resp.Shop.ReviewsRating != nil && resp.Shop.ReviewsRating.Value != "" {
		val := strings.TrimSpace(resp.Shop.ReviewsRating.Value)
		if r, parseErr := strconv.ParseFloat(val, 64); parseErr == nil {
			avgRating = roundTo2(r)
		}
	}

	return avgRating, totalCount, nil
}
