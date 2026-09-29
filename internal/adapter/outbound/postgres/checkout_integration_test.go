//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/catalog"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
)

// Spec payment-checkout AC-01, AC-06, AC-07 on PostgreSQL (migration 0003).
func TestIntegration_CheckoutStoresOwnerAndOrder(t *testing.T) {
	_, db := setupPayments(t)
	svc := service.NewCheckoutService(NewTxManager(db), catalog.Sample(), twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	ctx := context.Background()
	co, err := svc.CreatePayment(ctx, port.CreatePaymentCommand{CustomerID: "cust-it", OrderID: "ORD-IT-1",
		Items: []domain.OrderItem{{ProductID: "CERAMIC-MUG", Quantity: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	var m paymentModel
	if err := db.Where("invoice_no = ?", co.Payment.InvoiceNo).Take(&m).Error; err != nil {
		t.Fatal(err)
	}
	if m.OrderID != "ORD-IT-1" || m.CustomerID != "cust-it" || m.Status != "PENDING" || m.Amount != 58000 {
		t.Fatalf("stored = %+v", m)
	}
	p, err := svc.GetPayment(ctx, "cust-it", co.Payment.InvoiceNo)
	if err != nil || p.ID != co.Payment.ID || p.OrderID != "ORD-IT-1" {
		t.Fatalf("owner read: %+v, %v", p, err)
	}
	if _, err := svc.GetPayment(ctx, "cust-other", co.Payment.InvoiceNo); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other customer: err = %v, want ErrNotFound", err)
	}
}

// Rows created before 0003 have no owner and are readable by nobody.
func TestIntegration_LegacyPaymentHasNoOwner(t *testing.T) {
	_, db := setupPayments(t) // seeds itInvoice without order/customer
	svc := service.NewCheckoutService(NewTxManager(db), catalog.Sample(), twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	if _, err := svc.GetPayment(context.Background(), "cust-it", itInvoice); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Spec payment-checkout AC-13 on PostgreSQL: ListByKey sees the event and its publication.
func TestIntegration_ListPaymentEvents(t *testing.T) {
	webhooks, db := setupPayments(t)
	ctx := context.Background()
	if _, err := webhooks.HandlePaymentNotification(ctx, itBody(t, "0000", "230.87")); err != nil {
		t.Fatal(err)
	}
	paymentID := stored(t, db).ID
	repo := outboxRepo{db: db}
	recs, err := repo.ListByKey(ctx, paymentID)
	if err != nil || len(recs) != 1 || !recs[0].PublishedAt.IsZero() {
		t.Fatalf("before publish: %+v, %v", recs, err)
	}
	if err := repo.MarkPublished(ctx, []string{recs[0].Message.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if recs, _ = repo.ListByKey(ctx, paymentID); recs[0].PublishedAt.IsZero() {
		t.Fatal("published_at not reported")
	}
	if recs, _ = repo.ListByKey(ctx, "no-such-payment"); len(recs) != 0 {
		t.Fatalf("other key: %+v", recs)
	}
}
