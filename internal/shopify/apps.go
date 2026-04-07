package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// InstalledApp is a Shopify app installed on a store.
type InstalledApp struct {
	Title  string `json:"title"`
	Handle string `json:"handle"`
}

// FetchInstalledApps returns the title and handle of every app installed on the store.
// Used to detect which review app a merchant has without relying on metafields.
func FetchInstalledApps(ctx context.Context, shop, token string) ([]InstalledApp, error) {
	const q = `
query InstalledApps {
  appInstallations(first: 50) {
    nodes {
      app {
        title
        handle
      }
    }
  }
}`

	raw, err := Query(ctx, shop, token, q, nil)
	if err != nil {
		return nil, fmt.Errorf("shopify: FetchInstalledApps: %w", err)
	}

	var resp struct {
		AppInstallations struct {
			Nodes []struct {
				App struct {
					Title  string `json:"title"`
					Handle string `json:"handle"`
				} `json:"app"`
			} `json:"nodes"`
		} `json:"appInstallations"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("shopify: FetchInstalledApps decode: %w", err)
	}

	apps := make([]InstalledApp, 0, len(resp.AppInstallations.Nodes))
	for _, n := range resp.AppInstallations.Nodes {
		apps = append(apps, InstalledApp{
			Title:  n.App.Title,
			Handle: n.App.Handle,
		})
	}
	return apps, nil
}
