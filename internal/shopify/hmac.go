package shopify

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
)

// VerifyOAuthHMAC validates the HMAC on Shopify OAuth callback query params.
//
// Algorithm:
//  1. Remove the "hmac" key from the param map.
//  2. Sort remaining keys alphabetically.
//  3. Join as "key=value&key=value".
//  4. HMAC-SHA256 with the app secret, hex-encode.
//  5. Constant-time compare against the original hmac param.
func VerifyOAuthHMAC(params url.Values, secret string) bool {
	expected := params.Get("hmac")
	if expected == "" {
		return false
	}

	// Build sorted key=value pairs, excluding hmac
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "hmac" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	message := strings.Join(parts, "&")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	computed := hex.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(computed), []byte(expected)) == 1
}

// VerifyWebhookHMAC validates the X-Shopify-Hmac-Sha256 header on webhook payloads.
//
// Algorithm: HMAC-SHA256 of raw body bytes, base64-encoded, constant-time compare.
func VerifyWebhookHMAC(body []byte, header, secret string) bool {
	if header == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	computed := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(computed), []byte(header)) == 1
}
