package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ThemeDetectionResult holds the detected review app and any extracted API key,
// both derived from a single theme.liquid read.
type ThemeDetectionResult struct {
	App    string // "yotpo", "judge_me", etc. — empty if not found
	AppKey string // extracted public API key if found in theme content
}

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

// knownReviewAppCDN maps substrings in theme.liquid content to app IDs.
var knownReviewAppCDN = []struct {
	pattern string
	appID   string
}{
	{"staticw2.yotpo.com", "yotpo"},
	{"cdn-widgetsrepository.yotpo.com", "yotpo"},
	{"cdn.yotpo.com", "yotpo"},
	{"yotpo.com/", "yotpo"},
	{"yotpo-widget-instance", "yotpo"},
	{"data-yotpo-", "yotpo"},
	{"yotpo-widget", "yotpo"},
	{"cdn.judge.me", "judge_me"},
	{"judge.me/", "judge_me"},
	{"judgeme_widgets", "judge_me"},
	{"data-judgeme", "judge_me"},
	{"jdgm-widget", "judge_me"},
	{"jdgm-review-widget", "judge_me"},
	{"metafields.judgeme", "judge_me"},
	{"judgeme_product_reviews", "judge_me"},
	{"cdn.stamped.io", "stamped"},
	{"stamped.io/", "stamped"},
	{"data-stamped", "stamped"},
	{"loox.io/", "loox"},
	{"data-loox", "loox"},
	{"cdn.okendo.io", "okendo"},
	{"okendo.io/", "okendo"},
	{"data-okendo", "okendo"},
	{"cdn.growave.io", "growave"},
	{"growave.io/", "growave"},
	{"cdn.fera.ai", "fera"},
	{"fera.ai/", "fera"},
	{"data-fera", "fera"},
	{"ryviu.com/", "ryviu"},
}

// appKeyPatterns extracts the public API key for each review app from theme content.
// Each pattern must have exactly one capture group containing the key.
var appKeyPatterns = map[string][]*regexp.Regexp{
	"yotpo": {
		// https://staticw2.yotpo.com/APPKEY/widget.js
		regexp.MustCompile(`staticw2\.yotpo\.com/([A-Za-z0-9_-]+)/`),
		// https://cdn-widgetsrepository.yotpo.com/v1/loader/APPKEY
		regexp.MustCompile(`cdn-widgetsrepository\.yotpo\.com/v1/loader/([A-Za-z0-9_-]+)`),
	},
	"judge_me": {
		// https://cdn.judge.me/assets/badge-APPKEY.js
		regexp.MustCompile(`cdn\.judge\.me/[^"']*?-([A-Za-z0-9_-]{20,})`),
	},
	"stamped": {
		// apiKey: "APPKEY"  or  data-api-key="APPKEY"
		regexp.MustCompile(`stamped[^"']*?api[_-]?key['":\s]+([A-Za-z0-9_-]{10,})`),
	},
}

// extractAppKey scans theme.liquid content and returns the API key for the given app.
func extractAppKey(app, content string) string {
	patterns, ok := appKeyPatterns[app]
	if !ok {
		return ""
	}
	for _, re := range patterns {
		m := re.FindStringSubmatch(content)
		if len(m) > 1 && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

// DetectReviewAppFromTheme inspects the active theme to identify the installed
// review app and extract its public API key in a single theme.liquid read.
func DetectReviewAppFromTheme(ctx context.Context, shop, token string) (*ThemeDetectionResult, error) {
	result := &ThemeDetectionResult{}

	// Step 1: get active theme ID.
	const themeQ = `
query ActiveTheme {
  themes(first: 5, roles: [MAIN]) {
    nodes { id name role }
  }
}`
	raw, err := Query(ctx, shop, token, themeQ, nil)
	if err != nil {
		return result, fmt.Errorf("shopify: DetectReviewAppFromTheme themes: %w", err)
	}
	var themeResp struct {
		Themes struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"themes"`
	}
	if err := json.Unmarshal(raw, &themeResp); err != nil || len(themeResp.Themes.Nodes) == 0 {
		return result, nil
	}
	themeID := themeResp.Themes.Nodes[0].ID

	// Step 2: check snippet filenames.
	const filesQ = `
query ThemeSnippets($themeId: ID!) {
  theme(id: $themeId) {
    files(first: 200) {
      nodes { filename }
    }
  }
}`
	raw2, err := Query(ctx, shop, token, filesQ, map[string]any{"themeId": themeID})
	if err == nil {
		var filesResp struct {
			Theme struct {
				Files struct {
					Nodes []struct {
						Filename string `json:"filename"`
					} `json:"nodes"`
				} `json:"files"`
			} `json:"theme"`
		}
		if json.Unmarshal(raw2, &filesResp) == nil {
			for _, f := range filesResp.Theme.Files.Nodes {
				// Check entire filename (any directory) for review app identifiers.
				// Judge.me may only appear in sections/ or templates/, not snippets/.
				nameLower := strings.ToLower(f.Filename)
				for pattern, appID := range knownReviewAppSnippets {
					if strings.Contains(nameLower, pattern) {
						result.App = appID
						break
					}
				}
				if result.App != "" {
					break
				}
			}
		}
	}

	// Step 3: read key template files for inline review app code.
	// Some apps inject directly into templates/product.liquid or layout/theme.liquid
	// rather than adding separate snippet files.
	const contentQ = `
query TemplateLiquidContent($themeId: ID!) {
  theme(id: $themeId) {
    files(filenames: ["layout/theme.liquid", "templates/product.liquid", "sections/main-product.liquid", "sections/product-template.liquid"], first: 10) {
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
		return result, nil
	}
	var contentResp struct {
		Theme struct {
			Files struct {
				Nodes []struct {
					Body struct {
						Content string `json:"content"`
					} `json:"body"`
				} `json:"nodes"`
			} `json:"files"`
		} `json:"theme"`
	}
	if err := json.Unmarshal(raw3, &contentResp); err != nil || len(contentResp.Theme.Files.Nodes) == 0 {
		return result, nil
	}

	// Concatenate all file contents for a single scan pass.
	var combined strings.Builder
	for _, node := range contentResp.Theme.Files.Nodes {
		combined.WriteString(node.Body.Content)
		combined.WriteString("\n")
	}
	content := combined.String()
	contentLower := strings.ToLower(content)

	// If snippet scan didn't find an app, scan content for CDN/widget patterns.
	if result.App == "" {
		for _, m := range knownReviewAppCDN {
			if strings.Contains(contentLower, m.pattern) {
				result.App = m.appID
				break
			}
		}
	}

	// Extract app key from the original (non-lowercased) content to preserve case.
	if result.App != "" {
		result.AppKey = extractAppKey(result.App, content)
	}

	return result, nil
}

// FetchThemeSnippetNames returns all filenames in the active theme.
// Used by the debug endpoint only.
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
