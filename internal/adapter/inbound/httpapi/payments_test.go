package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/catalog"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

// Synthetic test data only (spec payment-checkout).
// One of each sample product: 450.00 + 290.00 + 260.00 = 1,000.00 THB.
const createBody = `{"orderId":"ORD-HTTP-1","items":[{"productId":"COFFEE-BEANS-250G","quantity":1},` +
	`{"productId":"CERAMIC-MUG","quantity":1},{"productId":"POUR-OVER-DRIPPER","quantity":1}]}`

type failingGateway struct{}

func (failingGateway) CreateSession(context.Context, domain.Payment) (port.PaymentSession, error) {
	return port.PaymentSession{}, errors.New("dial tcp 203.0.113.7:443: connection refused")
}

// newPaymentsEnv wires checkout, webhook, and (optionally) the demo page on one memory store.
func newPaymentsEnv(t *testing.T, gw port.PaymentGateway, demo bool) *env {
	t.Helper()
	st := memory.NewStore()
	e := &env{jwt: auth.NewJWT(secret, issuer), logs: &syncBuffer{}}
	log := slog.New(slog.NewJSONHandler(e.logs, nil))
	checkout := service.NewCheckoutService(st, catalog.Sample(), gw, system.Clock{}, system.UUIDGenerator{})
	public := []PublicModule{WebhookModule{UseCase: service.NewWebhookService(
		twoc2p.NewVerifier(whSecret, whMerchant), st, system.Clock{}, system.UUIDGenerator{}, log)}}
	modules := []Module{PaymentModule{UseCase: checkout}}
	if demo {
		d := DemoModule{Events: checkout, Publisher: PublisherInProcess}
		public, modules = append(public, d), append(modules, d)
	}
	e.app = NewApp(Deps{Auth: e.jwt, Log: log, RequestTimeout: 5 * time.Second, Public: public}, modules...)
	return e
}

