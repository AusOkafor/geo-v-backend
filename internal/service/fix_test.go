package service

import (
	"fmt"
	"testing"

	"github.com/austinokafor/geo-backend/internal/fix"
)

// ─── validateFAQPairs ─────────────────────────────────────────────────────────

func TestValidateFAQPairs_Valid(t *testing.T) {
	faqs := faqPairs([][]string{
		{"What is your return policy?", "We accept returns within 30 days of purchase in original condition."},
		{"Do you ship internationally?", "Yes, we ship to over 50 countries via standard courier services."},
	})
	if err := validateFAQPairs(faqs, "Acme Brand"); err != nil {
		t.Errorf("expected valid FAQs to pass, got: %v", err)
	}
}

func TestValidateFAQPairs_BlockedPhrase(t *testing.T) {
	tests := []struct {
		question string
		phrase   string
	}{
		{"Why should I choose Acme Brand?", "why should i choose"},
		{"Why is Acme Brand the best brand?", "best brand"},
		{"What makes Acme the number one store?", "number one"},
		{"Why is Acme the top brand for jewelry?", "top brand"},
	}
	for _, tt := range tests {
		t.Run(tt.phrase, func(t *testing.T) {
			faqs := faqPairs([][]string{
				{tt.question, "This is a sufficient answer with more than five words total here."},
			})
			err := validateFAQPairs(faqs, "Acme Brand")
			if err == nil {
				t.Errorf("expected blocked phrase %q to fail validation, got nil", tt.phrase)
			}
		})
	}
}

func TestValidateFAQPairs_AnswerTooShort(t *testing.T) {
	faqs := faqPairs([][]string{
		{"What materials do you use?", "Cotton only."},
	})
	err := validateFAQPairs(faqs, "TestBrand")
	if err == nil {
		t.Error("expected short answer to fail validation")
	}
}

func TestValidateFAQPairs_AnswerOnlyBrandName(t *testing.T) {
	faqs := faqPairs([][]string{
		{"Who makes these products?", "TestBrand makes these always."},
	})
	err := validateFAQPairs(faqs, "TestBrand")
	if err == nil {
		t.Error("expected brand-name-only answer to fail validation")
	}
}

func TestValidateFAQPairs_Empty(t *testing.T) {
	if err := validateFAQPairs(nil, "Brand"); err != nil {
		t.Errorf("nil slice should be valid, got: %v", err)
	}
}

// ─── blockedQuestionPhrases coverage ─────────────────────────────────────────

func TestBlockedQuestionPhrases_Completeness(t *testing.T) {
	// Verify the list is non-empty and has no duplicates.
	if len(blockedQuestionPhrases) == 0 {
		t.Error("blockedQuestionPhrases must not be empty")
	}
	seen := map[string]bool{}
	for _, p := range blockedQuestionPhrases {
		if seen[p] {
			t.Errorf("duplicate blocked phrase: %q", p)
		}
		seen[p] = true
	}
}

// ─── GenerateFixesResult ──────────────────────────────────────────────────────

func TestGenerateFixesResult_TotalGenerated(t *testing.T) {
	r := &GenerateFixesResult{
		CollectionFixes: 2,
		PageFixes:       1,
		SchemaFixes:     1,
		TotalGenerated:  4,
	}
	// TotalGenerated should equal the sum of specific counts + any non-categorised fixes.
	if r.TotalGenerated < r.CollectionFixes+r.PageFixes+r.SchemaFixes {
		t.Errorf("TotalGenerated (%d) less than sum of sub-counts (%d)",
			r.TotalGenerated, r.CollectionFixes+r.PageFixes+r.SchemaFixes)
	}
}

// ─── EstImpact constants ──────────────────────────────────────────────────────
// These constants are defined in the fix package and used by FixService when
// inserting fixes. Verify they match expected ordering (no regression).

func TestImpactScores_Ordering(t *testing.T) {
	scores := map[fix.FixType]int{
		fix.FixCollectionDescription: fix.EstImpact(fix.FixCollectionDescription),
		fix.FixFAQ:                   fix.EstImpact(fix.FixFAQ),
		fix.FixMerchantCenter:        fix.EstImpact(fix.FixMerchantCenter),
		fix.FixAboutPage:             fix.EstImpact(fix.FixAboutPage),
		fix.FixSizeGuide:             fix.EstImpact(fix.FixSizeGuide),
		fix.FixSchema:                fix.EstImpact(fix.FixSchema),
	}

	// Every registered fix type must have a non-zero impact.
	for ft, score := range scores {
		if score <= 0 {
			t.Errorf("fix type %q has zero or negative impact score", ft)
		}
	}

	// Collection descriptions should have the highest impact (they're the main
	// content signal; regression here would change fix priority ordering).
	if scores[fix.FixCollectionDescription] <= scores[fix.FixAboutPage] {
		t.Errorf("collection description impact (%d) should be > about page (%d)",
			scores[fix.FixCollectionDescription], scores[fix.FixAboutPage])
	}
	if scores[fix.FixCollectionDescription] <= scores[fix.FixSchema] {
		t.Errorf("collection description impact (%d) should be > schema (%d)",
			scores[fix.FixCollectionDescription], scores[fix.FixSchema])
	}
}

func TestImpactScores_FAQHigherThanSizeGuide(t *testing.T) {
	faqScore := fix.EstImpact(fix.FixFAQ)
	sizeScore := fix.EstImpact(fix.FixSizeGuide)
	if faqScore <= sizeScore {
		t.Errorf("FAQ impact (%d) should be > size guide impact (%d)", faqScore, sizeScore)
	}
}

// ─── unmarshalFixJSON ─────────────────────────────────────────────────────────

func TestUnmarshalFixJSON_Valid(t *testing.T) {
	var v struct{ Name string }
	if err := unmarshalFixJSON([]byte(`{"name":"test"}`), &v); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if v.Name != "test" {
		t.Errorf("got %q, want %q", v.Name, "test")
	}
}

func TestUnmarshalFixJSON_Empty(t *testing.T) {
	var v struct{ Name string }
	if err := unmarshalFixJSON(nil, &v); err == nil {
		t.Error("expected error for nil input")
	}
	if err := unmarshalFixJSON([]byte{}, &v); err == nil {
		t.Error("expected error for empty input")
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func faqPairs(pairs [][]string) []struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
} {
	out := make([]struct {
		Question string `json:"question"`
		Answer   string `json:"answer"`
	}, len(pairs))
	for i, p := range pairs {
		if len(p) != 2 {
			panic(fmt.Sprintf("faqPairs: pair %d must have exactly 2 elements", i))
		}
		out[i].Question = p[0]
		out[i].Answer = p[1]
	}
	return out
}
