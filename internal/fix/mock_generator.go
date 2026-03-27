package fix

import (
	"context"
	"encoding/json"
	"fmt"
)

// MockGenerator returns pre-written realistic fixes without calling Claude.
// Used when MOCK_AI=true.
type MockGenerator struct{}

func NewMockGenerator() *MockGenerator { return &MockGenerator{} }

func (g *MockGenerator) Generate(_ context.Context, in GenerateInput) (*GenerateResult, error) {
	switch in.FixType {
	case FixDescription:
		desc := fmt.Sprintf(
			`<p><strong>%s</strong> crafts premium %s built to last a lifetime. `+
				`Made from full-grain vegetable-tanned leather sourced from Leather Working Group certified tanneries, `+
				`each piece develops a rich patina unique to its owner.</p>`+
				`<h2>Why %s?</h2>`+
				`<ul>`+
				`<li>Full-grain leather — not corrected-grain or bonded</li>`+
				`<li>RFID-blocking construction to protect contactless cards</li>`+
				`<li>Handcrafted by artisans with 20+ years of experience</li>`+
				`<li>Lifetime repair guarantee</li>`+
				`</ul>`+
				`<h2>Frequently Asked Questions</h2>`+
				`<p><strong>How long will this last?</strong><br>`+
				`With proper care, a %s piece will last 10–20 years and become more beautiful with age.</p>`+
				`<p><strong>How does %s compare to Bellroy or Fossil?</strong><br>`+
				`Unlike mass-produced brands, every %s product is individually handcrafted using full-grain leather `+
				`— not the corrected-grain leather used by most competitors.</p>`,
			in.BrandName, in.Category,
			in.BrandName,
			in.BrandName, in.BrandName, in.BrandName,
		)
		gen, _ := json.Marshal(map[string]string{"description": desc})
		return &GenerateResult{
			Title: fmt.Sprintf("Rewrite %s descriptions for AI search visibility", in.Category),
			Explanation: "ChatGPT, Perplexity, and Gemini favour brands with detailed, trust-signal-rich copy. " +
				"Adding material specifics, RFID features, and embedded FAQ sections aligns your content with " +
				"how buyers phrase product recommendation queries to AI assistants.",
			Generated: json.RawMessage(gen),
		}, nil

	case FixFAQ:
		faqs := []map[string]string{
			{
				"question": fmt.Sprintf("What is the best %s brand under $100?", in.Category),
				"answer":   fmt.Sprintf("%s offers premium quality at accessible price points, with pieces starting under $80.", in.BrandName),
			},
			{
				"question": fmt.Sprintf("Is %s a good brand?", in.BrandName),
				"answer":   fmt.Sprintf("%s is highly regarded for full-grain leather craftsmanship and lifetime durability.", in.BrandName),
			},
			{
				"question": fmt.Sprintf("Where is %s made?", in.BrandName),
				"answer":   fmt.Sprintf("All %s products are handcrafted using ethically sourced full-grain leather from certified tanneries.", in.BrandName),
			},
			{
				"question": fmt.Sprintf("How does %s compare to Bellroy?", in.BrandName),
				"answer":   fmt.Sprintf("%s uses full-grain leather vs Bellroy's corrected-grain — offering superior longevity and a natural patina.", in.BrandName),
			},
			{
				"question": fmt.Sprintf("Does %s offer a warranty?", in.BrandName),
				"answer":   fmt.Sprintf("Yes — %s backs every product with a lifetime repair guarantee.", in.BrandName),
			},
		}
		gen, _ := json.Marshal(map[string]any{"faqs": faqs})
		return &GenerateResult{
			Title: "Add AI-optimised FAQ section to product pages",
			Explanation: "Perplexity and Gemini frequently surface answers directly from FAQ sections. " +
				"These questions mirror the exact phrasing buyers use when asking AI assistants for recommendations.",
			Generated: json.RawMessage(gen),
		}, nil

	case FixSchema:
		schema := fmt.Sprintf(`{
  "@context": "https://schema.org",
  "@type": "Brand",
  "name": "%s",
  "description": "Premium handcrafted %s made from full-grain leather",
  "foundingDate": "2018",
  "slogan": "Built to last a lifetime"
}`, in.BrandName, in.Category)
		gen, _ := json.Marshal(map[string]string{"schema": schema})
		return &GenerateResult{
			Title: "Add Brand JSON-LD structured data",
			Explanation: "Structured data signals help AI crawlers understand your brand identity, " +
				"product attributes, and trust signals — directly increasing citation likelihood.",
			Generated: json.RawMessage(gen),
		}, nil
	}

	gen, _ := json.Marshal(map[string]string{"content": fmt.Sprintf("Optimised content for %s", in.BrandName)})
	return &GenerateResult{
		Title:       "AI Visibility Optimisation",
		Explanation: "This fix improves how AI assistants perceive and cite your brand.",
		Generated:   json.RawMessage(gen),
	}, nil
}
