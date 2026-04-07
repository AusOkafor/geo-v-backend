package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// knownReviewAppSnippets maps theme snippet filenames to review app identifiers.
// Every review app adds recognisable snippet files to the active theme on install.
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
	return "", nil
}
