// Package auth verifies bearer tokens. Dev/test uses HS256 with a shared secret;
// production should verify RS256/ES256 tokens against the IdP's JWKS (see spec open questions).
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInvalidToken is returned for any token that must not be trusted.
var ErrInvalidToken = errors.New("invalid token")

// JWT verifies and issues HS256 tokens whose subject is the customer id.
type JWT struct {
	secret []byte
	issuer string
}

// NewJWT returns a verifier bound to one issuer.
func NewJWT(secret []byte, issuer string) *JWT { return &JWT{secret: secret, issuer: issuer} }

// Authenticate returns the customer id (sub) of a valid token.
// It requires HS256, a matching issuer, and an unexpired exp claim.
func (j *JWT) Authenticate(token string) (string, error) {
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (any, error) { return j.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(j.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || claims.Subject == "" {
		return "", ErrInvalidToken
	}
	return claims.Subject, nil
}

// Issue creates a token for customerID. For local development and tests only.
func (j *JWT) Issue(customerID string, ttl time.Duration, now time.Time) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   customerID,
		Issuer:    j.issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	})
	s, err := t.SignedString(j.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return s, nil
}
