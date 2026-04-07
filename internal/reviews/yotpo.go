package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchYotpoRatings calls Yotpo's public widget API for each product GID and
// returns a weighted-average rating and total review count across all products.
// productGIDs are Shopify Admin GIDs: "gid://shopify/Product/8437900050736".
// Returns (0, 0, nil) when appKey is empty or no reviews exist — never errors
// on individual product failures so a partial result is still usable.
func FetchYotpoRatings(ctx context.Context, appKey string, productGIDs []string) (avgRating float64, totalCount int, err error) {
	if appKey == "" || len(productGIDs) == 0 {
		return 0, 0, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var ratingSum float64

	for _, gid := range productGIDs {
		// "gid://shopify/Product/8437900050736" → "8437900050736"
		productID := gid
		if idx := strings.LastIndex(gid, "/"); idx >= 0 {
			productID = gid[idx+1:]
		}

		rating, count, fetchErr := fetchYotpoProductBottomline(ctx, client, appKey, productID)
		if fetchErr != nil {
			continue // non-fatal: skip this product
		}
		if count > 0 {
			ratingSum += rating * float64(count)
			totalCount += count
		}
	}

	if totalCount == 0 {
		return 0, 0, nil
	}
	return roundTo2(ratingSum / float64(totalCount)), totalCount, nil
}

// fetchYotpoProductBottomline fetches the average score and review count for
// a single product from Yotpo's public widget API.
func fetchYotpoProductBottomline(ctx context.Context, client *http.Client, appKey, productID string) (float64, int, error) {
	url := fmt.Sprintf(
		"https://api.yotpo.com/v1/widget/%s/products/%s/reviews.json?per_page=1",
		appKey, productID,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("yotpo api: status %d for product %s", resp.StatusCode, productID)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, 0, err
	}

	var data struct {
		Response struct {
			Bottomline struct {
				AverageScore float64 `json:"average_score"`
				TotalReviews int     `json:"total_reviews"`
			} `json:"bottomline"`
			// Older response shape
			Product struct {
				AverageScore float64 `json:"average_score"`
				ReviewsCount int     `json:"reviews_count"`
			} `json:"product"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, 0, err
	}

	// Prefer bottomline field, fall back to product field.
	avgScore := data.Response.Bottomline.AverageScore
	count := data.Response.Bottomline.TotalReviews
	if count == 0 {
		avgScore = data.Response.Product.AverageScore
		count = data.Response.Product.ReviewsCount
	}

	return avgScore, count, nil
}
