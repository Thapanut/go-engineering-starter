// Package twoc2p implements port.WebhookSignatureValidator for 2C2P PGW v4
// backend notifications.
//
// 2C2P POSTs {"payload":"<JWT>"} where the JWT is signed HS256 (HMAC-SHA256)
// with the merchant Secret Key:
// https://developer.2c2p.com/docs/api-payment-response-backend
// This adapter verifies the HMAC itself (alg pinned, constant-time compare) and
// translates 2C2P's field names into a provider-agnostic domain notification.
package twoc2p

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// respCodeSuccess is 2C2P's success code. Every other code is treated as FAILED
// (UNCONFIRMED: pending codes, see spec open questions).
const respCodeSuccess = "0000"

var b64 = base64.RawURLEncoding

// Validator verifies 2C2P webhooks for one merchant.
type Validator struct {
	secret     []byte
	merchantID string
}

var _ port.WebhookSignatureValidator = (*Validator)(nil)

// NewValidator returns a validator for merchantID using its 2C2P Secret Key.
func NewValidator(secret []byte, merchantID string) *Validator {
	return &Validator{secret: secret, merchantID: merchantID}
}

type envelope struct {
	Payload string `json:"payload"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
}

// claims are the 2C2P fields this service uses; others are ignored because the
// provider may add fields at any time.
type claims struct {
	MerchantID   string          `json:"merchantID"`
	InvoiceNo    string          `json:"invoiceNo"`
	Amount       json.RawMessage `json:"amount"`
	CurrencyCode string          `json:"currencyCode"`
	TranRef      string          `json:"tranRef"`
	RespCode     string          `json:"respCode"`
}

// Verify authenticates rawBody and returns the decoded notification.
// Any authentication failure returns domain.ErrInvalidSignature (spec AC-05, AC-06).
func (v *Validator) Verify(_ context.Context, rawBody []byte) (domain.PaymentNotification, error) {
	var env envelope
	if err := json.Unmarshal(rawBody, &env); err != nil || env.Payload == "" {
		return domain.PaymentNotification{}, domain.ErrInvalidSignature
	}
	payload, err := v.verifyJWT(env.Payload)
	if err != nil {
		return domain.PaymentNotification{}, err
	}

	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return domain.PaymentNotification{}, domain.Invalid("malformed notification payload")
	}
	if c.MerchantID != v.merchantID {
		return domain.PaymentNotification{}, domain.ErrInvalidSignature
	}
	amount, err := parseMinorUnits(c.Amount)
	if err != nil {
		return domain.PaymentNotification{}, err
	}
	outcome := domain.PaymentFailed
	if c.RespCode == respCodeSuccess {
		outcome = domain.PaymentSuccess
	}
	return domain.PaymentNotification{
		InvoiceNo:    c.InvoiceNo,
		ProviderRef:  c.TranRef,
		Amount:       domain.Money{Amount: amount, Currency: domain.Currency(strings.ToUpper(c.CurrencyCode))},
		Outcome:      outcome,
		ProviderCode: c.RespCode,
	}, nil
}

// verifyJWT checks an HS256 compact JWT and returns its decoded payload.
func (v *Validator) verifyJWT(token string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, domain.ErrInvalidSignature
	}
	headerJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, domain.ErrInvalidSignature
	}
	var h jwtHeader
	if err := json.Unmarshal(headerJSON, &h); err != nil || h.Alg != "HS256" {
		return nil, domain.ErrInvalidSignature // alg pinned: rejects "none" and algorithm confusion
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return nil, domain.ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) { // constant-time compare
		return nil, domain.ErrInvalidSignature
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, domain.ErrInvalidSignature
	}
	return payload, nil
}

var decimalRe = regexp.MustCompile(`^(\d{1,15})(?:\.(\d{1,2}))?$`)

// parseMinorUnits converts a decimal amount such as "230.87" or 230.87 into
// minor units (23087) using string arithmetic only, never float.
func parseMinorUnits(raw json.RawMessage) (int64, error) {
	s := string(bytes.TrimSpace(raw))
	if unq, err := strconv.Unquote(s); err == nil {
		s = unq
	}
	m := decimalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, domain.Invalid("amount must be a non-negative decimal with at most 2 places")
	}
	whole, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || whole > (math.MaxInt64-99)/100 {
		return 0, domain.Invalid("amount out of range")
	}
	frac := int64(0)
	if m[2] != "" {
		f, _ := strconv.ParseInt(m[2], 10, 64) // regex guarantees 1-2 digits
		if len(m[2]) == 1 {
			f *= 10
		}
		frac = f
	}
	return whole*100 + frac, nil
}

// Sign builds a 2C2P-style webhook body for payload using secret. It exists for
// tests and sandbox tooling only; production bodies are signed by 2C2P.
func Sign(secret, payload []byte) ([]byte, error) {
	head := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := b64.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(head + "." + body))
	token := head + "." + body + "." + b64.EncodeToString(mac.Sum(nil))
	out, err := json.Marshal(envelope{Payload: token})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook body: %w", err)
	}
	return out, nil
}
