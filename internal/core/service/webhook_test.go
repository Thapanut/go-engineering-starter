package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
)

// Synthetic test data only.
const invoice = "INV-0001"

var validBody = []byte("signed-body")

// stubVerifier accepts only validBody and returns the configured notification.
type stubVerifier struct{ n domain.PaymentNotification }

func (s *stubVerifier) Verify(_ context.Context, raw []byte) (domain.PaymentNotification, error) {
	if !bytes.Equal(raw, validBody) {
		return domain.PaymentNotification{}, domain.ErrInvalidSignature
	}
	return s.n, nil
}

// countingTx records how often the service opens a unit of work and how many
// outcome updates succeed. With outboxErr set, adding an event fails.
type countingTx struct {
	port.TxManager
	calls     atomic.Int64
	writes    atomic.Int64
	outboxErr error
}

func (c *countingTx) WithinTx(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	c.calls.Add(1)
	return c.TxManager.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		r.Payments = countingPayments{PaymentRepository: r.Payments, writes: &c.writes}
		if c.outboxErr != nil {
			r.Outbox = failingOutbox{OutboxRepository: r.Outbox, err: c.outboxErr}
		}
		return fn(ctx, r)
	})
}

type failingOutbox struct {
	port.OutboxRepository
	err error
}

func (f failingOutbox) AddPaymentStatusChanged(context.Context, domain.PaymentStatusChanged) error {
	return f.err
}

// seqIDs returns evt-1, evt-2, … so event ids are predictable.
type seqIDs struct{ n atomic.Int64 }

func (s *seqIDs) NewID() string { return fmt.Sprintf("evt-%d", s.n.Add(1)) }

// pendingOutbox returns the unpublished outbox messages without changing them.
func pendingOutbox(t *testing.T, st *memory.Store) []port.OutboxMessage {
	t.Helper()
	var msgs []port.OutboxMessage
	err := st.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		var err error
		msgs, err = r.Outbox.ClaimPending(ctx, 1000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

type countingPayments struct {
	port.PaymentRepository
	writes *atomic.Int64
}

func (c countingPayments) UpdateOutcome(ctx context.Context, p domain.Payment) error {
	if err := c.PaymentRepository.UpdateOutcome(ctx, p); err != nil {
		return err
	}
	c.writes.Add(1)
	return nil
}

// steppingClock advances one second per call so a second write would be visible.
type steppingClock struct{ n atomic.Int64 }

func (c *steppingClock) Now() time.Time {
	return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC).Add(time.Duration(c.n.Add(1)) * time.Second)
}

type fixture struct {
	svc   *service.WebhookService
	store *memory.Store
	tx    *countingTx
	val   *stubVerifier
	logs  *bytes.Buffer
}

func newFixture(t *testing.T, outcome domain.PaymentStatus) *fixture {
	t.Helper()
	st := memory.NewStore()
	amount := domain.Money{Amount: 23087, Currency: domain.THB}
	err := st.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		return r.Payments.Create(ctx, domain.Payment{ID: "pay-1", InvoiceNo: invoice, Amount: amount, Status: domain.PaymentPending})
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		store: st,
		tx:    &countingTx{TxManager: st},
		val: &stubVerifier{n: domain.PaymentNotification{
			InvoiceNo: invoice, ProviderRef: "2868821", Amount: amount, Outcome: outcome, ProviderCode: "0000",
		}},
		logs: &bytes.Buffer{},
	}
	f.svc = service.NewWebhookService(f.val, f.tx, &steppingClock{}, &seqIDs{}, slog.New(slog.NewJSONHandler(f.logs, nil)))
	return f
}

func (f *fixture) payment(t *testing.T) domain.Payment {
	t.Helper()
	var p domain.Payment
	err := f.store.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		var err error
		p, err = r.Payments.GetByInvoiceNoForUpdate(ctx, invoice)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAC01_ValidNotificationMarksPaymentSuccess(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	res, err := f.svc.HandlePaymentNotification(context.Background(), validBody)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != domain.OutcomeProcessed || res.PaymentID != "pay-1" {
		t.Fatalf("result = %+v", res)
	}
	p := f.payment(t)
	if p.Status != domain.PaymentSuccess || p.ProviderRef != "2868821" || p.ProviderCode != "0000" || p.UpdatedAt.IsZero() {
		t.Fatalf("payment = %+v", p)
	}
	if f.tx.writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", f.tx.writes.Load())
	}
}

