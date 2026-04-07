package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// knownReviewAppSnippets maps theme snippet filenames (stem, lowercased) to app IDs.
var knownReviewAppSnippets = map[string]string{
	"judgeme_widgets":       "judge_me",
	"judgeme":               "judge_me",
	"judge-me":              "judge_me",
	"yotpo":                 "yotpo",
	"yotpo-bottomline":      "yotpo",
	"yotpo-reviews":         "yotpo",
	"stamped":               "stamped",
	"stamped-main":          "stamped",
	"stamped-reviews":       "stamped",
	"loox":                  "loox",
	"okendo":                "okendo",
	"okendo-reviews-widget": "okendo",
	"growave":               "growave",
	"ssw-helper":            "growave",
	"fera":                  "fera",
	"fera-widget":           "fera",
	"ryviu":                 "ryviu",
}

// knownReviewAppCDN maps CDN/script URL substrings found in theme.liquid to app IDs.
// Used when a review app injects its script directly into theme.liquid rather than
// adding a separate snippet file.
var knownReviewAppCDN = []struct {
	pattern string
	appID   string
}{
	{"staticw2.yotpo.com", "yotpo"},
	{"cdn.yotpo.com", "yotpo"},
	{"yotpo.com/", "yotpo"},
	{"cdn.judge.me", "judge_me"},
	{"judge.me/", "judge_me"},
	{"judgeme", "judge_me"},
	{"cdn.stamped.io", "stamped"},
	{"stamped.io/", "stamped"},
	{"loox.io/", "loox"},
	{"cdn.okendo.io", "okendo"},
	{"okendo.io/", "okendo"},
	{"cdn.growave.io", "growave"},
	{"growave.io/", "growave"},
	{"cdn.fera.ai", "fera"},
	{"fera.ai/", "fera"},
	{"ryviu.com/", "ryviu"},
}

// FetchThemeSnippetNames returns all filenames in the active theme.
// Used by the debug endpoint to identify what a review app actually writes.
func FetchThemeSnippetNames(ctx context.Context, shop, token string) ([]string, error) {
	const themeQ = `
query ActiveTheme {
  themes(first: 5, roles: [MAIN]) {
    nodes { id }
  }
}`
	raw, err := Query(ctx, shop, token, themeQ, nil)
	if err != nil {
		return nil, fmt.Errorf("shopify: FetchThemeSnippetNames themes: %w", err)
	}
	var themeResp struct {
		Themes struct {
			Nodes []struct{ ID string `json:"id"` } `json:"nodes"`
		} `json:"themes"`
	}
	if err := json.Unmarshal(raw, &themeResp); err != nil || len(themeResp.Themes.Nodes) == 0 {
		return nil, fmt.Errorf("shopify: FetchThemeSnippetNames: no active theme")
	}
	themeID := themeResp.Themes.Nodes[0].ID

	const filesQ = `
query ThemeFiles($themeId: ID!) {
  theme(id: $themeId) {
    files(first: 200) {
      nodes { filename }
    }
  }
}`
	raw2, err := Query(ctx, shop, token, filesQ, map[string]any{"themeId": themeID})
	if err != nil {
		return nil, fmt.Errorf("shopify: FetchThemeSnippetNames files: %w", err)
	}
	var filesResp struct {
		Theme struct {
			Files struct {
				Nodes []struct{ Filename string `json:"filename"` } `json:"nodes"`
			} `json:"files"`
		} `json:"theme"`
	}
	if err := json.Unmarshal(raw2, &filesResp); err != nil {
		return nil, fmt.Errorf("shopify: FetchThemeSnippetNames decode: %w", err)
	}
	names := make([]string, 0, len(filesResp.Theme.Files.Nodes))
	for _, f := range filesResp.Theme.Files.Nodes {
		names = append(names, f.Filename)
	}
	return names, nil
}

