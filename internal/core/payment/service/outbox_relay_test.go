package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
)

// fakePublisher records batches and can be told to fail.
type fakePublisher struct {
	mu      sync.Mutex
	batches [][]port.OutboxMessage
	err     error
}

func (p *fakePublisher) Publish(_ context.Context, msgs []port.OutboxMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.batches = append(p.batches, msgs)
	return nil
}

func (p *fakePublisher) ids() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []string
	for _, b := range p.batches {
		for _, m := range b {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// markFailTx makes MarkPublished fail, as if the DB went away after the broker ack.
type markFailTx struct {
	port.TxManager
	err error
}

func (m markFailTx) WithinTx(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return m.TxManager.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		r.Outbox = markFailOutbox{OutboxRepository: r.Outbox, err: m.err}
		return fn(ctx, r)
	})
}

type markFailOutbox struct {
	port.OutboxRepository
	err error
}

func (m markFailOutbox) MarkPublished(context.Context, []string, time.Time) error { return m.err }

// seedOutbox adds n synthetic events evt-1..evt-n, oldest first.
func seedOutbox(t *testing.T, st *memory.Store, n int) {
	t.Helper()
	err := st.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		for i := 1; i <= n; i++ {
			e := domain.PaymentStatusChanged{
				EventID: fmt.Sprintf("evt-%d", i), PaymentID: fmt.Sprintf("pay-%d", i), InvoiceNo: fmt.Sprintf("INV-%04d", i),
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

// lockedBuffer is a log sink that is safe to read while the relay goroutine writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

func newRelay(tx port.TxManager, p port.MessagePublisher, batch int) (*service.OutboxRelay, *lockedBuffer) {
	logs := &lockedBuffer{}
	return service.NewOutboxRelay(tx, p, &steppingClock{}, slog.New(slog.NewJSONHandler(logs, nil)), batch), logs
}

func TestRelayAC05_PublishesPendingInOrderAndMarksThem(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 3)
	pub := &fakePublisher{}
	relay, _ := newRelay(st, pub, 100)

	n, err := relay.RelayOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := fmt.Sprint(pub.ids()); got != "[evt-1 evt-2 evt-3]" {
		t.Fatalf("published %s", got)
	}
	if left := pendingOutbox(t, st); len(left) != 0 {
		t.Fatalf("%d messages still pending", len(left))
	}
	if n, err := relay.RelayOnce(context.Background()); err != nil || n != 0 || len(pub.batches) != 1 {
		t.Fatalf("second run: n=%d err=%v batches=%d (published twice?)", n, err, len(pub.batches))
	}
}

func TestRelayAC05_EmptyOutboxDoesNotCallBroker(t *testing.T) {
	pub := &fakePublisher{}
	relay, _ := newRelay(memory.NewStore(), pub, 100)
	if n, err := relay.RelayOnce(context.Background()); err != nil || n != 0 || len(pub.batches) != 0 {
		t.Fatalf("n=%d err=%v batches=%d", n, err, len(pub.batches))
	}
}

func TestRelayAC06_BrokerFailureKeepsMessagesForRetry(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 2)
	boom := errors.New("broker unavailable")
	pub := &fakePublisher{err: boom}
	relay, _ := newRelay(st, pub, 100)

	if _, err := relay.RelayOnce(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if left := pendingOutbox(t, st); len(left) != 2 {
		t.Fatalf("pending = %d, want 2 (nothing lost)", len(left))
	}
	pub.err = nil // broker is back
	if n, err := relay.RelayOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
}

func TestRelayAC07_MarkFailureRepublishes(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 1)
	pub := &fakePublisher{}
	boom := errors.New("db connection lost")
	failing, _ := newRelay(markFailTx{TxManager: st, err: boom}, pub, 100)
	if _, err := failing.RelayOnce(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	healthy, _ := newRelay(st, pub, 100)
	if _, err := healthy.RelayOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// At-least-once: the ack was received but not recorded, so the event goes out again
	// with the same event_id, which consumers use to deduplicate.
	if got := fmt.Sprint(pub.ids()); got != "[evt-1 evt-1]" {
		t.Fatalf("published %s", got)
	}
}

func TestRelayAC08_BatchSizeBoundsEachTransaction(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 5)
	pub := &fakePublisher{}
	relay, _ := newRelay(st, pub, 2)
	for _, want := range []int{2, 2, 1, 0} {
		if n, err := relay.RelayOnce(context.Background()); err != nil || n != want {
			t.Fatalf("n=%d err=%v, want %d", n, err, want)
		}
	}
	if got := fmt.Sprint(pub.ids()); got != "[evt-1 evt-2 evt-3 evt-4 evt-5]" {
		t.Fatalf("published %s", got)
	}
}

func TestRelayAC08_RunDrainsBacklogWithoutWaitingAndStopsOnCancel(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 5)
	pub := &fakePublisher{}
	relay, _ := newRelay(st, pub, 2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); relay.Run(ctx, time.Hour) }() // only full batches can explain a drain

	deadline := time.After(5 * time.Second)
	for len(pub.ids()) < 5 {
		select {
		case <-deadline:
			t.Fatalf("published %d of 5 within 5s", len(pub.ids()))
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestRelayAC09_FailureIsLoggedWithoutPayload(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 1)
	pub := &fakePublisher{err: errors.New("broker unavailable")}
	relay, logs := newRelay(st, pub, 100)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); relay.Run(ctx, time.Hour) }()
	deadline := time.After(5 * time.Second)
	for !bytes.Contains(logs.Bytes(), []byte("outbox relay failed")) {
		select {
		case <-deadline:
			t.Fatal("no warning logged")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if bytes.Contains(logs.Bytes(), []byte("INV-0001")) || bytes.Contains(logs.Bytes(), []byte("pay-1")) {
		t.Fatalf("log leaks event data: %s", logs.Bytes())
	}
}
