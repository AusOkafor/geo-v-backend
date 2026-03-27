package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	apiVersion = "2025-01"
	gqlTimeout = 30 * time.Second
)

func gqlEndpoint(shop string) string {
	return fmt.Sprintf("https://%s/admin/api/%s/graphql.json", shop, apiVersion)
}

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlResponse struct {
	Data   json.RawMessage  `json:"data"`
	Errors []gqlError       `json:"errors"`
}

type gqlError struct {
	Message string `json:"message"`
}

// Query executes a raw GraphQL query/mutation and returns the data field.
func Query(ctx context.Context, shop, token, query string, variables map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(gqlRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, gqlTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gqlEndpoint(shop), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shopify graphql: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shopify graphql: HTTP %d", resp.StatusCode)
	}

	var gqlResp gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return nil, fmt.Errorf("shopify graphql: decode: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("shopify graphql: %s", gqlResp.Errors[0].Message)
	}
	return gqlResp.Data, nil
}

// ProductNode is the shape of a Shopify product returned by FetchAllProducts.
type ProductNode struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	DescriptionHTML string   `json:"descriptionHtml"`
	Tags            []string `json:"tags"`
	Status          string   `json:"status"`
	PriceMin        string   `json:"priceMin"`
	PriceMax        string   `json:"priceMax"`
	SEOTitle        string   `json:"seoTitle"`
	SEODescription  string   `json:"seoDescription"`
}

const fetchProductsQuery = `
query FetchProducts($cursor: String) {
  products(first: 250, after: $cursor) {
    pageInfo { hasNextPage endCursor }
    edges {
      node {
        id
        title
        descriptionHtml
        tags
        status
        priceRangeV2 {
          minVariantPrice { amount }
          maxVariantPrice { amount }
        }
        seo { title description }
      }
    }
  }
}`

type productEdge struct {
	Node struct {
		ID              string   `json:"id"`
		Title           string   `json:"title"`
		DescriptionHTML string   `json:"descriptionHtml"`
		Tags            []string `json:"tags"`
		Status          string   `json:"status"`
		PriceRangeV2    struct {
			MinVariantPrice struct{ Amount string `json:"amount"` } `json:"minVariantPrice"`
			MaxVariantPrice struct{ Amount string `json:"amount"` } `json:"maxVariantPrice"`
		} `json:"priceRangeV2"`
		SEO struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"seo"`
	} `json:"node"`
}

type fetchProductsData struct {
	Products struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []productEdge `json:"edges"`
	} `json:"products"`
}

// FetchAllProducts returns every product in the store using cursor pagination.
func FetchAllProducts(ctx context.Context, shop, token string) ([]ProductNode, error) {
	var products []ProductNode
	var cursor *string

	for {
		vars := map[string]any{}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		raw, err := Query(ctx, shop, token, fetchProductsQuery, vars)
		if err != nil {
			return nil, err
		}

		var data fetchProductsData
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("shopify: parse products page: %w", err)
		}

		for _, edge := range data.Products.Edges {
			n := edge.Node
			products = append(products, ProductNode{
				ID:              n.ID,
				Title:           n.Title,
				DescriptionHTML: n.DescriptionHTML,
				Tags:            n.Tags,
				Status:          n.Status,
				PriceMin:        n.PriceRangeV2.MinVariantPrice.Amount,
				PriceMax:        n.PriceRangeV2.MaxVariantPrice.Amount,
				SEOTitle:        n.SEO.Title,
				SEODescription:  n.SEO.Description,
			})
		}

		if !data.Products.PageInfo.HasNextPage {
			break
		}
		c := data.Products.PageInfo.EndCursor
		cursor = &c
	}

	return products, nil
}

const updateDescriptionMutation = `
mutation UpdateDescription($id: ID!, $input: ProductInput!) {
  productUpdate(id: $id, input: $input) {
    product { id }
    userErrors { field message }
  }
}`

type userError struct {
	Field   []string `json:"field"`
	Message string   `json:"message"`
}

// UpdateDescription sets the descriptionHtml of a product via the productUpdate mutation.
func UpdateDescription(ctx context.Context, shop, token, productGID, newHTML string) error {
	vars := map[string]any{
		"id":    productGID,
		"input": map[string]any{"descriptionHtml": newHTML},
	}

	raw, err := Query(ctx, shop, token, updateDescriptionMutation, vars)
	if err != nil {
		return err
	}

	var resp struct {
		ProductUpdate struct {
			UserErrors []userError `json:"userErrors"`
		} `json:"productUpdate"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("shopify: parse productUpdate response: %w", err)
	}
	if len(resp.ProductUpdate.UserErrors) > 0 {
		return fmt.Errorf("shopify: productUpdate userError: %s", resp.ProductUpdate.UserErrors[0].Message)
	}
	return nil
}
