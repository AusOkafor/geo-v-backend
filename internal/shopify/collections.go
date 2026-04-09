package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// ── Collection mutations ───────────────────────────────────────────────────────

const updateCollectionDescriptionMutation = `
mutation UpdateCollection($input: CollectionInput!) {
  collectionUpdate(input: $input) {
    collection { id }
    userErrors { field message }
  }
}`

// UpdateCollectionDescription sets the descriptionHtml of a collection.
func UpdateCollectionDescription(ctx context.Context, shop, token, collectionGID, descriptionHTML string) error {
	vars := map[string]any{
		"input": map[string]any{
			"id":              collectionGID,
			"descriptionHtml": descriptionHTML,
		},
	}
	raw, err := Query(ctx, shop, token, updateCollectionDescriptionMutation, vars)
	if err != nil {
		return err
	}
	var resp struct {
		CollectionUpdate struct {
			UserErrors []userError `json:"userErrors"`
		} `json:"collectionUpdate"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("shopify: parse collectionUpdate response: %w", err)
	}
	if len(resp.CollectionUpdate.UserErrors) > 0 {
		return fmt.Errorf("shopify: collectionUpdate userError: %s", resp.CollectionUpdate.UserErrors[0].Message)
	}
	return nil
}

// ── Page mutations ────────────────────────────────────────────────────────────

const createPageMutation = `
mutation PageCreate($page: PageCreateInput!) {
  pageCreate(page: $page) {
    page { id title handle }
    userErrors { field message }
  }
}`

const updatePageMutation = `
mutation PageUpdate($id: ID!, $page: PageUpdateInput!) {
  pageUpdate(id: $id, page: $page) {
    page { id title handle }
    userErrors { field message }
  }
}`

// CreatePage creates a new Shopify page and returns its GID.
func CreatePage(ctx context.Context, shop, token, title, bodyHTML string) (string, error) {
	vars := map[string]any{
		"page": map[string]any{
			"title": title,
			"body":  bodyHTML,
		},
	}
	raw, err := Query(ctx, shop, token, createPageMutation, vars)
	if err != nil {
		return "", err
	}
	var resp struct {
		PageCreate struct {
			Page struct {
				ID string `json:"id"`
			} `json:"page"`
			UserErrors []userError `json:"userErrors"`
		} `json:"pageCreate"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("shopify: parse pageCreate response: %w", err)
	}
	if len(resp.PageCreate.UserErrors) > 0 {
		return "", fmt.Errorf("shopify: pageCreate userError: %s", resp.PageCreate.UserErrors[0].Message)
	}
	return resp.PageCreate.Page.ID, nil
}

// UpdatePage replaces the body of an existing Shopify page.
func UpdatePage(ctx context.Context, shop, token, pageGID, bodyHTML string) error {
	vars := map[string]any{
		"id": pageGID,
		"page": map[string]any{
			"body": bodyHTML,
		},
	}
	raw, err := Query(ctx, shop, token, updatePageMutation, vars)
	if err != nil {
		return err
	}
	var resp struct {
		PageUpdate struct {
			UserErrors []userError `json:"userErrors"`
		} `json:"pageUpdate"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("shopify: parse pageUpdate response: %w", err)
	}
	if len(resp.PageUpdate.UserErrors) > 0 {
		return fmt.Errorf("shopify: pageUpdate userError: %s", resp.PageUpdate.UserErrors[0].Message)
	}
	return nil
}

// CollectionNode holds the fields needed for collection auditing.
type CollectionNode struct {
	ID           string
	Handle       string
	Title        string
	Description  string // plain text
	ProductCount int
}

const fetchCollectionsQuery = `
query FetchCollections($cursor: String) {
  collections(first: 50, after: $cursor) {
    pageInfo { hasNextPage endCursor }
    edges {
      node {
        id
        handle
        title
        description
        productsCount { count }
      }
    }
  }
}`

type fetchCollectionsData struct {
	Collections struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []struct {
			Node struct {
				ID          string `json:"id"`
				Handle      string `json:"handle"`
				Title       string `json:"title"`
				Description string `json:"description"`
				ProductsCount struct {
					Count int `json:"count"`
				} `json:"productsCount"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"collections"`
}

// FetchAllCollections returns every collection in the store using cursor pagination.
func FetchAllCollections(ctx context.Context, shop, token string) ([]CollectionNode, error) {
	var collections []CollectionNode
	var cursor *string

	for {
		vars := map[string]any{}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		raw, err := Query(ctx, shop, token, fetchCollectionsQuery, vars)
		if err != nil {
			return nil, fmt.Errorf("shopify: FetchAllCollections: %w", err)
		}

		var data fetchCollectionsData
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("shopify: FetchAllCollections decode: %w", err)
		}

		for _, edge := range data.Collections.Edges {
			n := edge.Node
			collections = append(collections, CollectionNode{
				ID:           n.ID,
				Handle:       n.Handle,
				Title:        n.Title,
				Description:  n.Description,
				ProductCount: n.ProductsCount.Count,
			})
		}

		if !data.Collections.PageInfo.HasNextPage {
			break
		}
		c := data.Collections.PageInfo.EndCursor
		cursor = &c
	}

	return collections, nil
}

// PageNode holds the fields needed for page auditing.
type PageNode struct {
	ID     string
	Handle string
	Title  string
	Body   string // raw HTML body
}

const fetchPagesQuery = `
query FetchPages($cursor: String) {
  pages(first: 50, after: $cursor) {
    pageInfo { hasNextPage endCursor }
    edges {
      node {
        id
        handle
        title
        body
      }
    }
  }
}`

type fetchPagesData struct {
	Pages struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []struct {
			Node struct {
				ID     string `json:"id"`
				Handle string `json:"handle"`
				Title  string `json:"title"`
				Body   string `json:"body"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"pages"`
}

// FetchAllPages returns every page in the store using cursor pagination.
func FetchAllPages(ctx context.Context, shop, token string) ([]PageNode, error) {
	var pages []PageNode
	var cursor *string

	for {
		vars := map[string]any{}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		raw, err := Query(ctx, shop, token, fetchPagesQuery, vars)
		if err != nil {
			return nil, fmt.Errorf("shopify: FetchAllPages: %w", err)
		}

		var data fetchPagesData
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("shopify: FetchAllPages decode: %w", err)
		}

		for _, edge := range data.Pages.Edges {
			n := edge.Node
			pages = append(pages, PageNode{
				ID:     n.ID,
				Handle: n.Handle,
				Title:  n.Title,
				Body:   n.Body,
			})
		}

		if !data.Pages.PageInfo.HasNextPage {
			break
		}
		c := data.Pages.PageInfo.EndCursor
		cursor = &c
	}

	return pages, nil
}
