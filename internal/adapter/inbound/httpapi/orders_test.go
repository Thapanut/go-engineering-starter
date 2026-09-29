package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/catalogclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/paymentclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	catalogservice "github.com/Thapanut/go-engineering-starter/internal/core/catalog/service"
	orderingservice "github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	paymentservice "github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
)

// newShopEnv wires the three modules the way cmd/api does, on memory stores.
func newShopEnv(t *testing.T) *env {
	t.Helper()
	e := &env{jwt: auth.NewJWT(secret, issuer), logs: &syncBuffer{}}
	log := slog.New(slog.NewJSONHandler(e.logs, nil))
	payStore := memory.NewStore()
	checkout := paymentservice.NewCheckoutService(payStore, twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	catalog := catalogservice.NewCatalogService(memory.SampleProducts())
	orders := orderingservice.NewOrderService(memory.NewOrderStore(), catalogclient.Client{Catalog: catalog},
		paymentclient.Client{Payments: checkout}, system.Clock{}, system.UUIDGenerator{})
	webhooks := paymentservice.NewWebhookService(twoc2p.NewVerifier(whSecret, whMerchant), payStore, system.Clock{}, system.UUIDGenerator{}, log)
	e.app = NewApp(Deps{Auth: e.jwt, Log: log, RequestTimeout: 5 * time.Second, Public: []PublicModule{WebhookModule{UseCase: webhooks}}},
		CatalogModule{UseCase: catalog}, OrderModule{UseCase: orders}, PaymentModule{UseCase: checkout})
	return e
}

// Synthetic cart: 2 × 450.00 + 1 × 290.00 = 1,190.00 THB.
const cartBody = `{"items":[{"productId":"COFFEE-BEANS-250G","quantity":2},{"productId":"CERAMIC-MUG","quantity":1}]}`

func placeOrder(t *testing.T, e *env) placedOrderResponse {
	t.Helper()
	r := e.do(t, call{method: "POST", path: "/v1/orders", token: e.token(t), body: cartBody})
	var out placedOrderResponse
	if r.status != http.StatusCreated || json.Unmarshal(r.body, &out) != nil {
		t.Fatalf("status = %d body = %s", r.status, r.body)
	}
	return out
}

func TestOrderFlowAC02_PlaceOrderAcrossModules(t *testing.T) {
	e := newShopEnv(t)
	out := placeOrder(t, e)
	if out.OrderID == "" || out.Status != "AWAITING_PAYMENT" || len(out.Lines) != 2 ||
		out.amountFields != (amountFields{Amount: "1190.00", AmountMinor: 119000, Currency: "THB"}) ||
		out.Lines[0] != (orderLineResponse{ProductID: "COFFEE-BEANS-250G", Name: "Arabica Coffee Beans 250 g", Quantity: 2,
			UnitPrice: "450.00", UnitPriceMinor: 45000, LineTotal: "900.00", LineTotalMinor: 90000, Currency: "THB"}) {
		t.Fatalf("order = %+v", out)
	}
	if out.Payment.InvoiceNo == "" || out.InvoiceNo != out.Payment.InvoiceNo || !strings.HasPrefix(out.Payment.PaymentToken, "stub_") {
		t.Fatalf("payment = %+v", out.Payment)
	}
	// Payment holds a PENDING payment for this order and amount.
	r := e.do(t, call{method: "GET", path: "/v1/payments/" + out.Payment.InvoiceNo + "/status", token: e.token(t)})
	var pay paymentStatusResponse
	if r.status != http.StatusOK || json.Unmarshal(r.body, &pay) != nil || pay.OrderID != out.OrderID ||
		pay.Status != "PENDING" || pay.AmountMinor != 119000 {
		t.Fatalf("payment status = %d %s", r.status, r.body)
	}
}

func TestOrderFlowAC03_InvalidOrdersAre400(t *testing.T) {
	e := newShopEnv(t)
	for name, body := range map[string]string{
		"malformed":        `{"items":`,
		"no items":         `{}`,
		"empty items":      `{"items":[]}`,
		"client amount":    `{"items":[{"productId":"CERAMIC-MUG","quantity":1}],"amount":"0.01"}`,
		"item price":       `{"items":[{"productId":"CERAMIC-MUG","quantity":1,"price":"0.01"}]}`,
		"client order id":  `{"orderId":"mine","items":[{"productId":"CERAMIC-MUG","quantity":1}]}`,
		"zero quantity":    `{"items":[{"productId":"CERAMIC-MUG","quantity":0}]}`,
		"string quantity":  `{"items":[{"productId":"CERAMIC-MUG","quantity":"1"}]}`,
		"unknown product":  `{"items":[{"productId":"NOPE","quantity":1}]}`,
		"inactive product": `{"items":[{"productId":"HAND-GRINDER","quantity":1}]}`,
		"duplicate":        `{"items":[{"productId":"CERAMIC-MUG","quantity":1},{"productId":"CERAMIC-MUG","quantity":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			assertError(t, e.do(t, call{method: "POST", path: "/v1/orders", token: e.token(t), body: body}),
				http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
	assertError(t, e.do(t, call{method: "POST", path: "/v1/orders", body: cartBody}), http.StatusUnauthorized, "UNAUTHORIZED")
}

func TestOrderFlowAC05_GetOrderIsOwnerOnly(t *testing.T) {
	e := newShopEnv(t)
	out := placeOrder(t, e)
	r := e.do(t, call{method: "GET", path: "/v1/orders/" + out.OrderID, token: e.token(t)})
	var got orderResponse
	if r.status != http.StatusOK || json.Unmarshal(r.body, &got) != nil || got.OrderID != out.OrderID ||
		got.Status != "AWAITING_PAYMENT" || got.InvoiceNo != out.Payment.InvoiceNo || len(got.Lines) != 2 {
		t.Fatalf("status = %d body = %s", r.status, r.body)
	}
	other, _ := auth.NewJWT(secret, issuer).Issue("cust-other", time.Hour, time.Now())
	a := assertError(t, e.do(t, call{method: "GET", path: "/v1/orders/" + out.OrderID, token: other}), http.StatusNotFound, "NOT_FOUND")
	b := assertError(t, e.do(t, call{method: "GET", path: "/v1/orders/nope", token: e.token(t)}), http.StatusNotFound, "NOT_FOUND")
	if a.Message != b.Message {
		t.Fatalf("another customer's order is distinguishable: %q vs %q", a.Message, b.Message)
	}
	if logs := e.logs.String(); strings.Contains(logs, out.OrderID) || strings.Contains(logs, out.Payment.InvoiceNo) {
		t.Fatalf("identifiers in logs:\n%s", logs)
	}
}
