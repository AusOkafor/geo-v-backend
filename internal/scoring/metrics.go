// Package scoring provides precision/recall/F1 calculation for citation detection validation.
package scoring

import "strings"

// Metrics holds the result of comparing manual vs auto-detected brand citations.
type Metrics struct {
	Precision      float64
	Recall         float64
	F1             float64
	TruePositives  int
	FalsePositives int
	FalseNegatives int
}

// Calculate computes Precision, Recall, and F1 score given the ground-truth
// manual brand list and the auto-detected brand list. Comparison is case-insensitive.
// Returns zero-value Metrics (all 0.0) when both sets are empty.
func Calculate(manual, detected []string) Metrics {
	manualSet := toLowerSet(manual)
	detectedSet := toLowerSet(detected)

	tp := 0
	for b := range detectedSet {
		if manualSet[b] {
			tp++
		}
	}
	fp := len(detectedSet) - tp
	fn := len(manualSet) - tp

	var precision, recall, f1 float64
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	if precision+recall > 0 {
		f1 = 2 * (precision * recall) / (precision + recall)
	}

	return Metrics{
		Precision:      precision,
		Recall:         recall,
		F1:             f1,
		TruePositives:  tp,
		FalsePositives: fp,
		FalseNegatives: fn,
	}
}

func toLowerSet(brands []string) map[string]bool {
	s := make(map[string]bool, len(brands))
	for _, b := range brands {
		t := strings.TrimSpace(strings.ToLower(b))
		if t != "" {
			s[t] = true
		}
	}
	return s
}
