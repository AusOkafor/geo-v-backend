package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yourname/geo-backend/internal/platform"
)

const (
	model    = "gemini-2.0-flash"
	endpoint = "https://generativelanguage.googleapis.com/v1/models/gemini-2.0-flash:generateContent"
	timeout  = 45 * time.Second

	inputCostPer1k  = 0.075 / 1000
	outputCostPer1k = 0.30 / 1000
)

// Client is the Gemini implementation of platform.AIClient.
type Client struct {
	apiKey string
	http   *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		http:   &http.Client{Timeout: timeout},
	}
}

func (c *Client) Name() string                    { return "gemini" }
func (c *Client) InputCostPer1kTokens() float64   { return inputCostPer1k }
func (c *Client) OutputCostPer1kTokens() float64  { return outputCostPer1k }

type generateRequest struct {
	SystemInstruction systemContent `json:"systemInstruction"`
	Contents          []content     `json:"contents"`
	GenerationConfig  genConfig     `json:"generationConfig"`
}

type systemContent struct {
	Parts []part `json:"parts"`
}

type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type genConfig struct {
	ResponseMIMEType string `json:"responseMimeType"`
}

type generateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

type structuredResult struct {
	Mentioned   bool                  `json:"mentioned"`
	Position    int                   `json:"position"`
	Sentiment   string                `json:"sentiment"`
	Competitors []platform.Competitor `json:"competitors"`
}

func systemPrompt(brandName string) string {
	return fmt.Sprintf(`You are an AI search visibility analyst. When answering the user's question, note whether the brand "%s" is mentioned. Return a JSON object:
{"mentioned": bool, "position": int, "sentiment": "positive"|"neutral"|"negative"|"", "competitors": [{"name": string, "position": int}]}
Where "position" is 1 if the brand is the first recommendation, 0 if not mentioned.`, brandName)
}

func (c *Client) Query(ctx context.Context, brandName, prompt string) (platform.CitationResult, error) {
	start := time.Now()

	reqBody := generateRequest{
		SystemInstruction: systemContent{
			Parts: []part{{Text: systemPrompt(brandName)}},
		},
		Contents: []content{
			{Role: "user", Parts: []part{{Text: prompt}}},
		},
		GenerationConfig: genConfig{
			ResponseMIMEType: "application/json",
		},
	}

	payload, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s?key=%s", endpoint, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return platform.CitationResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return platform.CitationResult{}, fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return platform.CitationResult{}, fmt.Errorf("gemini: HTTP %d", resp.StatusCode)
	}

	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return platform.CitationResult{}, fmt.Errorf("gemini: decode: %w", err)
	}

	raw := ""
	if len(genResp.Candidates) > 0 && len(genResp.Candidates[0].Content.Parts) > 0 {
		raw = genResp.Candidates[0].Content.Parts[0].Text
	}

	result := parseResponse(raw, brandName)
	result.Platform = c.Name()
	result.Query = prompt
	result.TokensIn = genResp.UsageMetadata.PromptTokenCount
	result.TokensOut = genResp.UsageMetadata.CandidatesTokenCount
	result.CostUSD = platform.CalcCost(c, result.TokensIn, result.TokensOut)
	result.Duration = time.Since(start)
	result.RawResponse = raw
	return result, nil
}

func parseResponse(raw, brandName string) platform.CitationResult {
	var s structuredResult
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return platform.CitationResult{
			Mentioned:   s.Mentioned,
			Position:    s.Position,
			Sentiment:   s.Sentiment,
			Competitors: s.Competitors,
		}
	}
	mentioned := strings.Contains(strings.ToLower(raw), strings.ToLower(brandName))
	return platform.CitationResult{Mentioned: mentioned}
}
