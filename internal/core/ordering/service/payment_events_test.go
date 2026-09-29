package service_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// placed returns a fixture with one order awaiting payment of 1,190.00 THB.
func placed(t *testing.T) (*fixture, *service.PaymentEventService, domain.Order) {
	t.Helper()
	f := newFixture()
	p, err := f.svc.PlaceOrder(context.Background(), port.PlaceOrderCommand{CustomerID: owner, Items: cart})
	if err != nil {
		t.Fatal(err)
	}
	return f, service.NewPaymentEventService(f.store, fixedClock{}), p.Order
}

func event(id string, o domain.Order, succeeded bool) port.PaymentStatusChanged {
	return port.PaymentStatusChanged{EventID: id, OrderID: o.ID, Succeeded: succeeded, Amount: o.Amount}
}

func (f *fixture) status(t *testing.T, id string) domain.Status {
	t.Helper()
	o, err := f.svc.GetOrder(context.Background(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	return o.Status
}

func TestOrderFlowAC06_PaymentEventConfirmsOrder(t *testing.T) {
	for succeeded, want := range map[bool]domain.Status{true: domain.Paid, false: domain.PaymentFailed} {
		f, h, o := placed(t)
		out, err := h.HandlePaymentStatusChanged(context.Background(), event("evt-1", o, succeeded))
		if err != nil || out != port.EventApplied || f.status(t, o.ID) != want {
			t.Fatalf("succeeded=%v: out=%s err=%v status=%s", succeeded, out, err, f.status(t, o.ID))
		}
	}
}

func TestOrderFlowAC07_RedeliveryAppliesOnce(t *testing.T) {
	_, h, o := placed(t)
	ctx := context.Background()
	if out, _ := h.HandlePaymentStatusChanged(ctx, event("evt-1", o, true)); out != port.EventApplied {
		t.Fatalf("first = %s", out)
	}
	if out, _ := h.HandlePaymentStatusChanged(ctx, event("evt-1", o, true)); out != port.EventAlreadyProcessed {
		t.Fatalf("same event again = %s", out)
	}
	if out, _ := h.HandlePaymentStatusChanged(ctx, event("evt-2", o, true)); out != port.EventDuplicate {
		t.Fatalf("new event, same outcome = %s", out)
	}
}

func TestOrderFlowAC07_ConcurrentDeliveriesApplyOnce(t *testing.T) {
	_, h, o := placed(t)
	var applied, seen atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			out, err := h.HandlePaymentStatusChanged(context.Background(), event("evt-1", o, true))
			switch {
			case err != nil:
				t.Errorf("err = %v", err)
			case out == port.EventApplied:
				applied.Add(1)
			case out == port.EventAlreadyProcessed:
				seen.Add(1)
			}
		})
	}
	wg.Wait()
	if applied.Load() != 1 || seen.Load() != 19 {
		t.Fatalf("applied=%d already=%d", applied.Load(), seen.Load())
	}
}

func TestOrderFlowAC08_ConflictingEventIsIgnored(t *testing.T) {
	f, h, o := placed(t)
	_, _ = h.HandlePaymentStatusChanged(context.Background(), event("evt-1", o, true))
	if out, _ := h.HandlePaymentStatusChanged(context.Background(), event("evt-2", o, false)); out != port.EventConflictIgnored || f.status(t, o.ID) != domain.Paid {
		t.Fatalf("out=%s status=%s", out, f.status(t, o.ID))
	}
}

func TestOrderFlowAC09_AmountMismatchIsRecordedNotApplied(t *testing.T) {
	f, h, o := placed(t)
	e := event("evt-1", o, true)
	e.Amount.Amount = 1
	if out, err := h.HandlePaymentStatusChanged(context.Background(), e); err != nil || out != port.EventAmountMismatch {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if f.status(t, o.ID) != domain.AwaitingPayment {
		t.Fatal("order changed on amount mismatch")
	}
	if out, _ := h.HandlePaymentStatusChanged(context.Background(), e); out != port.EventAlreadyProcessed {
		t.Fatalf("mismatch not recorded: %s", out)
	}
}

func TestOrderFlowAC10_UnknownOrderIsRecordedAndIgnored(t *testing.T) {
	_, h, o := placed(t)
	for id, orderID := range map[string]string{"evt-a": "no-such-order", "evt-b": ""} {
		e := event(id, o, true)
		e.OrderID = orderID
		if out, err := h.HandlePaymentStatusChanged(context.Background(), e); err != nil || out != port.EventUnknownOrder {
			t.Fatalf("%q: out=%s err=%v", orderID, out, err)
		}
	}
	if _, err := h.HandlePaymentStatusChanged(context.Background(), port.PaymentStatusChanged{}); !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("no event id: err = %v", err)
	}
}
