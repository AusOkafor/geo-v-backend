package crypto

import (
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key := []byte("12345678901234567890123456789012") // 32 bytes
	plaintext := "shpat_test_access_token_abc123"

	enc, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	dec, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if dec != plaintext {
		t.Errorf("round-trip mismatch: got %q, want %q", dec, plaintext)
	}
}

func TestEncryptProducesUniqueOutput(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	plaintext := "same_input"

	enc1, _ := Encrypt(plaintext, key)
	enc2, _ := Encrypt(plaintext, key)

	if enc1 == enc2 {
		t.Error("two Encrypt calls with the same input should produce different ciphertext (random nonce)")
	}
}

func TestDecryptBadInput(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	_, err := Decrypt("notvalidbase64!!!", key)
	if err == nil {
		t.Error("expected error decrypting invalid base64")
	}
}

func TestWrongKey(t *testing.T) {
	key1 := []byte("12345678901234567890123456789012")
	key2 := []byte("99999999999999999999999999999999")

	enc, _ := Encrypt("secret", key1)
	_, err := Decrypt(enc, key2)
	if err == nil {
		t.Error("expected error decrypting with wrong key")
	}
}
