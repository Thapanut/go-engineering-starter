package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
)

// Synthetic test data only (spec payment-checkout).
const (
	owner    = "cust-owner"
	stranger = "cust-stranger"
)

var thousandBaht = domain.Money{Amount: 100000, Currency: domain.THB}

type stubGateway struct{ err error }

func (g stubGateway) CreateSession(_ context.Context, p domain.Payment) (port.PaymentSession, error) {
	if g.err != nil {
		return port.PaymentSession{}, g.err
	}
	return port.PaymentSession{Token: "tok-" + p.InvoiceNo, CheckoutURL: "https://checkout.example.invalid/" + p.InvoiceNo}, nil
}

func newCheckout(gw port.PaymentGateway) (*service.CheckoutService, *memory.Store) {
	st := memory.NewStore()
	return service.NewCheckoutService(st, gw, &steppingClock{}, system.UUIDGenerator{}), st
}

func create(t *testing.T, svc *service.CheckoutService, orderID string) port.Checkout {
	t.Helper()
	co, err := svc.CreatePayment(context.Background(), port.CreatePaymentCommand{CustomerID: owner, OrderID: orderID, Amount: thousandBaht})
	if err != nil {
		t.Fatal(err)
	}
	return co
}

func TestCheckoutAC01_CreatesPendingPaymentForTheGivenAmount(t *testing.T) {
	svc, _ := newCheckout(stubGateway{})
	co := create(t, svc, "ORD-1")
	p := co.Payment
	if p.Status != domain.PaymentPending || p.OrderID != "ORD-1" || p.CustomerID != owner || p.Amount != thousandBaht || p.CreatedAt.IsZero() {
		t.Fatalf("payment = %+v", p)
	}
	if !strings.HasPrefix(p.InvoiceNo, "INV") || len(p.InvoiceNo) != 35 || strings.ContainsAny(p.InvoiceNo, "-abcdef") {
		t.Fatalf("invoiceNo = %q, want INV + 32 upper-case hex", p.InvoiceNo)
	}
	if co.Session.Token != "tok-"+p.InvoiceNo || co.Session.CheckoutURL == "" {
		t.Fatalf("session = %+v", co.Session)
	}
	stored, err := svc.GetPayment(context.Background(), owner, p.InvoiceNo)
	if err != nil || stored != p {
		t.Fatalf("stored = %+v, err = %v; want %+v", stored, err, p)
	}
}