// DetectReviewAppFromTheme returns the review app identifier found in the active
// theme's snippet files, or "" if no known review app snippets are present.
// Uses read_content scope (already in our OAuth scopes).
func DetectReviewAppFromTheme(ctx context.Context, shop, token string) (string, error) {
	// Step 1: get the ID of the active (main) theme.
	const themeQ = `
query ActiveTheme {
  themes(first: 5, roles: [MAIN]) {
    nodes {
      id
      name
      role
    }
  }
}`
	raw, err := Query(ctx, shop, token, themeQ, nil)
	if err != nil {
		return "", fmt.Errorf("shopify: DetectReviewAppFromTheme themes: %w", err)
	}

	var themeResp struct {
		Themes struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Role string `json:"role"`
			} `json:"nodes"`
		} `json:"themes"`
	}
	if err := json.Unmarshal(raw, &themeResp); err != nil {
		return "", fmt.Errorf("shopify: DetectReviewAppFromTheme decode themes: %w", err)
	}
	if len(themeResp.Themes.Nodes) == 0 {
		return "", nil
	}
	themeID := themeResp.Themes.Nodes[0].ID

	// Step 2: list snippet files in the theme and match against known review apps.
	const filesQ = `
query ThemeSnippets($themeId: ID!) {
  theme(id: $themeId) {
    files(first: 200) {
      nodes {
        filename
      }
    }
  }
}`
	raw2, err := Query(ctx, shop, token, filesQ, map[string]any{"themeId": themeID})
	if err != nil {
		return "", fmt.Errorf("shopify: DetectReviewAppFromTheme files: %w", err)
	}

	var filesResp struct {
		Theme struct {
			Files struct {
				Nodes []struct {
					Filename string `json:"filename"`
				} `json:"nodes"`
			} `json:"files"`
		} `json:"theme"`
	}
	if err := json.Unmarshal(raw2, &filesResp); err != nil {
		return "", fmt.Errorf("shopify: DetectReviewAppFromTheme decode files: %w", err)
	}

	for _, f := range filesResp.Theme.Files.Nodes {
		// Only look at snippets: "snippets/foo.liquid" → stem "foo"
		if !strings.HasPrefix(f.Filename, "snippets/") {
			continue
		}
		stem := strings.TrimSuffix(strings.TrimPrefix(f.Filename, "snippets/"), ".liquid")
		stem = strings.ToLower(stem)
		for pattern, appID := range knownReviewAppSnippets {
			if strings.Contains(stem, pattern) {
				return appID, nil
			}
		}
	}

	// Fallback: read layout/theme.liquid content and scan for known CDN URLs.
	// Some review apps paste their script tag directly into theme.liquid.
	const contentQ = `
query ThemeLiquidContent($themeId: ID!) {
  theme(id: $themeId) {
    files(filenames: ["layout/theme.liquid"], first: 1) {
      nodes {
        filename
        body {
          ... on OnlineStoreThemeFileBodyText {
            content
          }
        }
      }
    }
  }
}`
	raw3, err := Query(ctx, shop, token, contentQ, map[string]any{"themeId": themeID})
	if err != nil {
		// Non-fatal — snippet scan already came up empty.
		return "", nil
	}

	var contentResp struct {
		Theme struct {
			Files struct {
				Nodes []struct {
					Filename string `json:"filename"`
					Body     struct {
						Content string `json:"content"`
					} `json:"body"`
				} `json:"nodes"`
			} `json:"files"`
		} `json:"theme"`
	}
	if err := json.Unmarshal(raw3, &contentResp); err != nil || len(contentResp.Theme.Files.Nodes) == 0 {
		return "", nil
	}

	themeLiquid := strings.ToLower(contentResp.Theme.Files.Nodes[0].Body.Content)
	for _, m := range knownReviewAppCDN {
		if strings.Contains(themeLiquid, m.pattern) {
			return m.appID, nil
		}
	}

	return "", nil
}
