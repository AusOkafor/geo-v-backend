package fix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	claudeEndpoint = "https://api.anthropic.com/v1/messages"
	claudeModel    = "claude-haiku-4-5-20251001"
	claudeVersion  = "2023-06-01"
	fixTimeout     = 60 * time.Second
)

// FixType enumerates the kinds of fixes Claude can generate.
type FixType string

const (
	FixDescription          FixType = "description"           // deprecated — product descriptions only
	FixFAQ                  FixType = "faq"
	FixSchema               FixType = "schema"
	FixListing              FixType = "listing"
	FixCollectionDescription FixType = "collection_description" // AI-generated collection intro
	FixAboutPage            FixType = "about_page"             // About Us template
	FixSizeGuide            FixType = "size_guide"             // Size guide template
)

// EstImpact returns the estimated visibility improvement for each fix type.
func EstImpact(t FixType) int {
	switch t {
	case FixCollectionDescription:
		return 25
	case FixFAQ:
		return 18
	case FixAboutPage:
		return 15
	case FixSizeGuide:
		return 12
	case FixListing:
		return 12
	case FixSchema:
		return 8
	case FixDescription:
		return 23
	}
	return 5
}

// GenerateInput contains everything Claude needs to generate a fix.
type GenerateInput struct {
	BrandName          string
	Category           string
	ProductTitle       string // set for per-product description fixes
	CurrentDescription string
	Tags               []string
	Competitors        []string
	FixType            FixType
	// QueryGaps are the specific queries the merchant is missing from in AI results.
	// Injected so generated content directly targets real missed buyer intent.
	QueryGaps []string
	// Collection-specific fields
	CollectionTitle        string
	CollectionProductCount int
	// Page-specific fields
	PageType string // about_page | size_guide (faq uses FixFAQ)
}

// GenerateResult is the parsed output from Claude.
type GenerateResult struct {
	Generated  json.RawMessage
	Title      string
	Explanation string
}

// claudeRequest is the Anthropic Messages API request body.
type claudeRequest struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	System    string           `json:"system"`
	Messages  []claudeMessage  `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// FAQSuggestion is a single AI-suggested FAQ pair for merchant review.
type FAQSuggestion struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Generatable is implemented by both Generator (real) and MockGenerator (dev/staging).
type Generatable interface {
	Generate(ctx context.Context, in GenerateInput) (*GenerateResult, error)
}

// Generator calls the Claude API to produce optimization fixes.
type Generator struct {
	apiKey string
	http   *http.Client
}

func NewGenerator(apiKey string) *Generator {
	return &Generator{
		apiKey: apiKey,
		http:   &http.Client{Timeout: fixTimeout},
	}
}

const systemPrompt = `You are a GEO (Generative Engine Optimization) expert. Your job is to rewrite or generate content that makes e-commerce brands more likely to be cited when shoppers ask AI assistants for product recommendations. Return ONLY valid JSON — no markdown, no prose.`

func (g *Generator) Generate(ctx context.Context, in GenerateInput) (*GenerateResult, error) {
	userPrompt := buildPrompt(in)

	reqBody := claudeRequest{
		Model:     claudeModel,
		MaxTokens: 2048,
		System:    systemPrompt,
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", g.apiKey)
	req.Header.Set("anthropic-version", claudeVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fix: claude call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fix: claude HTTP %d", resp.StatusCode)
	}

	var claudeResp claudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return nil, fmt.Errorf("fix: decode claude response: %w", err)
	}

	raw := ""
	if len(claudeResp.Content) > 0 {
		raw = claudeResp.Content[0].Text
	}

	return parseResult(raw, in.FixType)
}

func buildPrompt(in GenerateInput) string {
	gapSection := ""
	if len(in.QueryGaps) > 0 {
		gapSection = "\n\nQueries AI was asked where this brand did NOT appear (target these specifically):\n"
		for i, q := range in.QueryGaps {
			if i >= 10 {
				break
			}
			gapSection += fmt.Sprintf("- %s\n", q)
		}
	}

	switch in.FixType {
	case FixDescription:
		productRef := in.ProductTitle
		if productRef == "" {
			productRef = in.BrandName
		}
		descSection := ""
		if strings.TrimSpace(in.CurrentDescription) == "" {
			descSection = "There is currently NO description for this product. Write one from scratch using only the product title and tags provided — do not invent features or claims beyond what the tags imply."
		} else {
			descSection = fmt.Sprintf("Current description: %s", in.CurrentDescription)
		}
		return fmt.Sprintf(`Generate an optimized product description for "%s" by brand "%s" (category: %s).

%s
Tags: %v
Top competitors AI cites instead: %v%s

