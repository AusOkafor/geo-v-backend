package shopify

import (
	"context"
	"encoding/json"
	"strings"
)

// MerchantCenterStatus describes the Google Merchant Center connection state
// as observed through Shopify's installed apps and metafields.
type MerchantCenterStatus struct {
	Connected         bool   `json:"connected"`
	ProductFeedActive bool   `json:"product_feed_active"`
	RecommendationURL string `json:"recommendation_url,omitempty"`
}

// knownGoogleAppHandles are Shopify app handles that represent the Google &
// YouTube channel (which manages Merchant Center syncing).
var knownGoogleAppHandles = []string{
	"google",
	"google-shopping",
	"google-channel",
	"google-and-youtube",
}

// CheckMerchantCenterStatus detects whether the merchant has the Google &
// YouTube app installed (which is the Shopify surface for Merchant Center).
//
// Detection strategy:
//  1. Query appInstallations for known Google channel app handles.
//  2. Check for the `google_shopping` namespace in shop metafields as a
//     secondary signal (some older installs write feed status there).
//
// We deliberately keep this read-only and Shopify-scoped — we never call the
// Google Content API directly (requires separate OAuth grant we don't have).
func CheckMerchantCenterStatus(ctx context.Context, shop, token string) (*MerchantCenterStatus, error) {
	status := &MerchantCenterStatus{
		RecommendationURL: "https://apps.shopify.com/google",
	}

	// ── 1. App installation check ─────────────────────────────────────────────
	const appsQ = `
query InstalledApps {
  appInstallations(first: 50) {
    nodes {
      app {
        handle
        title
      }
    }
  }
}`
	raw, err := Query(ctx, shop, token, appsQ, nil)
	if err == nil {
		var resp struct {
			AppInstallations struct {
				Nodes []struct {
					App struct {
						Handle string `json:"handle"`
						Title  string `json:"title"`
					} `json:"app"`
				} `json:"nodes"`
			} `json:"appInstallations"`
		}
		if json.Unmarshal(raw, &resp) == nil {
			for _, node := range resp.AppInstallations.Nodes {
				h := strings.ToLower(node.App.Handle)
				t := strings.ToLower(node.App.Title)
				for _, known := range knownGoogleAppHandles {
					if h == known || strings.Contains(h, "google") || strings.Contains(t, "google & youtube") {
						status.Connected = true
						break
					}
				}
				if status.Connected {
					break
				}
			}
		}
	}

	// ── 2. Metafield secondary signal ─────────────────────────────────────────
	// The Google channel writes a metafield when product feed sync is active.
	if !status.Connected {
		val, _ := GetShopMetafieldValue(ctx, shop, token, "google_shopping", "account_id")
		if val != "" {
			status.Connected = true
		}
	}

	// ── 3. Feed active check ──────────────────────────────────────────────────
	// If connected, check if feed sync metafield is present (written by Google channel).
	if status.Connected {
		feedVal, _ := GetShopMetafieldValue(ctx, shop, token, "google_shopping", "feed_status")
		status.ProductFeedActive = strings.EqualFold(feedVal, "active") || strings.EqualFold(feedVal, "syncing")
		// If we know they're connected but can't confirm feed state, be optimistic —
		// the feed is likely active if the app is installed.
		if feedVal == "" {
			status.ProductFeedActive = true
		}
		// Connected — no outbound recommendation needed.
		status.RecommendationURL = ""
	}

	return status, nil
}
