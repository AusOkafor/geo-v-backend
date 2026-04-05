// Package detection provides regex-based brand mention extraction from AI response text.
// Used by the spot check system to re-validate citation_records after the fact.
package detection

import (
	"regexp"
	"strings"
	"sync"
)

// BrandCitation is a single brand detected in an AI response.
type BrandCitation struct {
	Brand      string  `json:"brand"`
	Confidence float64 `json:"confidence"`
	Position   int     `json:"position"`
	Context    string  `json:"context"`
}

// BrandDetector extracts brand mentions from AI response text using per-brand
// regex patterns. Built from real data (merchant brand + competitor list),
// never hardcoded per category.
type BrandDetector struct {
	mu       sync.RWMutex
	patterns []namedPattern
}

type namedPattern struct {
	canonical string
	re        *regexp.Regexp
}

// New builds a detector for the given merchant brand and competitor list.
// brandName and each competitor get a word-boundary regex that matches
// common spacing variants (e.g. "West Elm", "WestElm", "west elm").
func New(brandName string, competitors []string) *BrandDetector {
	d := &BrandDetector{}
	all := append([]string{brandName}, competitors...)
	for _, name := range all {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Build pattern that matches the brand with optional internal spacing/punctuation
		// e.g. "West Elm" matches "West Elm", "West  Elm", "WestElm"
		words := strings.Fields(name)
		escaped := make([]string, len(words))
		for i, w := range words {
			escaped[i] = regexp.QuoteMeta(w)
		}
		pat := `(?i)\b` + strings.Join(escaped, `[\s\-&]*`) + `\b`
		re, err := regexp.Compile(pat)
		if err != nil {
			continue
		}
		d.patterns = append(d.patterns, namedPattern{canonical: name, re: re})
	}
	return d
}

// ExtractBrands returns all brands found in response, deduplicated by canonical name.
// Confidence is calculated from match context (word boundary, list position, URL presence).
func (d *BrandDetector) ExtractBrands(response string) []BrandCitation {
	d.mu.RLock()
	defer d.mu.RUnlock()

	seen := make(map[string]bool)
	var citations []BrandCitation

	for _, np := range d.patterns {
		loc := np.re.FindStringIndex(response)
		if loc == nil {
			continue
		}
		if seen[np.canonical] {
			continue
		}
		seen[np.canonical] = true

		confidence := d.calcConfidence(response, loc, np.canonical)
		citations = append(citations, BrandCitation{
			Brand:      np.canonical,
			Confidence: confidence,
			Position:   loc[0],
			Context:    extractContext(response, loc[0], 60),
		})
	}
	return citations
}

// BrandNames returns just the canonical brand names detected.
func (d *BrandDetector) BrandNames(response string) []string {
	citations := d.ExtractBrands(response)
	names := make([]string, len(citations))
	for i, c := range citations {
		names[i] = c.Brand
	}
	return names
}

// AddPattern allows the feedback loop to register new patterns at runtime.
func (d *BrandDetector) AddPattern(canonical, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.patterns = append(d.patterns, namedPattern{canonical: canonical, re: re})
	return nil
}

func (d *BrandDetector) calcConfidence(response string, loc []int, brand string) float64 {
	conf := 0.85 // base for regex word-boundary match

	// URL presence: brand appears inside a URL — very strong signal
	urlRe := regexp.MustCompile(`https?://[^\s]*` + regexp.QuoteMeta(strings.ToLower(brand)))
	if urlRe.MatchString(strings.ToLower(response)) {
		conf += 0.10
	}

	// List position: brand appears in a numbered or bulleted list
	listRe := regexp.MustCompile(`(?m)^[\d\-\*•]\s*` + regexp.QuoteMeta(brand))
	if listRe.MatchString(response) {
		conf += 0.05
	}

	// Early in response (first 300 chars) — more likely a primary recommendation
	if loc[0] < 300 {
		conf += 0.02
	}

	if conf > 1.0 {
		conf = 1.0
	}
	return conf
}

func extractContext(response string, pos, window int) string {
	start := pos - window
	if start < 0 {
		start = 0
	}
	end := pos + window
	if end > len(response) {
		end = len(response)
	}
	return response[start:end]
}
