package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
)

// Spec outbox-attempt-tracking.

// partialPublisher fails the ids in fail (with their error) and publishes the rest.
type partialPublisher struct {
	fakePublisher
	fail map[string]error
}

func (p *partialPublisher) Publish(ctx context.Context, msgs []port.OutboxMessage) error {
	var ok []port.OutboxMessage
	failed := map[string]error{}
	for _, m := range msgs {
		if err, bad := p.fail[m.ID]; bad {
			failed[m.ID] = err
		} else {
			ok = append(ok, m)
		}
	}
	if len(ok) > 0 {
		_ = p.fakePublisher.Publish(ctx, ok)
	}
	if len(failed) > 0 {
		return &port.PublishError{Failed: failed}
	}
	return nil
}

var errTooLarge = fmt.Errorf("%w: kafka: message too large", port.ErrPermanentPublish)

// statuses returns evt-id → status for evt-1..evt-n (each has key pay-i).
func statuses(t *testing.T, st *memory.Store, n int) map[string]port.OutboxStatus {
	t.Helper()
	out := map[string]port.OutboxStatus{}
	err := st.WithinTx(context.Background(), func(ctx context.Context, r port.Repositories) error {
		for i := 1; i <= n; i++ {
			recs, err := r.Outbox.ListByKey(ctx, fmt.Sprintf("pay-%d", i))
			if err != nil {
				return err
			}
			for _, rec := range recs {
				out[rec.Message.ID] = rec.Status
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOutboxAC01_WholeBatchFailureRecordsAttemptsAndKeepsPending(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 3)
	boom := errors.New("broker unavailable")
	relay, _ := newRelay(st, &fakePublisher{err: boom}, 100)
	if n, err := relay.RelayOnce(context.Background()); !errors.Is(err, boom) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var got []port.AttemptResult
	// A second failure shows the first attempt was committed (attempts 2, not 1).
	relay2, _ := newRelay(recordingTx{Store: st, got: &got}, &fakePublisher{err: boom}, 100)
	_, _ = relay2.RelayOnce(context.Background())
	if len(got) != 3 || got[0].Attempts != 2 || got[0].Parked || got[0].LastError != "broker unavailable" {
		t.Fatalf("attempts = %+v", got)
	}
	for id, s := range statuses(t, st, 3) {
		if s != port.OutboxPending {
			t.Fatalf("%s status %s, want PENDING", id, s)
		}
	}
}

func TestOutboxAC02_LaterSuccessPublishes(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 2)
	failing, _ := newRelay(st, &fakePublisher{err: errors.New("broker unavailable")}, 100)
	_, _ = failing.RelayOnce(context.Background())
	pub := &fakePublisher{}
	healthy, _ := newRelay(st, pub, 100)
	if n, err := healthy.RelayOnce(context.Background()); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for id, s := range statuses(t, st, 2) {
		if s != port.OutboxPublished {
			t.Fatalf("%s status %s", id, s)
		}
	}
}

func TestOutboxAC03_PartialFailurePublishesTheOthers(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 3)
	pub := &partialPublisher{fail: map[string]error{"evt-2": errors.New("NOT_ENOUGH_REPLICAS")}}
	relay, _ := newRelay(st, pub, 100)
	n, err := relay.RelayOnce(context.Background())
	if n != 2 || err == nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
	s := statuses(t, st, 3)
	if s["evt-1"] != port.OutboxPublished || s["evt-2"] != port.OutboxPending || s["evt-3"] != port.OutboxPublished {
		t.Fatalf("statuses = %v", s)
	}
	if got := fmt.Sprint(pub.ids()); got != "[evt-1 evt-3]" {
		t.Fatalf("published %s", got)
	}
}

func TestOutboxAC04_AC06_PermanentFailureIsParkedAtTheLimitWithoutBlockingOthers(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 3)
	pub := &partialPublisher{fail: map[string]error{"evt-1": errTooLarge}}
	relay, logs := newRelay(st, pub, 100)
	for i := 1; i <= service.MaxPublishAttempts; i++ {
		_, _ = relay.RelayOnce(context.Background())
		if i == 1 && fmt.Sprint(pub.ids()) != "[evt-2 evt-3]" {
			t.Fatalf("others blocked: published %v", pub.ids()) // AC-06
		}
		if parked := statuses(t, st, 1)["evt-1"] == port.OutboxFailed; parked != (i == service.MaxPublishAttempts) {
			t.Fatalf("after attempt %d parked = %v", i, parked)
		}
	}
	if n := bytes.Count(logs.Bytes(), []byte("outbox message parked")); n != 1 {
		t.Fatalf("parked logs = %d: %s", n, logs.Bytes())
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"alert":true,"alert_type":"DLQ_ALERT"`)) {
		t.Fatalf("parked log is not an alert: %s", logs.Bytes())
	}
	if !bytes.Contains(logs.Bytes(), []byte(`"attempts":10`)) || bytes.Contains(logs.Bytes(), []byte("INV-0001")) {
		t.Fatalf("log = %s", logs.Bytes())
	}
	// Never claimed again.
	before := len(pub.ids())
	if n, err := relay.RelayOnce(context.Background()); n != 0 || err != nil || len(pub.ids()) != before {
		t.Fatalf("parked message claimed again: n=%d err=%v", n, err)
	}
}

func TestOutboxAC05_TransientFailureIsNeverParked(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 1)
	relay, logs := newRelay(st, &fakePublisher{err: errors.New("broker unavailable")}, 100)
	for range 2*service.MaxPublishAttempts + 1 {
		_, _ = relay.RelayOnce(context.Background())
	}
	if s := statuses(t, st, 1)["evt-1"]; s != port.OutboxPending {
		t.Fatalf("status = %s, want PENDING", s)
	}
	if n := bytes.Count(logs.Bytes(), []byte(`"alert_type":"DLQ_ALERT"`)); n != 2 {
		t.Fatalf("alert lines = %d", n)
	}
	if n := bytes.Count(logs.Bytes(), []byte("outbox message not publishable")); n != 2 { // at 10 and 20
		t.Fatalf("alerts = %d", n)
	}
}

func TestOutboxAC07_LastErrorIsTruncated(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 1)
	var got []port.AttemptResult
	relay, _ := newRelay(recordingTx{Store: st, got: &got}, &fakePublisher{err: errors.New(strings.Repeat("x", 2000))}, 100)
	_, _ = relay.RelayOnce(context.Background())
	if len(got) != 1 || len(got[0].LastError) != 512 {
		t.Fatalf("last error len = %d", len(got[0].LastError))
	}
}

func TestOutboxAC08_RecordingFailureRollsBackAndRetries(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 1)
	boom := errors.New("db connection lost")
	relay, _ := newRelay(attemptFailTx{TxManager: st, err: boom}, &fakePublisher{err: errors.New("broker unavailable")}, 100)
	if _, err := relay.RelayOnce(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if left := pendingOutbox(t, st); len(left) != 1 {
		t.Fatalf("pending = %d", len(left))
	}
}

func TestOutboxAC10_RequeuedMessageIsPublishedWithTheSameID(t *testing.T) {
	st := memory.NewStore()
	seedOutbox(t, st, 1)
	parking, _ := newRelay(st, &partialPublisher{fail: map[string]error{"evt-1": errTooLarge}}, 100)
	for range service.MaxPublishAttempts {
		_, _ = parking.RelayOnce(context.Background())
	}
	if !st.Requeue("evt-1") {
		t.Fatal("not parked")
	}
	pub := &fakePublisher{}
	relay, _ := newRelay(st, pub, 100)
	if n, err := relay.RelayOnce(context.Background()); err != nil || n != 1 || fmt.Sprint(pub.ids()) != "[evt-1]" {
		t.Fatalf("n=%d err=%v ids=%v", n, err, pub.ids())
	}
}

// recordingTx captures RecordFailedAttempts results.
type recordingTx struct {
	*memory.Store
	got *[]port.AttemptResult
}

func (r recordingTx) WithinTx(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return r.Store.WithinTx(ctx, func(ctx context.Context, repos port.Repositories) error {
		repos.Outbox = recordingOutbox{OutboxRepository: repos.Outbox, got: r.got}
		return fn(ctx, repos)
	})
}

type recordingOutbox struct {
	port.OutboxRepository
	got *[]port.AttemptResult
}

func (r recordingOutbox) RecordFailedAttempts(ctx context.Context, f []port.FailedAttempt, at time.Time, max int) ([]port.AttemptResult, error) {
	res, err := r.OutboxRepository.RecordFailedAttempts(ctx, f, at, max)
	*r.got = append(*r.got, res...)
	return res, err
}

// attemptFailTx makes RecordFailedAttempts fail, as if the DB went away.
type attemptFailTx struct {
	port.TxManager
	err error
}

func (a attemptFailTx) WithinTx(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return a.TxManager.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		r.Outbox = attemptFailOutbox{OutboxRepository: r.Outbox, err: a.err}
		return fn(ctx, r)
	})
}

type attemptFailOutbox struct {
	port.OutboxRepository
	err error
}

func (a attemptFailOutbox) RecordFailedAttempts(context.Context, []port.FailedAttempt, time.Time, int) ([]port.AttemptResult, error) {
	return nil, a.err
}
