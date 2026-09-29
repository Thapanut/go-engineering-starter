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
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

// paymentsEnv wires payment status, webhook, and (optionally) the demo on one
// memory store. Payments are created through the use case, as ordering does.
type paymentsEnv struct {
	*env
	checkout *service.CheckoutService
}

func newPaymentsEnv(t *testing.T, demo bool) *paymentsEnv {
	t.Helper()
	st := memory.NewStore()
	e := &env{jwt: auth.NewJWT(secret, issuer), logs: &syncBuffer{}}
	log := slog.New(slog.NewJSONHandler(e.logs, nil))
	checkout := service.NewCheckoutService(st, twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	public := []PublicModule{WebhookModule{UseCase: service.NewWebhookService(
		twoc2p.NewVerifier(whSecret, whMerchant), st, system.Clock{}, system.UUIDGenerator{}, log)}}
	modules := []Module{PaymentModule{UseCase: checkout}}
	if demo {
		d := DemoModule{Events: checkout, Publisher: PublisherInProcess}
		public, modules = append(public, d), append(modules, d)
	}
	e.app = NewApp(Deps{Auth: e.jwt, Log: log, RequestTimeout: 5 * time.Second, Public: public}, modules...)
	return &paymentsEnv{env: e, checkout: checkout}
}

func (e *env) tokenFor(t *testing.T, sub string) string {
	t.Helper()
	tok, err := e.jwt.Issue(sub, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// createPayment starts a 1,000.00 THB payment for the test customer (synthetic data).
func createPayment(t *testing.T, e *paymentsEnv) domain.Payment {
	t.Helper()
	co, err := e.checkout.CreatePayment(context.Background(), port.CreatePaymentCommand{
		CustomerID: customer, OrderID: "ORD-HTTP-1", Amount: domain.Money{Amount: 100000, Currency: domain.THB}})
	if err != nil {
		t.Fatal(err)
	}
	return co.Payment
}

func getStatus(t *testing.T, e *paymentsEnv, token, invoiceNo string) (response, paymentStatusResponse) {
	t.Helper()
	r := e.do(t, call{method: "GET", path: "/v1/payments/" + invoiceNo + "/status", token: token})
	var out paymentStatusResponse
	if r.status == http.StatusOK {
		if err := json.Unmarshal(r.body, &out); err != nil {
			t.Fatal(err)
		}
	}
	return r, out
}

func TestOrderFlowAC13_NoPublicPaymentCreation(t *testing.T) {
	e := newPaymentsEnv(t, false)
	body := `{"orderId":"ORD-1","amount":"0.01","currency":"THB"}`
	assertError(t, e.do(t, call{method: "POST", path: "/v1/payments", token: e.token(t), body: body}), http.StatusNotFound, "NOT_FOUND")
}

func TestCheckoutAC03_StatusRequiresBearerToken(t *testing.T) {
	e := newPaymentsEnv(t, false)
	p := createPayment(t, e)
	forged, _ := auth.NewJWT([]byte("another-secret-that-is-32-bytes-long"), issuer).Issue(customer, time.Hour, time.Now())
	for _, tok := range []string{"", forged} {
		r, _ := getStatus(t, e, tok, p.InvoiceNo)
		assertError(t, r, http.StatusUnauthorized, "UNAUTHORIZED")
	}
}

func TestCheckoutAC06_AC07_StatusIsOwnerOnly(t *testing.T) {
	e := newPaymentsEnv(t, false)
	out := createPayment(t, e)
	r, st := getStatus(t, e, e.token(t), out.InvoiceNo)
	if r.status != http.StatusOK || st.InvoiceNo != out.InvoiceNo || st.OrderID != "ORD-HTTP-1" || st.Status != "PENDING" ||
		st.amountFields != (amountFields{Amount: "1000.00", AmountMinor: 100000, Currency: "THB"}) || st.UpdatedAt == "" {
		t.Fatalf("status = %d %+v", r.status, st)
	}
	other, _ := getStatus(t, e, e.tokenFor(t, "cust-other"), out.InvoiceNo)
	unknown, _ := getStatus(t, e, e.token(t), "INV00000000000000000000000000000000")
	a := assertError(t, other, http.StatusNotFound, "NOT_FOUND")
	b := assertError(t, unknown, http.StatusNotFound, "NOT_FOUND")
	if a.Message != b.Message {
		t.Fatalf("another customer's payment is distinguishable from an unknown one: %q vs %q", a.Message, b.Message)
	}
}

func TestCheckoutAC08_WebhookUpdatesPolledStatus(t *testing.T) {
	e := newPaymentsEnv(t, false)
	out := createPayment(t, e)
	if got := outcomeOf(t, postWebhook(t, e.env, webhookBody(t, whSecret, out.InvoiceNo, "1000.00"))); got != "PROCESSED" {
		t.Fatalf("webhook outcome = %s", got)
	}
	if _, st := getStatus(t, e, e.token(t), out.InvoiceNo); st.Status != "SUCCESS" {
		t.Fatalf("status after webhook = %+v", st)
	}
}

func TestCheckoutAC09_LogsContainNoIdentifiers(t *testing.T) {
	e := newPaymentsEnv(t, false)
	out := createPayment(t, e)
	getStatus(t, e, e.token(t), out.InvoiceNo)
	logs := e.logs.String()
	for _, forbidden := range []string{out.InvoiceNo, out.OrderID, out.ID} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains %q:\n%s", forbidden, logs)
		}
	}
	for _, route := range []string{`"route":"/v1/payments/:invoiceNo/status"`} {
		if !strings.Contains(logs, route) {
			t.Errorf("missing access log %s:\n%s", route, logs)
		}
	}
}

func TestCheckoutAC10_DemoPageOnlyWhenEnabled(t *testing.T) {
	assertError(t, newPaymentsEnv(t, false).do(t, call{method: "GET", path: "/demo"}),
		http.StatusNotFound, "NOT_FOUND")

	r := newPaymentsEnv(t, true).do(t, call{method: "GET", path: "/demo"})
	if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d, content-type = %q", r.status, r.header.Get("Content-Type"))
	}
	if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("CSP = %q", csp)
	}
	if !strings.Contains(string(r.body), "Simulate Successful Payment (OTP Passed)") {
		t.Fatal("demo page body is not the checkout page")
	}
}