func TestCheckoutAC02_InvalidInputStoresNothing(t *testing.T) {
	tx := &countingTx{TxManager: memory.NewStore()}
	svc := service.NewCheckoutService(tx, stubGateway{}, &steppingClock{}, system.UUIDGenerator{})
	for name, cmd := range map[string]port.CreatePaymentCommand{
		"bad order":   {CustomerID: owner, OrderID: "ORD 1", Amount: thousandBaht},
		"no customer": {OrderID: "ORD-1", Amount: thousandBaht},
		"zero amount": {CustomerID: owner, OrderID: "ORD-1", Amount: domain.Money{Currency: domain.THB}},
		"bad ccy":     {CustomerID: owner, OrderID: "ORD-1", Amount: domain.Money{Amount: 1, Currency: "baht"}},
	} {
		if _, err := svc.CreatePayment(context.Background(), cmd); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	if tx.calls.Load() != 0 {
		t.Fatalf("unit of work opened %d times for invalid input", tx.calls.Load())
	}
}

func TestCheckoutAC04_EachCreateGetsItsOwnInvoice(t *testing.T) {
	svc, _ := newCheckout(stubGateway{})
	a, b := create(t, svc, "ORD-1"), create(t, svc, "ORD-1")
	if a.Payment.InvoiceNo == b.Payment.InvoiceNo || a.Payment.ID == b.Payment.ID {
		t.Fatalf("same invoice/id for two attempts: %+v %+v", a.Payment, b.Payment)
	}
}

func TestCheckoutAC05_GatewayFailureLeavesPaymentPending(t *testing.T) {
	boom := errors.New("2c2p unreachable")
	st := memory.NewStore()
	ids := &recordingIDs{}
	svc := service.NewCheckoutService(st, stubGateway{err: boom}, &steppingClock{}, ids)
	_, err := svc.CreatePayment(context.Background(), port.CreatePaymentCommand{CustomerID: owner, OrderID: "ORD-1", Amount: thousandBaht})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	invoiceNo := "INV" + strings.ToUpper(strings.ReplaceAll(ids.last, "-", ""))
	p, err := service.NewCheckoutService(st, stubGateway{}, &steppingClock{}, ids).GetPayment(context.Background(), owner, invoiceNo)
	if err != nil || p.Status != domain.PaymentPending {
		t.Fatalf("payment = %+v, err = %v; want it stored PENDING", p, err)
	}
}

// recordingIDs remembers the last id, which CreatePayment turns into the invoice no.
type recordingIDs struct{ last string }

func (r *recordingIDs) NewID() string {
	r.last = system.UUIDGenerator{}.NewID()
	return r.last
}

func TestCheckoutAC06_OwnerReadsStatus(t *testing.T) {
	svc, _ := newCheckout(stubGateway{})
	co := create(t, svc, "ORD-1")
	p, err := svc.GetPayment(context.Background(), owner, co.Payment.InvoiceNo)
	if err != nil || p.Status != domain.PaymentPending || p.OrderID != "ORD-1" {
		t.Fatalf("payment = %+v, err = %v", p, err)
	}
}

func TestCheckoutAC07_OtherCustomerAndUnknownInvoiceAreNotFound(t *testing.T) {
	svc, _ := newCheckout(stubGateway{})
	co := create(t, svc, "ORD-1")
	for name, c := range map[string][2]string{
		"stranger":    {stranger, co.Payment.InvoiceNo},
		"no customer": {"", co.Payment.InvoiceNo},
		"unknown":     {owner, "INV-UNKNOWN"},
	} {
		if _, err := svc.GetPayment(context.Background(), c[0], c[1]); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestCheckoutAC08_WebhookOutcomeIsVisibleInStatus(t *testing.T) {
	st, clock := memory.NewStore(), &steppingClock{}
	svc := service.NewCheckoutService(st, stubGateway{}, clock, system.UUIDGenerator{})
	co := create(t, svc, "ORD-1")
	verifier := &stubVerifier{n: domain.PaymentNotification{InvoiceNo: co.Payment.InvoiceNo, ProviderRef: "tr-1",
		Amount: thousandBaht, Outcome: domain.PaymentSuccess, ProviderCode: "0000"}}
	webhooks := service.NewWebhookService(verifier, st, clock, &seqIDs{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if res, err := webhooks.HandlePaymentNotification(context.Background(), validBody); err != nil || res.Outcome != domain.OutcomeProcessed {
		t.Fatalf("webhook: res=%+v err=%v", res, err)
	}
	p, err := svc.GetPayment(context.Background(), owner, co.Payment.InvoiceNo)
	if err != nil || p.Status != domain.PaymentSuccess || !p.UpdatedAt.After(p.CreatedAt) {
		t.Fatalf("payment = %+v, err = %v", p, err)
	}
}

func TestCheckoutAC13_PaymentEventsAreOwnerOnly(t *testing.T) {
	st, clock := memory.NewStore(), &steppingClock{}
	svc := service.NewCheckoutService(st, stubGateway{}, clock, system.UUIDGenerator{})
	co := create(t, svc, "ORD-1")
	if recs, err := svc.ListPaymentEvents(context.Background(), owner, co.Payment.InvoiceNo); err != nil || len(recs) != 0 {
		t.Fatalf("before webhook: %+v, %v", recs, err)
	}
	verifier := &stubVerifier{n: domain.PaymentNotification{InvoiceNo: co.Payment.InvoiceNo, ProviderRef: "tr-1",
		Amount: thousandBaht, Outcome: domain.PaymentSuccess, ProviderCode: "0000"}}
	webhooks := service.NewWebhookService(verifier, st, clock, &seqIDs{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if _, err := webhooks.HandlePaymentNotification(context.Background(), validBody); err != nil {
		t.Fatal(err)
	}
	recs, err := svc.ListPaymentEvents(context.Background(), owner, co.Payment.InvoiceNo)
	if err != nil || len(recs) != 1 || recs[0].Message.Key != co.Payment.ID || !recs[0].PublishedAt.IsZero() {
		t.Fatalf("after webhook: %+v, %v", recs, err)
	}
	if _, err := svc.ListPaymentEvents(context.Background(), stranger, co.Payment.InvoiceNo); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stranger: err = %v, want ErrNotFound", err)
	}
}
