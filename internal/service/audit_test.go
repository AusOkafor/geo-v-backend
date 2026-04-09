package service

import (
	"math"
	"testing"
)

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain text", "hello world", "hello world"},
		{"simple tag", "<p>hello</p>", " hello "},
		{"nested tags", "<div><p>Cotton blend fabric</p></div>", "  Cotton blend fabric  "},
		{"tag with attributes", `<p class="desc">Material: cotton</p>`, " Material: cotton "},
		{"self-closing", "<br/>line", " line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripHTML(tt.input)
			if got != tt.want {
				t.Errorf("StripHTML(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		keywords []string
		want     bool
	}{
		{"match first", "made from cotton", MaterialKeywords, true},
		{"match middle", "polyester blend", MaterialKeywords, true},
		{"case insensitive", "100% WOOL", MaterialKeywords, true},
		{"no match", "great product", MaterialKeywords, false},
		{"empty text", "", MaterialKeywords, false},
		{"empty keywords", "cotton shirt", []string{}, false},
		{"sizing match", "fits true to size", SizingKeywords, true},
		{"care match", "machine wash cold", CareKeywords, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainsAny(tt.text, tt.keywords)
			if got != tt.want {
				t.Errorf("ContainsAny(%q, ...) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestClassifyPage(t *testing.T) {
	tests := []struct {
		title  string
		handle string
		want   string
	}{
		{"FAQ", "faq", "faq"},
		{"Frequently Asked Questions", "frequently-asked-questions", "faq"},
		{"About Us", "about-us", "about"},
		{"Our Story", "our-story", "about"},
		{"Who We Are", "who-we-are", "about"},
		{"Size Guide", "size-guide", "size_guide"},
		{"Sizing", "sizing", "size_guide"},
		{"Fit Guide", "fit-guide", "size_guide"},
		{"Shipping Policy", "shipping-policy", "shipping"},
		{"Delivery Information", "delivery-info", "shipping"},
		{"Returns", "returns", "returns"},
		{"Refund Policy", "refund-policy", "returns"},
		{"Exchange Policy", "exchange-policy", "returns"},
		{"Contact Us", "contact-us", "contact"},
		{"Get in Touch", "get-in-touch", "contact"},
		{"Blog Post", "blog-post", "other"},
		{"", "", "other"},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := ClassifyPage(tt.title, tt.handle)
			if got != tt.want {
				t.Errorf("ClassifyPage(%q, %q) = %q, want %q", tt.title, tt.handle, got, tt.want)
			}
		})
	}
}

func TestDescCompletenessScore(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		want  float64
	}{
		{"empty", "", 0.0},
		{"very short no signals", "great product", 0.1},
		{"short with material", "nice cotton shirt buy now", 0.3},          // <50w → 0.1 + material 0.2
		{"50-79w no signals", buildWords(55), 0.2},                         // 50-79w → 0.2, no signals
		{"80-149w no signals", buildWords(85), 0.3},                        // 80-149w → 0.3
		{"150+w no signals", buildWords(155), 0.4},                         // ≥150w → 0.4
		{"full score", "100% cotton fabric true to size machine wash cold " + buildWords(150), 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescCompletenessScore(tt.text)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("DescCompletenessScore(%q...) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// buildWords returns a string with n space-separated "word" tokens.
func buildWords(n int) string {
	var b []byte
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, "word"...)
	}
	return string(b)
}