func (e *env) tokenFor(t *testing.T, sub string) string {
	t.Helper()
	tok, err := e.jwt.Issue(sub, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func createPayment(t *testing.T, e *env) paymentCheckoutResponse {
	t.Helper()
	r := e.do(t, call{method: "POST", path: "/v1/payments", token: e.token(t), body: createBody})
	if r.status != http.StatusCreated {
		t.Fatalf("status = %d; body %s", r.status, r.body)
	}
	var out paymentCheckoutResponse
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func getStatus(t *testing.T, e *env, token, invoiceNo string) (response, paymentStatusResponse) {
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

func TestCheckoutAC01_CreatePaymentReturnsSession(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	out := createPayment(t, e)
	if out.PaymentID == "" || !strings.HasPrefix(out.InvoiceNo, "INV") || out.OrderID != "ORD-HTTP-1" || out.Status != "PENDING" ||
		out.amountFields != (amountFields{Amount: "1000.00", AmountMinor: 100000, Currency: "THB"}) {
		t.Fatalf("response = %+v", out)
	}
	if len(out.Lines) != 3 || out.Lines[0] != (orderLineResponse{ProductID: "COFFEE-BEANS-250G", Name: "Arabica Coffee Beans 250 g",
		Quantity: 1, UnitPrice: "450.00", UnitPriceMinor: 45000, LineTotal: "450.00", LineTotalMinor: 45000, Currency: "THB"}) {
		t.Fatalf("lines = %+v", out.Lines)
	}
	if !strings.HasPrefix(out.PaymentToken, "stub_") || out.CheckoutURL != "https://checkout.2c2p.invalid/payment/"+out.PaymentToken {
		t.Fatalf("session = %q %q", out.PaymentToken, out.CheckoutURL)
	}
	if _, err := time.Parse(time.RFC3339Nano, out.CreatedAt); err != nil {
		t.Fatalf("createdAt %q: %v", out.CreatedAt, err)
	}
	if r, st := getStatus(t, e, e.token(t), out.InvoiceNo); r.status != http.StatusOK || st.Status != "PENDING" {
		t.Fatalf("stored payment not readable by its creator: %d %s", r.status, r.body)
	}
}

func TestCheckoutAC02_InvalidBodyIs400(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	for name, body := range map[string]string{
		"malformed":         `{"orderId":`,
		"client amount":     `{"orderId":"ORD-1","items":[{"productId":"CERAMIC-MUG","quantity":1}],"amount":"0.01"}`,
		"item price":        `{"orderId":"ORD-1","items":[{"productId":"CERAMIC-MUG","quantity":1,"price":"0.01"}]}`,
		"no order":          `{"items":[{"productId":"CERAMIC-MUG","quantity":1}]}`,
		"no items":          `{"orderId":"ORD-1"}`,
		"empty items":       `{"orderId":"ORD-1","items":[]}`,
		"bad order":         `{"orderId":"ORD 1","items":[{"productId":"CERAMIC-MUG","quantity":1}]}`,
		"zero quantity":     `{"orderId":"ORD-1","items":[{"productId":"CERAMIC-MUG","quantity":0}]}`,
		"quantity 100":      `{"orderId":"ORD-1","items":[{"productId":"CERAMIC-MUG","quantity":100}]}`,
		"string quantity":   `{"orderId":"ORD-1","items":[{"productId":"CERAMIC-MUG","quantity":"1"}]}`,
		"unknown product":   `{"orderId":"ORD-1","items":[{"productId":"NOPE","quantity":1}]}`,
		"duplicate product": `{"orderId":"ORD-1","items":[{"productId":"CERAMIC-MUG","quantity":1},{"productId":"CERAMIC-MUG","quantity":1}]}`,
		"two objects":       createBody + createBody,
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, e.do(t, call{method: "POST", path: "/v1/payments", token: e.token(t), body: body}),
				http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
}

func TestCheckoutAC03_EndpointsRequireBearerToken(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	out := createPayment(t, e)
	forged, _ := auth.NewJWT([]byte("another-secret-that-is-32-bytes-long"), issuer).Issue(customer, time.Hour, time.Now())
	for _, tok := range []string{"", forged} {
		assertError(t, e.do(t, call{method: "POST", path: "/v1/payments", token: tok, body: createBody}), http.StatusUnauthorized, "UNAUTHORIZED")
		r, _ := getStatus(t, e, tok, out.InvoiceNo)
		assertError(t, r, http.StatusUnauthorized, "UNAUTHORIZED")
	}
}

func TestCheckoutAC05_GatewayFailureIs500WithoutDetail(t *testing.T) {
	e := newPaymentsEnv(t, failingGateway{}, false)
	r := e.do(t, call{method: "POST", path: "/v1/payments", token: e.token(t), body: createBody})
	body := assertError(t, r, http.StatusInternalServerError, "INTERNAL_ERROR")
	if strings.Contains(string(r.body), "203.0.113.7") || body.Message != "internal error" {
		t.Fatalf("internal detail leaked: %s", r.body)
	}
}

func TestCheckoutAC06_AC07_StatusIsOwnerOnly(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
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
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	out := createPayment(t, e)
	if got := outcomeOf(t, postWebhook(t, e, webhookBody(t, whSecret, out.InvoiceNo, "1000.00"))); got != "PROCESSED" {
		t.Fatalf("webhook outcome = %s", got)
	}
	if _, st := getStatus(t, e, e.token(t), out.InvoiceNo); st.Status != "SUCCESS" {
		t.Fatalf("status after webhook = %+v", st)
	}
}

func TestCheckoutAC09_LogsContainNoIdentifiers(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	out := createPayment(t, e)
	getStatus(t, e, e.token(t), out.InvoiceNo)
	logs := e.logs.String()
	for _, forbidden := range []string{out.InvoiceNo, out.OrderID, out.PaymentToken, out.PaymentID} {
		if strings.Contains(logs, forbidden) {
			t.Errorf("log contains %q:\n%s", forbidden, logs)
		}
	}
	for _, route := range []string{`"route":"/v1/payments"`, `"route":"/v1/payments/:invoiceNo/status"`} {
		if !strings.Contains(logs, route) {
			t.Errorf("missing access log %s:\n%s", route, logs)
		}
	}
}

func TestCheckoutAC10_DemoPageOnlyWhenEnabled(t *testing.T) {
	assertError(t, newPaymentsEnv(t, twoc2p.StubGateway{}, false).do(t, call{method: "GET", path: "/demo"}),
		http.StatusNotFound, "NOT_FOUND")

	r := newPaymentsEnv(t, twoc2p.StubGateway{}, true).do(t, call{method: "GET", path: "/demo"})
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
	r := newPaymentsEnv(t, twoc2p.StubGateway{}, true).do(t, call{method: "GET", path: "/demo"})
	for _, s := range []string{string(secret), string(whSecret), whMerchant} {
		if strings.Contains(string(r.body), s) {
			t.Errorf("demo page contains %q", s)
		}
	}
}

func TestCheckoutAC12_ProductsComeFromTheBackend(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	assertError(t, e.do(t, call{method: "GET", path: "/v1/products"}), http.StatusUnauthorized, "UNAUTHORIZED")
	r := e.do(t, call{method: "GET", path: "/v1/products", token: e.token(t)})
	var out productListResponse
	if r.status != http.StatusOK || json.Unmarshal(r.body, &out) != nil || len(out.Products) != 3 {
		t.Fatalf("status = %d body = %s", r.status, r.body)
	}
	if out.Products[1] != (productResponse{ProductID: "CERAMIC-MUG", Name: "Ceramic Mug 350 ml", Price: "290.00", PriceMinor: 29000, Currency: "THB"}) {
		t.Fatalf("product = %+v", out.Products[1])
	}
}

func TestCheckoutAC13_DemoEventsShowOutboxState(t *testing.T) {
	e := newPaymentsEnv(t, twoc2p.StubGateway{}, true)
	out := createPayment(t, e)
	path := "/v1/demo/payments/" + out.InvoiceNo + "/events"
	outcomeOf(t, postWebhook(t, e, webhookBody(t, whSecret, out.InvoiceNo, "1000.00")))
	r := e.do(t, call{method: "GET", path: path, token: e.token(t)})
	var ev demoEventsResponse
	if r.status != http.StatusOK || json.Unmarshal(r.body, &ev) != nil {
		t.Fatalf("status = %d body = %s", r.status, r.body)
	}
	if ev.Publisher != PublisherInProcess || len(ev.Events) != 1 || ev.Events[0].Key != out.PaymentID ||
		ev.Events[0].PublishedAt != nil || !strings.Contains(string(ev.Events[0].Payload), `"amount_minor":100000`) {
		t.Fatalf("events = %s", r.body)
	}
	assertError(t, e.do(t, call{method: "GET", path: path, token: e.tokenFor(t, "cust-other")}), http.StatusNotFound, "NOT_FOUND")
	assertError(t, e.do(t, call{method: "GET", path: path}), http.StatusUnauthorized, "UNAUTHORIZED")

	// Disabled demo: the route does not exist.
	off := newPaymentsEnv(t, twoc2p.StubGateway{}, false)
	offOut := createPayment(t, off)
	assertError(t, off.do(t, call{method: "GET", path: "/v1/demo/payments/" + offOut.InvoiceNo + "/events", token: off.token(t)}),
		http.StatusNotFound, "NOT_FOUND")
}

func TestCheckoutAC10_ReturnURLServesTheDemoPage(t *testing.T) {
	r := newPaymentsEnv(t, twoc2p.StubGateway{}, true).do(t, call{method: "GET", path: "/demo/return?invoiceNo=INV1"})
	if r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
		t.Fatalf("status = %d", r.status)
	}
	assertError(t, newPaymentsEnv(t, twoc2p.StubGateway{}, false).do(t, call{method: "GET", path: "/demo/return"}),
		http.StatusNotFound, "NOT_FOUND")
}
