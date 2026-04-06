package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const oauthScopes = "read_products,write_products,read_content"

// BuildAuthURL constructs the Shopify OAuth authorization URL.
func BuildAuthURL(shop, clientID, redirectURI, state string) string {
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("scope", oauthScopes)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	params.Set("grant_options[]", "per-user")

	return fmt.Sprintf("https://%s/admin/oauth/authorize?%s", shop, params.Encode())
}

// TokenResponse is the payload Shopify returns after a successful code exchange.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
}

// ExchangeCode exchanges a Shopify OAuth code for a permanent access token.
func ExchangeCode(ctx context.Context, shop, code, clientID, secret string) (*TokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", clientID)
	body.Set("client_secret", secret)
	body.Set("code", code)

	endpoint := fmt.Sprintf("https://%s/admin/oauth/access_token", shop)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("shopify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shopify: token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shopify: token exchange: unexpected status %d", resp.StatusCode)
	}

	var tok TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("shopify: decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("shopify: empty access token in response")
	}
	return &tok, nil
}
