// Package verification provides the Citation Verifier — an admin-only tool that
// re-queries AI platforms to validate stored citation records, detect hallucinated
// brands, measure cross-platform consistency, and track response drift over time.
package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/austinokafor/geo-backend/internal/platform"
)

// Verifier holds the platform clients needed to re-run AI queries.
type Verifier struct {
	clients map[string]platform.AIClient
	db      *pgxpool.Pool
}

// New creates a Verifier from a slice of AIClient implementations.
func New(clients []platform.AIClient, db *pgxpool.Pool) *Verifier {
	m := make(map[string]platform.AIClient, len(clients))
	for _, c := range clients {
		m[c.Name()] = c
	}
	return &Verifier{clients: m, db: db}
}

// ─────────────────────────────────────────────────────────────────────────────
// Public types
// ─────────────────────────────────────────────────────────────────────────────

// VerificationResult is the full output of a re-query verification run.
type VerificationResult struct {
	CitationRecordID int64              `json:"citation_record_id"`
	MerchantID       int64              `json:"merchant_id"`
	Query            string             `json:"query"`
	Platform         string             `json:"platform"`
	OriginalResponse string             `json:"original_response"`
	ReQueryResponse  string             `json:"re_query_response"`
	SimilarityScore  float64            `json:"similarity_score"`
	ResponseChanged  bool               `json:"response_changed"`
	Hallucinations   []HallucinationFlag `json:"hallucinations"`
	HallucinationCount int              `json:"hallucination_count"`
	IsAuthentic      bool               `json:"is_authentic"`
	// StoredID is set after the result is persisted to citation_verifications.
	StoredID int64 `json:"id,omitempty"`
}

// HallucinationFlag marks a brand that has little or no evidence in our DB.
type HallucinationFlag struct {
	Brand       string `json:"brand"`
	Occurrences int    `json:"occurrences"` // # of citation_records rows that mention this brand
	Reason      string `json:"reason"`
}

// CrossPlatformResult is the output of running the same query on all platforms.
type CrossPlatformResult struct {
	Query            string                     `json:"query"`
	MerchantID       int64                      `json:"merchant_id"`
	ConsistencyScore float64                    `json:"consistency_score"` // 0–1
	SharedBrands     []string                   `json:"shared_brands"`     // cited on 2+ platforms
	Platforms        map[string]PlatformResult  `json:"platforms"`
}