func TestCheckoutAC11_DemoPageCarriesNoSecrets(t *testing.T) {
	r := newPaymentsEnv(t, true).do(t, call{method: "GET", path: "/demo"})
	for _, s := range []string{string(secret), string(whSecret), whMerchant} {
		if strings.Contains(string(r.body), s) {
			t.Errorf("demo page contains %q", s)
		}
	}
}

func TestCheckoutAC13_DemoEventsShowOutboxState(t *testing.T) {
	e := newPaymentsEnv(t, true)
	out := createPayment(t, e)
	path := "/v1/demo/payments/" + out.InvoiceNo + "/events"
	outcomeOf(t, postWebhook(t, e.env, webhookBody(t, whSecret, out.InvoiceNo, "1000.00")))
	r := e.do(t, call{method: "GET", path: path, token: e.token(t)})
	var ev demoEventsResponse
	if r.status != http.StatusOK || json.Unmarshal(r.body, &ev) != nil {
		t.Fatalf("status = %d body = %s", r.status, r.body)
	}
	if ev.Publisher != PublisherInProcess || len(ev.Events) != 1 || ev.Events[0].Key != out.ID ||
		ev.Events[0].PublishedAt != nil || !strings.Contains(string(ev.Events[0].Payload), `"amount_minor":100000`) {
		t.Fatalf("events = %s", r.body)
	}
	assertError(t, e.do(t, call{method: "GET", path: path, token: e.tokenFor(t, "cust-other")}), http.StatusNotFound, "NOT_FOUND")
	assertError(t, e.do(t, call{method: "GET", path: path}), http.StatusUnauthorized, "UNAUTHORIZED")

	// Disabled demo: the route does not exist.
	off := newPaymentsEnv(t, false)
	offOut := createPayment(t, off)
	assertError(t, off.do(t, call{method: "GET", path: "/v1/demo/payments/" + offOut.InvoiceNo + "/events", token: off.token(t)}),
		http.StatusNotFound, "NOT_FOUND")
}

func TestCheckoutAC10_ReturnURLServesTheDemoPage(t *testing.T) {
	r := newPaymentsEnv(t, true).do(t, call{method: "GET", path: "/demo/return?invoiceNo=INV1"})
	if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d", r.status)
	}
	assertError(t, newPaymentsEnv(t, false).do(t, call{method: "GET", path: "/demo/return"}),
		http.StatusNotFound, "NOT_FOUND")
}