The description must naturally cover the buyer intents behind the missed queries — not by stuffing keywords, but by actually answering what a shopper would want to know.

Requirements:
- 600-900 words, HTML format with <h2> and <p> tags
- Cover: what the product is, who it's for, key materials/features, use cases, fit or sizing guidance if relevant
- Write in a natural brand voice — do NOT repeat the brand name more than twice
- Do NOT mention SEO, keyword density, AI optimization, or visibility tactics
- Do NOT invent certifications, materials, or claims that aren't verifiable from the product title or tags
- The explanation field must describe what the description covers and why it helps buyers find the product — never reference internal tactics like "brand name repetition" or "semantic structure"

Return JSON: {"title": "Fix title", "explanation": "What this description covers and how it helps shoppers find the right product", "generated": {"description": "HTML product description"}}`,
			productRef, in.BrandName, in.Category, descSection, in.Tags, in.Competitors, gapSection)

	case FixFAQ:
		return fmt.Sprintf(`Generate 10 FAQ Q&A pairs for brand "%s" (category: %s) that a real buyer would ask about this store.

Focus exclusively on factual, policy-based topics:
- Shipping: delivery times, carriers, international availability
- Returns: return window, process, conditions
- Materials: what the products are made of, any relevant certifications
- Sizing: how to choose, fit guidance, size charts
- Care: how to clean or maintain the products
- Products: what types they sell, key features

Question rules:
- Neutral, practical questions only — never "Why is [brand] the best?" or "Why should I choose [brand]?"
- Questions a first-time buyer would actually type into Google or ask an AI
- Do NOT use superlatives: no "best", "top", "leading", "standout"

Answer rules:
- 2–3 sentences, factual and specific
- Mention the brand name once at most
- No unverifiable claims: no "ethically-sourced", "investment-grade", "lasts decades" unless provably true
- No price ranges — prices may change
- Minimum 20 words per answer

Return JSON: {"title": "Fix title", "explanation": "Why factual FAQs improve AI visibility", "generated": {"faqs": [{"question": "...", "answer": "..."}]}}`,
			in.BrandName, in.Category)

	case FixSchema:
		return fmt.Sprintf(`Write a brand description for "%s" (category: %s) to be embedded in JSON-LD structured data.%s

Requirements:
- 120–160 words maximum — factual, natural prose
- Cover: materials, use cases (gifts, everyday wear), and what makes the brand distinct — without inflating claims
- Address the buyer intent behind the missed queries, but do NOT repeat any phrase more than once
- Use descriptive language only: "offers", "focuses on", "designed for", "known for" — never superlatives like "best", "standout", "top brand"
- Do NOT include any price range or pricing claims — prices vary and will be read directly from live product data
- Do NOT mention certifications, sourcing transparency, warranties, or durability claims unless they are verifiable facts for this brand
- Do NOT mention any suppliers, third-party brand names, or wholesale vendors

Return JSON: {"title": "Fix title", "explanation": "Why structured schema markup improves AI visibility", "generated": {"brand_description": "120-160 word factual brand description"}}`,
			in.BrandName, in.Category, gapSection)

	case FixListing:
		return fmt.Sprintf(`Generate directory listing content for brand "%s" (category: %s) suitable for submission to niche directories and editorial sites.%s

The listing copy should answer the missed queries above so that when directories index this content, AI can find it.

Return JSON: {"title": "Fix title", "explanation": "Why this improves AI visibility", "generated": {"listing": "..."}}`,
			in.BrandName, in.Category, gapSection)

	case FixCollectionDescription:
		return fmt.Sprintf(`Write a collection description for "%s", a collection of %d products from the brand "%s" (category: %s).%s

Requirements:
- 80–120 words, plain HTML with <p> tags only — no headers
- Introduce what the collection contains and who it is for
- Use natural, browsable language — not keyword stuffing
- Do NOT mention specific product names, prices, or availability
- Do NOT use superlatives: no "best", "top", "premium", "exclusive"
- Do NOT mention the brand name more than once

Return JSON: {"title": "Add description to %s collection", "explanation": "Collection descriptions help AI cite your category pages when shoppers ask for product recommendations", "generated": {"description": "<p>...</p>"}}`,
			in.CollectionTitle, in.CollectionProductCount, in.BrandName, in.Category, gapSection, in.CollectionTitle)

	case FixAboutPage:
		return fmt.Sprintf(`Write an About Us page template for brand "%s" (category: %s).%s

