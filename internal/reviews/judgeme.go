package reviews

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FetchJudgeMeRatings calls Judge.me's public widget API for a given shop domain
// and returns a weighted-average rating and total review count.
// No authentication required — Judge.me exposes aggregate stats publicly.
func FetchJudgeMeRatings(ctx context.Context, shopDomain string) (avgRating float64, totalCount int, err error) {
	if shopDomain == "" {
		return 0, 0, nil
	}

	// Judge.me public stats endpoint — returns site-wide aggregate.
	url := fmt.Sprintf(
		"https://judge.me/api/v1/reviews?shop_domain=%s&platform=shopify&per_page=0",
		shopDomain,
	)

	client := &http.Client{Timeout: 10 * time.Second}
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
		return 0, 0, fmt.Errorf("judge.me api: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, 0, err
	}

	var data struct {
		Reviews []struct {
			Rating int `json:"rating"`
		} `json:"reviews"`
		// Some endpoints return totals directly
		CurrentPage int `json:"current_page"`
		PerPage     int `json:"per_page"`
		TotalCount  int `json:"total"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, 0, err
	}

	// Calculate average from returned reviews if present.
	if len(data.Reviews) > 0 {
		var sum float64
		for _, r := range data.Reviews {
			sum += float64(r.Rating)
		}
		return roundTo2(sum / float64(len(data.Reviews))), len(data.Reviews), nil
	}

	return 0, 0, nil
}