// PlatformResult holds the live result from one platform for a cross-platform run.
type PlatformResult struct {
	Brands    []string `json:"brands"`
	Mentioned bool     `json:"mentioned"`
	Response  string   `json:"response"`
	Error     string   `json:"error,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// VerifyCitation: re-queries the original platform, computes similarity + hallucinations
// ─────────────────────────────────────────────────────────────────────────────

// VerifyCitation re-runs the original query against the same platform and
// compares the new response to the stored one.
func (v *Verifier) VerifyCitation(ctx context.Context, citationRecordID, merchantID int64) (*VerificationResult, error) {
	// 1. Fetch the original record.
	var (
		query        string
		plt          string
		answerText   *string
		brandName    string
		competJSON   []byte
	)
	err := v.db.QueryRow(ctx, `
		SELECT cr.query, cr.platform, cr.answer_text, m.brand_name, cr.competitors
		FROM citation_records cr
		JOIN merchants m ON m.id = cr.merchant_id
		WHERE cr.id = $1 AND cr.merchant_id = $2
	`, citationRecordID, merchantID).Scan(&query, &plt, &answerText, &brandName, &competJSON)
	if err != nil {
		return nil, fmt.Errorf("verification: fetch record: %w", err)
	}

	original := ""
	if answerText != nil {
		original = *answerText
	}

	// 2. Find the right client.
	client, ok := v.clients[plt]
	if !ok {
		return nil, fmt.Errorf("verification: no client for platform %q", plt)
	}

	// 3. Re-query.
	slog.Info("verification: re-querying", "platform", plt, "query", query[:min(len(query), 60)])
	reResult, err := client.Query(ctx, brandName, query)
	if err != nil {
		return nil, fmt.Errorf("verification: re-query failed: %w", err)
	}

	// 4. Similarity (Jaccard on word bags).
	sim := jaccardSimilarity(original, reResult.AnswerText)

	// 5. Hallucination check — brands in new response with low DB evidence.
	newBrands := competitorNames(reResult.Competitors)
	hallucinations, err := v.checkHallucinations(ctx, newBrands)
	if err != nil {
		slog.Warn("verification: hallucination check failed", "err", err)
		hallucinations = nil
	}

	result := &VerificationResult{
		CitationRecordID:   citationRecordID,
		MerchantID:         merchantID,
		Query:              query,
		Platform:           plt,
		OriginalResponse:   original,
		ReQueryResponse:    reResult.AnswerText,
		SimilarityScore:    sim,
		ResponseChanged:    sim < 0.85,
		Hallucinations:     hallucinations,
		HallucinationCount: len(hallucinations),
		IsAuthentic:        sim >= 0.75 && len(hallucinations) == 0,
	}

	// 6. Persist.
	storedID, err := v.storeVerification(ctx, result)
	if err != nil {
		slog.Warn("verification: persist failed", "err", err)
	} else {
		result.StoredID = storedID
	}

	// 7. Update stability tracker.
	if uErr := v.upsertStability(ctx, merchantID, query, plt, sim); uErr != nil {
		slog.Warn("verification: stability upsert failed", "err", uErr)
	}

	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CrossPlatform: same query, all three platforms, concurrently
// ─────────────────────────────────────────────────────────────────────────────

// CrossPlatform runs the same query on all configured platform clients
// concurrently and returns per-platform results plus a consistency score.
func (v *Verifier) CrossPlatform(ctx context.Context, query, brandName string, merchantID int64) *CrossPlatformResult {
	type entry struct {
		name   string
		result PlatformResult
	}

	ch := make(chan entry, len(v.clients))
	var wg sync.WaitGroup

	for name, client := range v.clients {
		wg.Add(1)
		go func(name string, c platform.AIClient) {
			defer wg.Done()
			r, err := c.Query(ctx, brandName, query)
			pr := PlatformResult{
				Brands:    competitorNames(r.Competitors),
				Mentioned: r.Mentioned,
				Response:  r.AnswerText,
			}
			if err != nil {
				pr.Error = err.Error()
			}
			ch <- entry{name: name, result: pr}
		}(name, client)
	}

	wg.Wait()
	close(ch)

	platforms := make(map[string]PlatformResult)
	for e := range ch {
		platforms[e.name] = e.result
	}

	shared, score := consistencyScore(platforms)

	return &CrossPlatformResult{
		Query:            query,
		MerchantID:       merchantID,
		ConsistencyScore: score,
		SharedBrands:     shared,
		Platforms:        platforms,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Hallucination detection
// ─────────────────────────────────────────────────────────────────────────────

// checkHallucinations cross-references each brand name against our citation_records
// database. Brands with very few occurrences (< 3) are flagged as potentially
// unverified — they may be hallucinated by the AI.
func (v *Verifier) checkHallucinations(ctx context.Context, brands []string) ([]HallucinationFlag, error) {
	if len(brands) == 0 {
		return nil, nil
	}

	var flags []HallucinationFlag
	for _, brand := range brands {
		if brand == "" {
			continue
		}
		var count int
		err := v.db.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM citation_records
			WHERE competitors @> jsonb_build_array(jsonb_build_object('name', $1::text))
		`, brand).Scan(&count)
		if err != nil {
			continue
		}
		if count < 3 {
			flags = append(flags, HallucinationFlag{
				Brand:       brand,
				Occurrences: count,
				Reason:      fmt.Sprintf("Only %d evidence record(s) in database — may be AI-fabricated", count),
			})
		}
	}
	return flags, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Persistence helpers