Requirements:
- 200–300 words total across 3 sections
- Section 1 "Our Story": origin, motivation, founding moment — use [brackets] for facts the merchant must fill in (e.g. "[year founded]", "[city]")
- Section 2 "What We Make": describe the product category and what makes the craft distinct — keep generic enough that the merchant only needs to edit a few details
- Section 3 "Our Values": 2–3 sentences on quality, customer focus, or community — no unverifiable sustainability claims
- Format: HTML with <h2> and <p> tags
- Do NOT claim certifications, sourcing transparency, or specific awards

Return JSON: {"title": "Add an About Us page to build brand trust", "explanation": "An About page helps AI assistants verify the brand is real and establish trust when recommending it", "generated": {"content": "<h2>Our Story</h2><p>...</p>"}}`,
			in.BrandName, in.Category, gapSection)

	case FixSizeGuide:
		return fmt.Sprintf(`Write a size guide template for brand "%s" (category: %s).

Requirements:
- Provide a measurement guide section explaining how to measure chest, waist, and hips (or equivalent for the category)
- Include a simple HTML table with columns: Size | Chest | Waist | Hips (or relevant measurements for %s)
- Add a brief note that sizes vary and to contact the brand if unsure
- Format: HTML with <h2>, <p>, and <table> tags
- Use placeholder values like "34–36 in" so the merchant edits real measurements — do NOT invent precise numbers
- 150–250 words total

Return JSON: {"title": "Add a size guide to reduce returns and improve AI recommendations", "explanation": "A size guide helps AI assistants answer sizing questions and recommend your products with confidence", "generated": {"content": "<h2>How to Measure</h2><p>...</p><table>...</table>"}}`,
			in.BrandName, in.Category, in.Category)
	}

	return fmt.Sprintf(`Improve AI visibility for brand "%s". Return JSON with title, explanation, and generated fields.`, in.BrandName)
}

type resultJSON struct {
	Title       string          `json:"title"`
	Explanation string          `json:"explanation"`
	Generated   json.RawMessage `json:"generated"`
}

func stripMarkdown(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json ... ``` or ``` ... ``` fences
	if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx+3:]
		s = strings.TrimPrefix(s, "json")
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
	}
	return strings.TrimSpace(s)
}

func parseResult(raw string, _ FixType) (*GenerateResult, error) {
	cleaned := stripMarkdown(raw)
	var r resultJSON
	if err := json.Unmarshal([]byte(cleaned), &r); err != nil {
		// Return raw as generated if JSON parsing fails
		return &GenerateResult{
			Title:       "AI-generated optimization",
			Explanation: "Generated by Claude",
			Generated:   json.RawMessage(`{"raw":` + jsonString(raw) + `}`),
		}, nil
	}
	return &GenerateResult{
		Title:       r.Title,
		Explanation: r.Explanation,
		Generated:   r.Generated,
	}, nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// SuggestFAQs generates 5 neutral, policy-based FAQ suggestions for merchant review.
// These are safe topics (shipping, returns, materials, sizing, care) that AI assistants
// treat as trustworthy signals rather than self-promotional content.
func (g *Generator) SuggestFAQs(ctx context.Context, brandName, category string) ([]FAQSuggestion, error) {
	prompt := fmt.Sprintf(`Generate exactly 5 FAQ Q&A pairs for a %s brand called "%s".

Topics to cover (pick the 5 most relevant):
- Shipping times and delivery options
- Return and exchange policy
- Materials used in products
- Sizing or fit guidance
- Product care instructions
- International shipping availability
- Payment options accepted

Rules:
- Questions must be neutral and practical (what a first-time buyer would ask)
- Answers must be 1–2 sentences, factual placeholders the merchant can edit
- Use "[X days]", "[X]", "[material]" as placeholders where real data is needed
- No brand promotion, no superlatives, no price claims

Return JSON: {"suggestions": [{"question": "...", "answer": "..."}]}`,
		category, brandName)

	reqBody := claudeRequest{
		Model:     claudeModel,
		MaxTokens: 1024,
		System:    `You generate neutral, factual FAQ suggestions for e-commerce merchants. Return ONLY valid JSON — no markdown, no prose.`,
		Messages:  []claudeMessage{{Role: "user", Content: prompt}},
	}

	payload, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", g.apiKey)
	req.Header.Set("anthropic-version", claudeVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fix: SuggestFAQs claude call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fix: SuggestFAQs claude HTTP %d", resp.StatusCode)
	}

	var claudeResp claudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return nil, fmt.Errorf("fix: SuggestFAQs decode: %w", err)
	}
	raw := ""
	if len(claudeResp.Content) > 0 {
		raw = claudeResp.Content[0].Text
	}

	cleaned := stripMarkdown(raw)
	var result struct {
		Suggestions []FAQSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("fix: SuggestFAQs parse: %w", err)
	}
	return result.Suggestions, nil
}