func TestAC02_FailedNotificationMarksPaymentFailed(t *testing.T) {
	f := newFixture(t, domain.PaymentFailed)
	if res, err := f.svc.HandlePaymentNotification(context.Background(), validBody); err != nil || res.Outcome != domain.OutcomeProcessed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if p := f.payment(t); p.Status != domain.PaymentFailed {
		t.Fatalf("status = %s", p.Status)
	}
}

func TestAC03_DuplicateDeliveryIsAcknowledgedWithoutWriting(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	ctx := context.Background()
	if _, err := f.svc.HandlePaymentNotification(ctx, validBody); err != nil {
		t.Fatal(err)
	}
	first := f.payment(t)
	for range 3 {
		res, err := f.svc.HandlePaymentNotification(ctx, validBody)
		if err != nil || res.Outcome != domain.OutcomeDuplicate || res.PaymentID != "pay-1" {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	}
	if got := f.payment(t); got != first {
		t.Fatalf("payment changed on duplicate:\nbefore %+v\nafter  %+v", first, got)
	}
	if f.tx.writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1 (side effects re-run)", f.tx.writes.Load())
	}
}

func TestAC04_ConflictingOutcomeIsIgnoredAndLogged(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	ctx := context.Background()
	if _, err := f.svc.HandlePaymentNotification(ctx, validBody); err != nil {
		t.Fatal(err)
	}
	first := f.payment(t)
	f.val.n.Outcome = domain.PaymentFailed
	res, err := f.svc.HandlePaymentNotification(ctx, validBody)
	if err != nil || res.Outcome != domain.OutcomeConflictIgnored {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if f.payment(t) != first || f.tx.writes.Load() != 1 {
		t.Fatal("terminal payment was modified")
	}
	if !bytes.Contains(f.logs.Bytes(), []byte("needs reconciliation")) || bytes.Contains(f.logs.Bytes(), []byte(invoice)) {
		t.Fatalf("want reconciliation warning without invoice no, got %s", f.logs.String())
	}
}

func TestAC05_InvalidSignatureIsRejectedBeforeAnyDBAccess(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	_, err := f.svc.HandlePaymentNotification(context.Background(), []byte("forged-body"))
	if !errors.Is(err, domain.ErrInvalidSignature) {
		t.Fatalf("err = %v, want ErrInvalidSignature", err)
	}
	if f.tx.calls.Load() != 0 {
		t.Fatalf("unit of work opened %d times for a forged webhook", f.tx.calls.Load())
	}
	if p := f.payment(t); p.Status != domain.PaymentPending {
		t.Fatalf("status = %s", p.Status)
	}
}

func TestAC07_UnknownInvoiceIsNotFound(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	f.val.n.InvoiceNo = "INV-UNKNOWN"
	if _, err := f.svc.HandlePaymentNotification(context.Background(), validBody); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAC08_AmountMismatchIsRejectedWithoutWriting(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	f.val.n.Amount.Amount = 1
	if _, err := f.svc.HandlePaymentNotification(context.Background(), validBody); !errors.Is(err, domain.ErrPaymentMismatch) {
		t.Fatalf("err = %v, want ErrPaymentMismatch", err)
	}
	if p := f.payment(t); p.Status != domain.PaymentPending || f.tx.writes.Load() != 0 {
		t.Fatalf("payment modified: %+v", p)
	}
}

func TestAC09_IncompleteNotificationIsInvalid(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	f.val.n.ProviderRef = ""
	if _, err := f.svc.HandlePaymentNotification(context.Background(), validBody); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if f.tx.calls.Load() != 0 {
		t.Fatal("unit of work opened for an invalid notification")
	}
}

func TestAC10_ConcurrentDeliveriesTransitionOnce(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	var processed, duplicate atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.svc.HandlePaymentNotification(context.Background(), validBody)
			switch {
			case err != nil:
				t.Errorf("unexpected error: %v", err)
			case res.Outcome == domain.OutcomeProcessed:
				processed.Add(1)
			case res.Outcome == domain.OutcomeDuplicate:
				duplicate.Add(1)
			}
		}()
	}
	wg.Wait()
	if processed.Load() != 1 || duplicate.Load() != 19 || f.tx.writes.Load() != 1 {
		t.Fatalf("processed=%d duplicate=%d writes=%d", processed.Load(), duplicate.Load(), f.tx.writes.Load())
	}
}

