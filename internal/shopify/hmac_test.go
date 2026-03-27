package shopify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"testing"
)

const testSecret = "hush_puppies_secret_key_abc"

func TestVerifyOAuthHMAC_Valid(t *testing.T) {
	secret := testSecret
	params := url.Values{
		"shop":      {"testshop.myshopify.com"},
		"timestamp": {"1337178173"},
		"code":      {"0907a61c0c8d55e99db179b68161bc00"},
	}
	// Compute correct HMAC: sorted params (excluding hmac), joined with &
	message := "code=0907a61c0c8d55e99db179b68161bc00&shop=testshop.myshopify.com&timestamp=1337178173"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	params.Set("hmac", hex.EncodeToString(mac.Sum(nil)))

	if !VerifyOAuthHMAC(params, secret) {
		t.Error("expected valid HMAC to pass")
	}
}

func TestVerifyOAuthHMAC_Invalid(t *testing.T) {
	params := url.Values{
		"shop":      {"testshop.myshopify.com"},
		"timestamp": {"1337178173"},
		"hmac":      {"badhmacsignature"},
	}
	if VerifyOAuthHMAC(params, testSecret) {
		t.Error("expected invalid HMAC to fail")
	}
}

func TestVerifyOAuthHMAC_MissingHMAC(t *testing.T) {
	params := url.Values{"shop": {"testshop.myshopify.com"}}
	if VerifyOAuthHMAC(params, testSecret) {
		t.Error("expected missing hmac param to fail")
	}
}

func TestVerifyWebhookHMAC_Valid(t *testing.T) {
	body := []byte(`{"id":123,"email":"test@example.com"}`)
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	header := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !VerifyWebhookHMAC(body, header, testSecret) {
		t.Error("expected valid webhook HMAC to pass")
	}
}

func TestVerifyWebhookHMAC_Invalid(t *testing.T) {
	body := []byte(`{"id":123}`)
	if VerifyWebhookHMAC(body, "invalidsignature==", testSecret) {
		t.Error("expected invalid webhook HMAC to fail")
	}
}

func TestVerifyWebhookHMAC_MissingHeader(t *testing.T) {
	if VerifyWebhookHMAC([]byte("body"), "", testSecret) {
		t.Error("expected empty header to fail")
	}
}
