package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// Synthetic test data only.
func event(i int) domain.PaymentStatusChanged {
	return domain.PaymentStatusChanged{
		EventID: fmt.Sprintf("evt-%d", i), PaymentID: fmt.Sprintf("pay-%d", i), InvoiceNo: "INV",
		Status: domain.PaymentSuccess, Amount: domain.Money{Amount: 100, Currency: domain.THB},
		ProviderRef: "ref", OccurredAt: time.Date(2026, 9, 27, 10, 0, i, 0, time.UTC),
	}
}

func claim(t *testing.T, s *Store, limit int) []string {
	t.Helper()
	var ids []string
	err := s.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		msgs, err := r.Outbox.ClaimPending(ctx, limit)
		for _, m := range msgs {
			ids = append(ids, m.ID)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestOutboxClaimsOldestFirstUpToLimitAndSkipsPublished(t *testing.T) {
	s := NewStore()
	err := s.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		for i := 1; i <= 3; i++ {
			if err := r.Outbox.AddPaymentStatusChanged(ctx, event(i)); err != nil {
				return err
			}
		}
		return r.Outbox.MarkPublished(ctx, []string{"evt-1"}, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(claim(t, s, 1)); got != "[evt-2]" {
		t.Fatalf("claim(1) = %s", got)
	}
	if got := fmt.Sprint(claim(t, s, 10)); got != "[evt-2 evt-3]" {
		t.Fatalf("claim(10) = %s", got)
	}
}

func TestOutboxWritesRollBackWithTheTransaction(t *testing.T) {
	s := NewStore()
	boom := errors.New("boom")
	err := s.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		if err := r.Outbox.AddPaymentStatusChanged(ctx, event(1)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if ids := claim(t, s, 10); len(ids) != 0 {
		t.Fatalf("rolled-back event visible: %v", ids)
	}
}

func TestOutboxMarkPublishedRejectsUnknownOrAlreadyPublished(t *testing.T) {
	s := NewStore()
	err := s.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		if err := r.Outbox.AddPaymentStatusChanged(ctx, event(1)); err != nil {
			return err
		}
		return r.Outbox.MarkPublished(ctx, []string{"evt-1"}, time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{"evt-1"}, {"evt-404"}} {
		err := s.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
			return r.Outbox.MarkPublished(ctx, ids, time.Now())
		})
		if !errors.Is(err, domain.ErrConflict) {
			t.Errorf("MarkPublished(%v) err = %v, want ErrConflict", ids, err)
		}
	}
}

func TestPublisherKeepsMessagesInOrder(t *testing.T) {
	p := &Publisher{}
	ctx := context.Background()
	_ = p.Publish(ctx, []port.OutboxMessage{{ID: "a"}, {ID: "b"}})
	_ = p.Publish(ctx, []port.OutboxMessage{{ID: "c"}})
	got := p.Messages()
	if len(got) != 3 || got[0].ID != "a" || got[2].ID != "c" {
		t.Fatalf("messages = %+v", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := p.Publish(cancelled, []port.OutboxMessage{{ID: "d"}}); err == nil || len(p.Messages()) != 3 {
		t.Fatalf("publish on cancelled ctx: err=%v, messages=%d", err, len(p.Messages()))
	}
}