// Outbox (spec payment-events-outbox).

func TestOutboxAC01_TransitionAddsEventInSameTransaction(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	if _, err := f.svc.HandlePaymentNotification(context.Background(), validBody); err != nil {
		t.Fatal(err)
	}
	msgs := pendingOutbox(t, f.store)
	if len(msgs) != 1 {
		t.Fatalf("outbox = %d messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.ID != "evt-1" || m.Topic != "payments.v1.status-changed" || m.Key != "pay-1" {
		t.Fatalf("message = %+v", m)
	}
	var ev struct {
		Status      string `json:"status"`
		Amount      string `json:"amount"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
		ProviderRef string `json:"provider_ref"`
		OccurredAt  string `json:"occurred_at"`
	}
	if err := json.Unmarshal(m.Payload, &ev); err != nil {
		t.Fatal(err)
	}
	p := f.payment(t)
	if ev.Status != "SUCCESS" || ev.Amount != "230.87" || ev.AmountMinor != 23087 || ev.Currency != "THB" || ev.ProviderRef != "2868821" ||
		ev.OccurredAt != p.UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("payload = %s (payment %+v)", m.Payload, p)
	}
}

func TestOutboxAC01_FailedOutcomeAlsoAddsEvent(t *testing.T) {
	f := newFixture(t, domain.PaymentFailed)
	if _, err := f.svc.HandlePaymentNotification(context.Background(), validBody); err != nil {
		t.Fatal(err)
	}
	if msgs := pendingOutbox(t, f.store); len(msgs) != 1 || !bytes.Contains(msgs[0].Payload, []byte(`"status":"FAILED"`)) {
		t.Fatalf("outbox = %+v", msgs)
	}
}

func TestOutboxAC02_NoEventWithoutTransition(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	ctx := context.Background()
	if _, err := f.svc.HandlePaymentNotification(ctx, validBody); err != nil {
		t.Fatal(err)
	}
	_, _ = f.svc.HandlePaymentNotification(ctx, validBody) // DUPLICATE
	f.val.n.Outcome = domain.PaymentFailed
	_, _ = f.svc.HandlePaymentNotification(ctx, validBody) // CONFLICT_IGNORED
	f.val.n.Amount.Amount = 1
	_, _ = f.svc.HandlePaymentNotification(ctx, validBody) // PAYMENT_MISMATCH
	_, _ = f.svc.HandlePaymentNotification(ctx, []byte("forged-body"))
	if msgs := pendingOutbox(t, f.store); len(msgs) != 1 {
		t.Fatalf("outbox = %d messages, want only the first transition", len(msgs))
	}
}

func TestOutboxAC03_OutboxFailureRollsBackTheTransition(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	boom := errors.New("outbox unavailable")
	f.tx.outboxErr = boom
	if _, err := f.svc.HandlePaymentNotification(context.Background(), validBody); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if p := f.payment(t); p.Status != domain.PaymentPending {
		t.Fatalf("status = %s, want PENDING (no state change without its event)", p.Status)
	}
	f.tx.outboxErr = nil // 2C2P retries: the retry must process normally
	if res, err := f.svc.HandlePaymentNotification(context.Background(), validBody); err != nil || res.Outcome != domain.OutcomeProcessed {
		t.Fatalf("retry: res=%+v err=%v", res, err)
	}
	if msgs := pendingOutbox(t, f.store); len(msgs) != 1 {
		t.Fatalf("outbox = %d messages, want 1", len(msgs))
	}
}

func TestOutboxAC04_ConcurrentDeliveriesAddOneEvent(t *testing.T) {
	f := newFixture(t, domain.PaymentSuccess)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.svc.HandlePaymentNotification(context.Background(), validBody)
		}()
	}
	wg.Wait()
	if msgs := pendingOutbox(t, f.store); len(msgs) != 1 {
		t.Fatalf("outbox = %d messages, want 1", len(msgs))
	}
}
