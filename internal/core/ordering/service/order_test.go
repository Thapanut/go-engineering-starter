package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Synthetic test data only (spec order-flow-modules).
const (
	owner    = "cust-owner"
	stranger = "cust-stranger"
)

func thb(v int64) kernel.Money { return kernel.Money{Amount: v, Currency: kernel.THB} }

type fakePrices struct{ calls atomic.Int64 }

func (f *fakePrices) FindProducts(_ context.Context, ids []string) (map[string]domain.PricedProduct, error) {
	f.calls.Add(1)
	all := map[string]domain.PricedProduct{
		"04abef6a-166a-45f1-8004-904d9607a857": {ID: "04abef6a-166a-45f1-8004-904d9607a857", Name: "Beans", Price: thb(45000)},
		"959e6207-8780-45c0-885b-be846a8f147f": {ID: "959e6207-8780-45c0-885b-be846a8f147f", Name: "Mug", Price: thb(29000)},
	}
	out := map[string]domain.PricedProduct{}
	for _, id := range ids {
		if p, ok := all[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

type fakePayments struct {
	err   error
	calls []domain.Order
}

func (f *fakePayments) StartPayment(_ context.Context, o domain.Order) (port.PaymentHandoff, error) {
	f.calls = append(f.calls, o)
	if f.err != nil {
		return port.PaymentHandoff{}, f.err
	}
	return port.PaymentHandoff{InvoiceNo: "INV-" + o.ID, Token: "tok", CheckoutURL: "https://checkout.example.invalid/tok"}, nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC) }

type seqIDs struct{ n atomic.Int64 }

func (s *seqIDs) NewID() string { return fmt.Sprintf("ord-%d", s.n.Add(1)) }

type fixture struct {
	svc      *service.OrderService
	store    *memory.OrderStore
	prices   *fakePrices
	payments *fakePayments
}

func newFixture() *fixture {
	f := &fixture{store: memory.NewOrderStore(), prices: &fakePrices{}, payments: &fakePayments{}}
	f.svc = service.NewOrderService(f.store, f.prices, f.payments, fixedClock{}, &seqIDs{})
	return f
}

var cart = []domain.Item{{ProductID: "04abef6a-166a-45f1-8004-904d9607a857", Quantity: 2}, {ProductID: "959e6207-8780-45c0-885b-be846a8f147f", Quantity: 1}}

func TestOrderFlowAC02_PlaceOrderStoresItAndStartsPayment(t *testing.T) {
	f := newFixture()
	placed, err := f.svc.PlaceOrder(context.Background(), port.PlaceOrderCommand{CustomerID: owner, Items: cart})
	if err != nil {
		t.Fatal(err)
	}
	o := placed.Order
	if o.Status != domain.AwaitingPayment || o.Amount != thb(119000) || o.InvoiceNo != "INV-"+o.ID || placed.Payment.Token != "tok" {
		t.Fatalf("placed = %+v", placed)
	}
	if len(f.payments.calls) != 1 || f.payments.calls[0].Amount != thb(119000) || f.payments.calls[0].CustomerID != owner {
		t.Fatalf("payment calls = %+v", f.payments.calls)
	}
	stored, err := f.svc.GetOrder(context.Background(), owner, o.ID)
	if err != nil || stored.InvoiceNo != o.InvoiceNo || len(stored.Lines) != 2 || stored.Amount != o.Amount {
		t.Fatalf("stored = %+v, err = %v", stored, err)
	}
}

func TestOrderFlowAC03_InvalidCartReachesNoOtherModule(t *testing.T) {
	f := newFixture()
	for name, items := range map[string][]domain.Item{
		"empty":     nil,
		"duplicate": {{ProductID: "959e6207-8780-45c0-885b-be846a8f147f", Quantity: 1}, {ProductID: "959e6207-8780-45c0-885b-be846a8f147f", Quantity: 1}},
	} {
		if _, err := f.svc.PlaceOrder(context.Background(), port.PlaceOrderCommand{CustomerID: owner, Items: items}); !errors.Is(err, kernel.ErrValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if f.prices.calls.Load() != 0 {
		t.Fatal("catalog called for an invalid cart")
	}
	_, err := f.svc.PlaceOrder(context.Background(), port.PlaceOrderCommand{CustomerID: owner,
		Items: []domain.Item{{ProductID: "bdb770cd-3bbe-4fe0-a0c6-2bea0db94c1c", Quantity: 1}}})
	if !errors.Is(err, kernel.ErrValidation) || len(f.payments.calls) != 0 {
		t.Fatalf("unknown product: err = %v, payment calls = %d", err, len(f.payments.calls))
	}
}

func TestOrderFlowAC04_PaymentFailureLeavesOrderAwaitingWithoutInvoice(t *testing.T) {
	f := newFixture()
	boom := errors.New("payment unavailable")
	f.payments.err = boom
	_, err := f.svc.PlaceOrder(context.Background(), port.PlaceOrderCommand{CustomerID: owner, Items: cart})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	o, err := f.svc.GetOrder(context.Background(), owner, f.payments.calls[0].ID)
	if err != nil || o.Status != domain.AwaitingPayment || o.InvoiceNo != "" {
		t.Fatalf("order = %+v, err = %v", o, err)
	}
}

func TestOrderFlowAC05_OrdersAreOwnerOnly(t *testing.T) {
	f := newFixture()
	placed, err := f.svc.PlaceOrder(context.Background(), port.PlaceOrderCommand{CustomerID: owner, Items: cart})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][2]string{
		"stranger": {stranger, placed.Order.ID}, "no customer": {"", placed.Order.ID}, "unknown": {owner, "nope"},
	} {
		if _, err := f.svc.GetOrder(context.Background(), c[0], c[1]); !errors.Is(err, kernel.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}