// ─────────────────────────────────────────────────────────────────────────────

func (v *Verifier) storeVerification(ctx context.Context, r *VerificationResult) (int64, error) {
	// Ensure nil slice marshals to "[]" not "null" — pgx v5 passes []byte as bytea,
	// so we marshal to string so PostgreSQL receives valid JSONB text.
	flags := r.Hallucinations
	if flags == nil {
		flags = []HallucinationFlag{}
	}
	hallJSON, _ := json.Marshal(flags)

	// Use a fresh context for the DB write so a slow AI call can't expire the
	// context before persistence completes.
	dbCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var id int64
	err := v.db.QueryRow(dbCtx, `
		INSERT INTO citation_verifications (
			citation_record_id, merchant_id,
			original_query, original_platform, original_response,
			re_query_response, similarity_score, response_changed,
			hallucination_flags, hallucination_count,
			is_authentic
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id
	`,
		r.CitationRecordID, r.MerchantID,
		r.Query, r.Platform, r.OriginalResponse,
		r.ReQueryResponse, r.SimilarityScore, r.ResponseChanged,
		string(hallJSON), r.HallucinationCount,
		r.IsAuthentic,
	).Scan(&id)
	return id, err
}

func (v *Verifier) upsertStability(_ context.Context, merchantID int64, query, plt string, sim float64) error {
	dbCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := v.db.Exec(dbCtx, `
		INSERT INTO response_stability
			(merchant_id, query_text, platform, avg_similarity, min_similarity, check_count, last_checked_at, drift_detected)
		VALUES ($1, $2, $3, $4, $4, 1, NOW(), $4 < 0.75)
		ON CONFLICT (merchant_id, query_text, platform) DO UPDATE SET
			avg_similarity  = (response_stability.avg_similarity * response_stability.check_count + $4)
			                  / (response_stability.check_count + 1),
			min_similarity  = LEAST(response_stability.min_similarity, $4),
			check_count     = response_stability.check_count + 1,
			last_checked_at = NOW(),
			drift_detected  = ((response_stability.avg_similarity * response_stability.check_count + $4)
			                  / (response_stability.check_count + 1)) < 0.75
	`, merchantID, query, plt, sim)
	return err
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure functions
// ─────────────────────────────────────────────────────────────────────────────

// jaccardSimilarity computes the Jaccard index (word-set intersection / union)
// between two response strings. Returns 1.0 for identical texts, 0.0 for no overlap.
func jaccardSimilarity(a, b string) float64 {
	setA := wordSet(a)
	setB := wordSet(b)

	if len(setA) == 0 && len(setB) == 0 {
		return 1.0
	}

	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}

func wordSet(text string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		// Strip common punctuation.
		w = strings.Trim(w, ".,!?;:\"'()-[]")
		if len(w) > 2 {
			set[w] = true
		}
	}
	return set
}

// consistencyScore returns brands that appear on 2+ platforms and the
// fraction of total unique brands that are cross-platform (0–1).
func consistencyScore(platforms map[string]PlatformResult) ([]string, float64) {
	// Count occurrences of each brand across platforms.
	freq := make(map[string]int)
	for _, pr := range platforms {
		seen := make(map[string]bool)
		for _, b := range pr.Brands {
			bl := strings.ToLower(b)
			if !seen[bl] {
				freq[bl]++
				seen[bl] = true
			}
		}
	}

	total := len(freq)
	if total == 0 {
		return nil, 0
	}

	var shared []string
	crossCount := 0
	for b, count := range freq {
		if count >= 2 {
			shared = append(shared, b)
			crossCount++
		}
	}
	return shared, float64(crossCount) / float64(total)
}

func competitorNames(cs []platform.Competitor) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Name != "" {
			out = append(out, c.Name)
		}
	}
	return out
}

