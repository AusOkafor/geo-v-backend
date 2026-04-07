// Package reviews detects which review app a Shopify merchant uses and
// aggregates rating data for schema injection.
package reviews

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/austinokafor/geo-backend/internal/shopify"
)

// App identifies the review platform detected.
type App string

const (
	AppJudgeMe App = "judge_me"
	AppYotpo   App = "yotpo"
	AppStamped App = "stamped"
	AppLoox    App = "loox"
	AppOkendo  App = "okendo"
	AppGrowave App = "growave"
	AppFera    App = "fera"
	AppRyviu   App = "ryviu"
	AppNone    App = "none"
)

// AppLabel returns a human-readable name for the detected review app.
func (a App) AppLabel() string {
	switch a {
	case AppJudgeMe:
		return "Judge.me"
	case AppYotpo:
		return "Yotpo"
	case AppStamped:
		return "Stamped.io"
	case AppLoox:
		return "Loox"
	case AppOkendo:
		return "Okendo"
	case AppGrowave:
		return "Growave"
	case AppFera:
		return "Fera"
	case AppRyviu:
		return "Ryviu"
	default:
		return "None"
	}
}

// MerchantReviewData is the aggregated review result for a merchant.
type MerchantReviewData struct {
	App         App
	AvgRating   float64 // weighted average across sampled products
	TotalCount  int     // sum of review counts across sampled products
	HasReviews  bool
	ProductsHit int     // number of products that had review data
}

// Detect inspects product-level review metafields and returns aggregated review
// data. It checks all supported apps in priority order and stops at the first
// one that has data — a merchant rarely installs more than one.
func Detect(products []shopify.ProductReviewMetafields) MerchantReviewData {
	type extractor struct {
		app     App
		extract func(p shopify.ProductReviewMetafields) (rating float64, count int, ok bool)
	}

	extractors := []extractor{
		{AppJudgeMe, extractJudgeMe},
		{AppYotpo, extractYotpo},
		{AppStamped, extractStamped},
		{AppLoox, extractLoox},
		{AppOkendo, extractOkendo},
		{AppGrowave, extractGrowave},
		{AppFera, extractFera},
		{AppRyviu, extractRyviu},
	}

	for _, ex := range extractors {
		var ratingSum float64
		var countSum, hits int

		for _, p := range products {
			r, c, ok := ex.extract(p)
			if !ok || c == 0 {
				continue
			}
			ratingSum += r * float64(c) // weight by count for accuracy
			countSum += c
			hits++
		}

		if hits == 0 {
			continue
		}

		avg := 0.0
		if countSum > 0 {
			avg = ratingSum / float64(countSum)
		}

		return MerchantReviewData{
			App:         ex.app,
			AvgRating:   roundTo2(avg),
			TotalCount:  countSum,
			HasReviews:  countSum > 0,
			ProductsHit: hits,
		}
	}

	return MerchantReviewData{App: AppNone}
}

// DetectAppFromInstalled identifies which review app is installed by matching
// known app handles and titles from the store's installed apps list.
// Returns AppNone if no known review app is found.
func DetectAppFromInstalled(apps []shopify.InstalledApp) App {
	// knownApps maps substrings found in handle or title to the App constant.
	// Longer/more-specific strings are listed first to avoid false matches.
	type matcher struct {
		substr string
		app    App
	}
	matchers := []matcher{
		{"judge-me", AppJudgeMe},
		{"judgeme", AppJudgeMe},
		{"judge.me", AppJudgeMe},
		{"yotpo", AppYotpo},
		{"stamped", AppStamped},
		{"loox", AppLoox},
		{"okendo", AppOkendo},
		{"growave", AppGrowave},
		{"fera", AppFera},
		{"ryviu", AppRyviu},
	}

	for _, a := range apps {
		combined := strings.ToLower(a.Handle + " " + a.Title)
		for _, m := range matchers {
			if strings.Contains(combined, m.substr) {
				return m.app
			}
		}
	}
	return AppNone
}

// ─── per-app extractors ───────────────────────────────────────────────────────

func extractJudgeMe(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.JMRating, p.JMCount)
}

func extractYotpo(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.YotpoRating, p.YotpoCount)
}

func extractStamped(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.StampedRating, p.StampedCount)
}

func extractLoox(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.LooxRating, p.LooxCount)
}

func extractOkendo(p shopify.ProductReviewMetafields) (float64, int, bool) {
	if p.OkendoSummary == nil {
		return 0, 0, false
	}
	// Okendo metafield value: {"ratingAverage":4.8,"ratingCount":127}
	var s struct {
		RatingAverage float64 `json:"ratingAverage"`
		RatingCount   int     `json:"ratingCount"`
	}
	if err := json.Unmarshal([]byte(*p.OkendoSummary), &s); err != nil {
		return 0, 0, false
	}
	return s.RatingAverage, s.RatingCount, true
}

func extractGrowave(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.GrowaveRating, p.GrowaveCount)
}

func extractFera(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.FeraRating, p.FeraCount)
}

func extractRyviu(p shopify.ProductReviewMetafields) (float64, int, bool) {
	return parseRatingCount(p.RyviuRating, p.RyviuCount)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseRatingCount(ratingPtr, countPtr *string) (float64, int, bool) {
	if ratingPtr == nil || countPtr == nil {
		return 0, 0, false
	}
	rating, err := strconv.ParseFloat(strings.TrimSpace(*ratingPtr), 64)
	if err != nil {
		return 0, 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(*countPtr))
	if err != nil {
		// Some apps store count as float ("127.0")
		if f, err2 := strconv.ParseFloat(strings.TrimSpace(*countPtr), 64); err2 == nil {
			count = int(f)
		} else {
			return 0, 0, false
		}
	}
	return rating, count, true
}

func roundTo2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
