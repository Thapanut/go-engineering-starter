package twoc2p

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
)

// Synthetic test values only.
var (
	secret     = []byte("test-2c2p-secret-key-0123456789abcdef")
	merchantID = "JT04"
)

func payload(t *testing.T, overrides map[string]any) []byte {
	t.Helper()
	c := map[string]any{
		"merchantID": merchantID, "invoiceNo": "280520075921", "amount": "230.87",
		"currencyCode": "THB", "tranRef": "2868821", "respCode": "0000", "respDesc": "Success",
		"cardNo": "411111XXXXXX1111",
	}
	for k, v := range overrides {
		c[k] = v
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func signed(t *testing.T, key []byte, overrides map[string]any) []byte {
	t.Helper()
	body, err := Sign(key, payload(t, overrides))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestVerifyDecodesValidNotification(t *testing.T) {
	v := NewVerifier(secret, merchantID)
	n, err := v.Verify(context.Background(), signed(t, secret, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := domain.PaymentNotification{InvoiceNo: "280520075921", ProviderRef: "2868821",
		Amount: domain.Money{Amount: 23087, Currency: domain.THB}, Outcome: domain.PaymentSuccess, ProviderCode: "0000"}
	if n != want {
		t.Fatalf("got %+v, want %+v", n, want)
	}
}

func TestVerifyMapsNonSuccessCodeToFailed(t *testing.T) {
	n, err := NewVerifier(secret, merchantID).Verify(context.Background(), signed(t, secret, map[string]any{"respCode": "4001"}))
	if err != nil || n.Outcome != domain.PaymentFailed || n.ProviderCode != "4001" {
		t.Fatalf("got %+v, %v", n, err)
	}
}

func TestVerifyRejectsUntrustedBodies(t *testing.T) {
	v := NewVerifier(secret, merchantID)
	good := signed(t, secret, nil)
	var env envelope
	_ = json.Unmarshal(good, &env)
	parts := strings.Split(env.Payload, ".")

	tampered := payload(t, map[string]any{"amount": "1.00"})
	noneHeader := b64.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	hs512Header := b64.EncodeToString([]byte(`{"alg":"HS512","typ":"JWT"}`))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(hs512Header + "." + parts[1]))
	wrap := func(token string) []byte { b, _ := json.Marshal(envelope{Payload: token}); return b }

	cases := map[string][]byte{
		"wrong key":          signed(t, []byte("another-secret-key-0123456789abcdef!"), nil),
		"tampered payload":   wrap(parts[0] + "." + b64.EncodeToString(tampered) + "." + parts[2]),
		"alg none":           wrap(noneHeader + "." + parts[1] + "."),
		"alg HS512":          wrap(hs512Header + "." + parts[1] + "." + b64.EncodeToString(mac.Sum(nil))),
		"not a jwt":          wrap("abc"),
		"bad signature b64":  wrap(parts[0] + "." + parts[1] + ".***"),
		"empty payload":      []byte(`{"payload":""}`),
		"not json":           []byte(`payload=abc`),
		"other merchant":     signed(t, secret, map[string]any{"merchantID": "OTHER"}),
		"missing merchantID": signed(t, secret, map[string]any{"merchantID": ""}),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), body); !errors.Is(err, domain.ErrInvalidSignature) {
				t.Fatalf("err = %v, want ErrInvalidSignature", err)
			}
		})
	}
}

func TestParseMinorUnits(t *testing.T) {
	ok := map[string]int64{
		`"230.87"`: 23087, `230.87`: 23087, `"230.8"`: 23080, `"230"`: 23000, `0`: 0,
		`"0.01"`: 1, `"999999999999999.99"`: 99999999999999999,
	}
	for in, want := range ok {
		if got, err := parseMinorUnits(json.RawMessage(in)); err != nil || got != want {
			t.Errorf("%s → %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{`"-1.00"`, `"1.234"`, `"1e3"`, `"abc"`, `""`, `null`, `"1,000.00"`, `".5"`, `"1234567890123456"`} {
		if _, err := parseMinorUnits(json.RawMessage(in)); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", in, err)
		}
	}
}
