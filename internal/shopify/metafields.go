package shopify

import (
	"context"
	"encoding/json"
	"fmt"
)

// getShopGID returns the shop's canonical GID (e.g. "gid://shopify/Shop/12345678").
func getShopGID(ctx context.Context, shop, token string) (string, error) {
	const q = `query { shop { id } }`
	raw, err := Query(ctx, shop, token, q, nil)
	if err != nil {
		return "", fmt.Errorf("shopify: getShopGID: %w", err)
	}
	var resp struct {
		Shop struct {
			ID string `json:"id"`
		} `json:"shop"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("shopify: getShopGID decode: %w", err)
	}
	if resp.Shop.ID == "" {
		return "", fmt.Errorf("shopify: getShopGID: empty id")
	}
	return resp.Shop.ID, nil
}

// SetShopMetafield creates or updates a metafield on the shop object.
// valueType should be a valid Shopify metafield type, e.g. "json", "single_line_text_field".
func SetShopMetafield(ctx context.Context, shop, token, namespace, key, valueType, value string) error {
	shopGID, err := getShopGID(ctx, shop, token)
	if err != nil {
		return err
	}

	const mutation = `
mutation SetMetafield($metafields: [MetafieldsSetInput!]!) {
  metafieldsSet(metafields: $metafields) {
    metafields { id key namespace value }
    userErrors { field message code }
  }
}`
	vars := map[string]any{
		"metafields": []map[string]any{
			{
				"ownerId":   shopGID,
				"namespace": namespace,
				"key":       key,
				"type":      valueType,
				"value":     value,
			},
		},
	}

	raw, err := Query(ctx, shop, token, mutation, vars)
	if err != nil {
		return fmt.Errorf("shopify: SetShopMetafield: %w", err)
	}

	var resp struct {
		MetafieldsSet struct {
			UserErrors []userError `json:"userErrors"`
		} `json:"metafieldsSet"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("shopify: SetShopMetafield decode: %w", err)
	}
	if len(resp.MetafieldsSet.UserErrors) > 0 {
		return fmt.Errorf("shopify: SetShopMetafield userError: %s", resp.MetafieldsSet.UserErrors[0].Message)
	}
	return nil
}

// GetShopMetafieldValue returns the value of a shop metafield, or ("", nil) if not found.
func GetShopMetafieldValue(ctx context.Context, shop, token, namespace, key string) (string, error) {
	const query = `
query GetShopMetafield($namespace: String!, $key: String!) {
  shop {
    metafield(namespace: $namespace, key: $key) {
      value
    }
  }
}`
	raw, err := Query(ctx, shop, token, query, map[string]any{
		"namespace": namespace,
		"key":       key,
	})
	if err != nil {
		return "", fmt.Errorf("shopify: GetShopMetafieldValue: %w", err)
	}

	var resp struct {
		Shop struct {
			Metafield *struct {
				Value string `json:"value"`
			} `json:"metafield"`
		} `json:"shop"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("shopify: GetShopMetafieldValue decode: %w", err)
	}
	if resp.Shop.Metafield == nil {
		return "", nil
	}
	return resp.Shop.Metafield.Value, nil
}

// GrantStorefrontMetafieldAccess enables storefront API read access for a metafield definition.
// This is required for the Theme App Extension's Liquid template to read the metafield.
// It is idempotent — safe to call multiple times.
func GrantStorefrontMetafieldAccess(ctx context.Context, shop, token, namespace, key string) error {
	// First, find the metafield definition ID
	const findQuery = `
query FindMetafieldDef($namespace: String!, $key: String!) {
  metafieldDefinitions(first: 1, ownerType: SHOP, namespace: $namespace, key: $key) {
    edges {
      node { id }
    }
  }
}`
	raw, err := Query(ctx, shop, token, findQuery, map[string]any{
		"namespace": namespace,
		"key":       key,
	})
	if err != nil {
		return fmt.Errorf("shopify: GrantStorefrontMetafieldAccess find: %w", err)
	}

	var findResp struct {
		MetafieldDefinitions struct {
			Edges []struct {
				Node struct {
					ID string `json:"id"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"metafieldDefinitions"`
	}
	if err := json.Unmarshal(raw, &findResp); err != nil {
		return fmt.Errorf("shopify: GrantStorefrontMetafieldAccess decode: %w", err)
	}

	if len(findResp.MetafieldDefinitions.Edges) == 0 {
		// No definition exists — create one with storefront access enabled
		return createMetafieldDefinition(ctx, shop, token, namespace, key)
	}

	defID := findResp.MetafieldDefinitions.Edges[0].Node.ID

	// Update existing definition to enable storefront access
	const updateMutation = `
mutation UpdateMetafieldDef($id: ID!, $access: MetafieldDefinitionUpdateInput!) {
  metafieldDefinitionUpdate(id: $id, definition: $access) {
    updatedDefinition { id }
    userErrors { field message }
  }
}`
	updateRaw, err := Query(ctx, shop, token, updateMutation, map[string]any{
		"id": defID,
		"access": map[string]any{
			"access": map[string]any{
				"storefront": "PUBLIC_READ",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("shopify: GrantStorefrontMetafieldAccess update: %w", err)
	}

	var updateResp struct {
		MetafieldDefinitionUpdate struct {
			UserErrors []userError `json:"userErrors"`
		} `json:"metafieldDefinitionUpdate"`
	}
	if err := json.Unmarshal(updateRaw, &updateResp); err != nil {
		return fmt.Errorf("shopify: GrantStorefrontMetafieldAccess update decode: %w", err)
	}
	if len(updateResp.MetafieldDefinitionUpdate.UserErrors) > 0 {
		// Non-fatal — storefront access may already be granted
		return nil
	}
	return nil
}

func createMetafieldDefinition(ctx context.Context, shop, token, namespace, key string) error {
	const mutation = `
mutation CreateMetafieldDef($definition: MetafieldDefinitionInput!) {
  metafieldDefinitionCreate(definition: $definition) {
    createdDefinition { id }
    userErrors { field message }
  }
}`
	raw, err := Query(ctx, shop, token, mutation, map[string]any{
		"definition": map[string]any{
			"name":      "GEO Visibility Schema",
			"namespace": namespace,
			"key":       key,
			"type":      "json",
			"ownerType": "SHOP",
			"access": map[string]any{
				"storefront": "PUBLIC_READ",
			},
		},
	})
	if err != nil {
		return fmt.Errorf("shopify: createMetafieldDefinition: %w", err)
	}

	var resp struct {
		MetafieldDefinitionCreate struct {
			UserErrors []userError `json:"userErrors"`
		} `json:"metafieldDefinitionCreate"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("shopify: createMetafieldDefinition decode: %w", err)
	}
	if len(resp.MetafieldDefinitionCreate.UserErrors) > 0 {
		return fmt.Errorf("shopify: createMetafieldDefinition userError: %s", resp.MetafieldDefinitionCreate.UserErrors[0].Message)
	}
	return nil
}
