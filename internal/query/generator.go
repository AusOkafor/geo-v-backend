package query

import "fmt"

// QueryType classifies what kind of intent the query tests.
type QueryType string

const (
	TypePriceBracket  QueryType = "price_bracket"
	TypeUseCase       QueryType = "use_case"
	TypeBrand         QueryType = "brand"
	TypeProblem       QueryType = "problem_based"
	TypeComparison    QueryType = "comparison"
	TypeLongTail      QueryType = "long_tail"
)

// Query is a single prompt to be sent to an AI platform.
type Query struct {
	Text      string
	QueryType QueryType
}

// Generate produces ~35 queries for a merchant's category and brand name.
func Generate(category, brandName string) []Query {
	if category == "" {
		category = "products"
	}

	var queries []Query

	// --- Price bracket (5) ---
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("Best %s under $100", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Top %s brands 2026", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Affordable %s that last", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Best value %s under $200", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Premium %s worth the money", category), QueryType: TypePriceBracket},
	}...)

	// --- Use case (5) ---
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("Handmade %s recommendations", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Where to buy %s online", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Best %s for everyday use", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Unique %s gifts", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Sustainable %s brands", category), QueryType: TypeUseCase},
	}...)

	// --- Problem-based (7) ---
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("What to buy when I need %s", category), QueryType: TypeProblem},
		{Text: fmt.Sprintf("Best %s for beginners", category), QueryType: TypeProblem},
		{Text: fmt.Sprintf("Best %s for professionals", category), QueryType: TypeProblem},
		{Text: fmt.Sprintf("Most popular %s right now", category), QueryType: TypeProblem},
		{Text: fmt.Sprintf("Which %s should I buy", category), QueryType: TypeProblem},
		{Text: fmt.Sprintf("Recommended %s for quality", category), QueryType: TypeProblem},
		{Text: fmt.Sprintf("What are the top rated %s", category), QueryType: TypeProblem},
	}...)

	// --- Comparison (5) ---
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("Best %s brands compared", category), QueryType: TypeComparison},
		{Text: fmt.Sprintf("Top 5 %s brands 2026", category), QueryType: TypeComparison},
		{Text: fmt.Sprintf("Best independent %s brands", category), QueryType: TypeComparison},
		{Text: fmt.Sprintf("%s brand rankings", category), QueryType: TypeComparison},
		{Text: fmt.Sprintf("Most trusted %s brands online", category), QueryType: TypeComparison},
	}...)

	// --- Long tail (5) ---
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("Where can I find high quality %s", category), QueryType: TypeLongTail},
		{Text: fmt.Sprintf("Good small business %s brands", category), QueryType: TypeLongTail},
		{Text: fmt.Sprintf("Boutique %s brands worth trying", category), QueryType: TypeLongTail},
		{Text: fmt.Sprintf("Ethical %s brands to support", category), QueryType: TypeLongTail},
		{Text: fmt.Sprintf("Best online stores for %s", category), QueryType: TypeLongTail},
	}...)

	// --- Brand-specific (8, only if brand name provided) ---
	if brandName != "" {
		queries = append(queries, []Query{
			{Text: fmt.Sprintf("%s reviews", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("Is %s worth it", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("%s vs competitors", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("%s quality", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("Should I buy from %s", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("Is %s a good brand", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("%s pros and cons", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("What do people say about %s", brandName), QueryType: TypeBrand},
		}...)
	}

	return queries
}
