//go:build integration

package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	orderingservice "github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Spec order-flow-modules AC-06, AC-07 on PostgreSQL: row lock + processed_events.
func TestIntegration_PaymentEventConfirmsOrderOnce(t *testing.T) {
	_, db := setupPayments(t)
	tx := NewOrderingTxManager(db)
	o, err := ordering.NewOrder("5b1d7c2e-8f3a-4c6b-9e0d-1a2b3c4d5e6f", "cust-it",
		[]ordering.Item{{ProductID: "CERAMIC-MUG", Quantity: 1}},
		map[string]ordering.PricedProduct{"CERAMIC-MUG": {ID: "CERAMIC-MUG", Name: "Mug", Price: kernel.Money{Amount: 29000, Currency: kernel.THB}}},
		time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.WithinTx(context.Background(), func(ctx context.Context, r orderingport.Repositories) error {
		return r.Orders.Create(ctx, o)
	}); err != nil {
		t.Fatal(err)
	}
	h := orderingservice.NewPaymentEventService(tx, system.Clock{})
	e := orderingport.PaymentStatusChanged{EventID: "7f0c2b1e-5d4a-4e8b-9a61-3c2d1e0f9a8b", OrderID: o.ID, Succeeded: true, Amount: o.Amount}

	var mu sync.Mutex
	outcomes := map[orderingport.EventOutcome]int{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			out, err := h.HandlePaymentStatusChanged(context.Background(), e)
			if err != nil {
				t.Errorf("err = %v", err)
				return
			}
			mu.Lock()
			outcomes[out]++
			mu.Unlock()
		})
	}
	wg.Wait()
	if outcomes[orderingport.EventApplied] != 1 || outcomes[orderingport.EventAlreadyProcessed] != 19 {
		t.Fatalf("outcomes = %v", outcomes)
	}
	var status string
	if err := db.Raw("SELECT status FROM ordering.orders WHERE id = ?", o.ID).Scan(&status).Error; err != nil || status != "PAID" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	var n int64
	if err := db.Table("ordering.processed_events").Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("processed_events = %d, err = %v", n, err)
	}
}
