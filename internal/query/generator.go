package query

import "fmt"

// QueryType classifies what kind of intent the query tests.
type QueryType string

const (
	TypePriceBracket QueryType = "price_bracket"
	TypeUseCase      QueryType = "use_case"
	TypeBrand        QueryType = "brand"
)

// Query is a single prompt to be sent to an AI platform.
type Query struct {
	Text      string
	QueryType QueryType
}

// Generate produces ~15 queries for a merchant's category and brand name.
func Generate(category, brandName string) []Query {
	price := "100"
	if category == "" {
		category = "products"
	}

	var queries []Query

	// Price bracket queries
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("Best %s under $%s", category, price), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Top %s brands 2026", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Affordable %s that last", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Best value %s under $200", category), QueryType: TypePriceBracket},
		{Text: fmt.Sprintf("Premium %s worth the money", category), QueryType: TypePriceBracket},
	}...)

	// Use case queries
	queries = append(queries, []Query{
		{Text: fmt.Sprintf("Handmade %s recommendations", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Where to buy %s online", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Best %s for everyday use", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Unique %s gifts", category), QueryType: TypeUseCase},
		{Text: fmt.Sprintf("Sustainable %s brands", category), QueryType: TypeUseCase},
	}...)

	// Brand-specific queries
	if brandName != "" {
		queries = append(queries, []Query{
			{Text: fmt.Sprintf("%s reviews", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("Is %s worth it", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("%s vs competitors", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("%s quality", brandName), QueryType: TypeBrand},
			{Text: fmt.Sprintf("Should I buy from %s", brandName), QueryType: TypeBrand},
		}...)
	}

	return queries
}
