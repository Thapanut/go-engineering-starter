//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

func outboxRows(t *testing.T, db *gorm.DB) []outboxModel {
	t.Helper()
	var rows []outboxModel
	if err := db.Order("created_at, id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestIntegration_Outbox_TransitionWritesEventAtomically(t *testing.T) {
	svc, db := setupPayments(t)
	ctx := context.Background()
	if _, err := svc.HandlePaymentNotification(ctx, itBody(t, "0000", "230.87")); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.HandlePaymentNotification(ctx, itBody(t, "0000", "230.87")) // DUPLICATE: no second event
	rows := outboxRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("outbox rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Topic != "payments.v1.status-changed" || r.MessageKey != stored(t, db).ID || r.PublishedAt != nil {
		t.Fatalf("row = %+v", r)
	}
}

func TestIntegration_Outbox_MismatchWritesNoEvent(t *testing.T) {
	svc, db := setupPayments(t)
	if _, err := svc.HandlePaymentNotification(context.Background(), itBody(t, "0000", "1.00")); !errors.Is(err, domain.ErrPaymentMismatch) {
		t.Fatalf("err = %v", err)
	}
	if rows := outboxRows(t, db); len(rows) != 0 {
		t.Fatalf("outbox rows = %d, want 0", len(rows))
	}
}

func seedEvents(t *testing.T, m *TxManager, n int) {
	t.Helper()
	err := m.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		for i := 1; i <= n; i++ {
			e := domain.PaymentStatusChanged{
				EventID:   fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
				PaymentID: fmt.Sprintf("10000000-0000-4000-8000-%012d", i), InvoiceNo: fmt.Sprintf("INV-%04d", i),
				Status: domain.PaymentSuccess, Amount: domain.Money{Amount: 100, Currency: domain.THB}, ProviderRef: "ref",
				OccurredAt: time.Date(2026, 9, 27, 10, 0, i, 0, time.UTC),
			}
			if err := r.Outbox.AddPaymentStatusChanged(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegration_Outbox_ClaimSkipsRowsLockedByAnotherRelay(t *testing.T) {
	m, db := setup(t)
	applyMigrations(t, db)
	seedEvents(t, m, 4)

	// Relay A claims 2 and holds its transaction open while relay B claims.
	claimedA := make(chan []port.OutboxMessage)
	releaseA := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = m.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
			msgs, err := r.Outbox.ClaimPending(ctx, 2)
			if err != nil {
				t.Error(err)
			}
			claimedA <- msgs
			<-releaseA
			return nil
		})
	}()
	a := <-claimedA
	var b []port.OutboxMessage
	err := m.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		var err error
		b, err = r.Outbox.ClaimPending(ctx, 10)
		return err
	})
	close(releaseA)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("relay A got %d, relay B got %d; want 2 and 2", len(a), len(b))
	}
	seen := map[string]bool{}
	for _, msg := range append(a, b...) {
		if seen[msg.ID] {
			t.Fatalf("message %s claimed by both relays", msg.ID)
		}
		seen[msg.ID] = true
	}
}

func TestIntegration_Outbox_MarkPublishedHidesRowsFromNextClaim(t *testing.T) {
	m, db := setup(t)
	applyMigrations(t, db)
	seedEvents(t, m, 3)
	at := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)
	err := m.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		msgs, err := r.Outbox.ClaimPending(ctx, 2)
		if err != nil {
			return err
		}
		return r.Outbox.MarkPublished(ctx, []string{msgs[0].ID, msgs[1].ID}, at)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := outboxRows(t, db)
	if rows[0].PublishedAt == nil || !rows[0].PublishedAt.Equal(at) || rows[1].PublishedAt == nil || rows[2].PublishedAt != nil {
		t.Fatalf("published_at = %v, %v, %v", rows[0].PublishedAt, rows[1].PublishedAt, rows[2].PublishedAt)
	}
	err = m.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		return r.Outbox.MarkPublished(ctx, []string{rows[0].ID}, at) // already published
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("re-mark err = %v, want ErrConflict", err)
	}
}
