package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer  = "geo-visibility"
	expiry  = 30 * 24 * time.Hour
)

// Issue generates a signed JWT encoding the shop domain.
// secret must be the 32-byte ENCRYPTION_KEY.
func Issue(shopDomain string, secret []byte) (string, error) {
	claims := jwt.RegisteredClaims{
		Issuer:    issuer,
		Subject:   shopDomain,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(secret)
}

// Verify parses and validates a JWT, returning the shop domain on success.
func Verify(tokenStr string, secret []byte) (string, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("auth: unexpected signing method")
		}
		return secret, nil
	}, jwt.WithIssuer(issuer), jwt.WithExpirationRequired())
	if err != nil {
		return "", err
	}
	claims, ok := tok.Claims.(*jwt.RegisteredClaims)
	if !ok || claims.Subject == "" {
		return "", errors.New("auth: invalid claims")
	}
	return claims.Subject, nil
}
