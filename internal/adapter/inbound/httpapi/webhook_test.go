package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
)

// Synthetic test values only.
var (
	whSecret   = []byte("test-2c2p-secret-key-0123456789abcdef")
	whMerchant = "JT04"
)

const whInvoice = "INV-HTTP-0001"

func newWebhookEnv(t *testing.T) (*env, *memory.Store) {
	t.Helper()
	st := memory.NewStore()
	err := st.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		return r.Payments.Create(ctx, domain.Payment{ID: "pay-1", InvoiceNo: whInvoice,
			Amount: domain.Money{Amount: 23087, Currency: domain.THB}, Status: domain.PaymentPending})
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{jwt: nil, logs: &syncBuffer{}}
	log := slog.New(slog.NewJSONHandler(e.logs, nil))
	svc := service.NewWebhookService(twoc2p.NewVerifier(whSecret, whMerchant), st, system.Clock{}, system.UUIDGenerator{}, log)
	e.app = NewApp(Deps{Auth: rejectAll{}, Log: log, RequestTimeout: 5 * time.Second,
		Public: []PublicModule{WebhookModule{UseCase: svc}}})
	return e, st
}

type rejectAll struct{}

func (rejectAll) Authenticate(string) (string, error) { return "", errUnauthorized }

func webhookBody(t *testing.T, key []byte, invoice, amount string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"merchantID": whMerchant, "invoiceNo": invoice, "amount": amount, "currencyCode": "THB",
		"tranRef": "2868821", "respCode": "0000", "cardNo": "411111XXXXXX1111",
	})
	body, err := twoc2p.Sign(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func postWebhook(t *testing.T, e *env, body string) response {
	t.Helper()
	return e.do(t, call{method: "POST", path: "/webhooks/2c2p", body: body})
}

func outcomeOf(t *testing.T, r response) string {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("status = %d; body %s", r.status, r.body)
	}
	var ack webhookAck
	if err := json.Unmarshal(r.body, &ack); err != nil || ack.Status != "OK" {
		t.Fatalf("ack = %s (%v)", r.body, err)
	}
	return ack.Outcome
}

func TestWebhookAC01_AC03_ProcessThenDuplicate(t *testing.T) {
	e, st := newWebhookEnv(t)
	body := webhookBody(t, whSecret, whInvoice, "230.87")
	if got := outcomeOf(t, postWebhook(t, e, body)); got != "PROCESSED" {
		t.Fatalf("first delivery outcome = %s", got)
	}
	first := storedPayment(t, st)
	if got := outcomeOf(t, postWebhook(t, e, body)); got != "DUPLICATE" {
		t.Fatalf("second delivery outcome = %s", got)
	}
	if p := storedPayment(t, st); p.Status != domain.PaymentSuccess || p != first {
		t.Fatalf("payment changed on duplicate:\nbefore %+v\nafter  %+v", first, p)
	}
}

func storedPayment(t *testing.T, st *memory.Store) domain.Payment {
	t.Helper()
	var p domain.Payment
	err := st.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		var err error
		p, err = r.Payments.GetByInvoiceNoForUpdate(ctx, whInvoice)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWebhookAC05_InvalidSignatureIs401(t *testing.T) {
	e, st := newWebhookEnv(t)
	forged := webhookBody(t, []byte("attacker-key-0123456789abcdef-xyz!!"), whInvoice, "230.87")
	assertError(t, postWebhook(t, e, forged), http.StatusUnauthorized, "INVALID_SIGNATURE")
	assertError(t, postWebhook(t, e, `{"payload":"not-a-jwt"}`), http.StatusUnauthorized, "INVALID_SIGNATURE")
	if p := storedPayment(t, st); p.Status != domain.PaymentPending {
		t.Fatalf("status = %s", p.Status)
	}
}

func TestWebhookAC07_AC08_AC09_ErrorMapping(t *testing.T) {
	e, _ := newWebhookEnv(t)
	assertError(t, postWebhook(t, e, webhookBody(t, whSecret, "INV-UNKNOWN", "230.87")), http.StatusNotFound, "NOT_FOUND")
	assertError(t, postWebhook(t, e, webhookBody(t, whSecret, whInvoice, "1.00")), http.StatusUnprocessableEntity, "PAYMENT_MISMATCH")
	assertError(t, postWebhook(t, e, webhookBody(t, whSecret, whInvoice, "230.871")), http.StatusBadRequest, "VALIDATION_ERROR")
}

func TestWebhookIsPublicButV1StaysProtected(t *testing.T) {
	e, _ := newWebhookEnv(t)
	outcomeOf(t, postWebhook(t, e, webhookBody(t, whSecret, whInvoice, "230.87"))) // no bearer token needed
	assertError(t, e.do(t, call{method: "GET", path: "/v1/anything"}), http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestWebhookAC11_LogsContainNoPayloadData(t *testing.T) {
	e, _ := newWebhookEnv(t)
	body := webhookBody(t, whSecret, whInvoice, "230.87")
	postWebhook(t, e, body)
	logs := e.logs.String()
	for _, forbidden := range []string{whInvoice, "411111", "2868821", body[12:40]} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains %q:\n%s", forbidden, logs)
		}
	}
	if !strings.Contains(logs, `"route":"/webhooks/2c2p"`) {
		t.Errorf("missing access log:\n%s", logs)
	}
}
