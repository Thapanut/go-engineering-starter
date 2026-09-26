//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
)

// Synthetic test values only.
var (
	itSecret   = []byte("it-2c2p-secret-key-0123456789abcdef!")
	itMerchant = "JT04"
)

const itInvoice = "INV-IT-0001"

func setupPayments(t *testing.T) (*service.WebhookService, *gorm.DB) {
	t.Helper()
	m, db := setup(t)
	for _, f := range []string{"../../../../migrations/0001_payments.down.sql", "../../../../migrations/0001_payments.up.sql"} {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(string(sql)).Error; err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	err := m.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		return r.Payments.Create(ctx, domain.Payment{
			ID: "0e6a4f6e-6a1f-4f5e-9d6e-2b7f3c1a9d01", InvoiceNo: itInvoice,
			Amount: domain.Money{Amount: 23087, Currency: domain.THB}, Status: domain.PaymentPending,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := service.NewWebhookService(twoc2p.NewValidator(itSecret, itMerchant), m, system.Clock{}, slog.Default())
	return svc, db
}

func itBody(t *testing.T, respCode, amount string) []byte {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"merchantID": itMerchant, "invoiceNo": itInvoice, "amount": amount, "currencyCode": "THB",
		"tranRef": "2868821", "respCode": respCode,
	})
	body, err := twoc2p.Sign(itSecret, payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func stored(t *testing.T, db *gorm.DB) paymentModel {
	t.Helper()
	var m paymentModel
	if err := db.Where("invoice_no = ?", itInvoice).Take(&m).Error; err != nil {
		t.Fatal(err)
	}
	return m
}

func TestIntegration_AC01_AC03_ProcessThenDuplicate(t *testing.T) {
	svc, db := setupPayments(t)
	ctx := context.Background()
	res, err := svc.HandlePaymentNotification(ctx, itBody(t, "0000", "230.87"))
	if err != nil || res.Outcome != domain.OutcomeProcessed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	first := stored(t, db)
	if first.Status != "SUCCESS" || first.ProviderRef != "2868821" || first.ProviderCode != "0000" {
		t.Fatalf("stored = %+v", first)
	}
	res, err = svc.HandlePaymentNotification(ctx, itBody(t, "0000", "230.87"))
	if err != nil || res.Outcome != domain.OutcomeDuplicate {
		t.Fatalf("duplicate: res=%+v err=%v", res, err)
	}
	if again := stored(t, db); !again.UpdatedAt.Equal(first.UpdatedAt) || again.Status != first.Status {
		t.Fatalf("row modified by duplicate: %+v → %+v", first, again)
	}
}

func TestIntegration_AC08_MismatchRollsBack(t *testing.T) {
	svc, db := setupPayments(t)
	_, err := svc.HandlePaymentNotification(context.Background(), itBody(t, "0000", "1.00"))
	if !errors.Is(err, domain.ErrPaymentMismatch) {
		t.Fatalf("err = %v", err)
	}
	if s := stored(t, db).Status; s != "PENDING" {
		t.Fatalf("status = %s, want PENDING", s)
	}
}

func TestIntegration_AC10_ConcurrentDeliveriesTransitionOnce(t *testing.T) {
	svc, db := setupPayments(t)
	body := itBody(t, "0000", "230.87")
	var mu sync.Mutex
	outcomes := map[domain.WebhookOutcome]int{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.HandlePaymentNotification(context.Background(), body)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			mu.Lock()
			outcomes[res.Outcome]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if outcomes[domain.OutcomeProcessed] != 1 || outcomes[domain.OutcomeDuplicate] != 19 {
		t.Fatalf("outcomes = %v, want 1 PROCESSED and 19 DUPLICATE", outcomes)
	}
	if s := stored(t, db).Status; s != "SUCCESS" {
		t.Fatalf("status = %s", s)
	}
}

func TestIntegration_ConditionalUpdateGuardsTerminalRows(t *testing.T) {
	_, db := setupPayments(t)
	repo := paymentRepo{db: db}
	p := stored(t, db).toDomain()
	p.Status = domain.PaymentSuccess
	if err := repo.UpdateOutcome(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p.Status = domain.PaymentFailed
	if err := repo.UpdateOutcome(context.Background(), p); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second update err = %v, want ErrConflict", err)
	}
}
