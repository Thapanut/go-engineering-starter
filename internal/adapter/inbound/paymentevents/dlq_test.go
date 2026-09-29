package paymentevents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Spec ordering-consumer-dlq.

var fastBackoff = Backoff{Initial: time.Millisecond, Max: 4 * time.Millisecond, StallAfter: time.Hour}

// fakeWriter records writes; the first len(errs) calls fail.
type fakeWriter struct {
	mu   sync.Mutex
	msgs []kafkago.Message
	errs []error
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.errs) > 0 {
		err := w.errs[0]
		w.errs = w.errs[1:]
		return err
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *fakeWriter) Close() error { return nil }

func (w *fakeWriter) written() []kafkago.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]kafkago.Message(nil), w.msgs...)
}

// syncBuffer is a log sink safe to read while the consumer goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func headersOf(m kafkago.Message) map[string]string {
	out := map[string]string{}
	for _, h := range m.Headers {
		if _, dup := out[h.Key]; dup {
			out[h.Key] += "|DUPLICATE"
			continue
		}
		out[h.Key] = string(h.Value)
	}
	return out
}

// runUntil runs c until cond holds (or 2 s), then stops it.
func runUntil(t *testing.T, c *Consumer, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func (f *fakeReader) commits() []kafkago.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kafkago.Message(nil), f.committed...)
}

func sourceMsg(value string) kafkago.Message {
	return kafkago.Message{Topic: Topic, Partition: 2, Offset: 41, Key: []byte("pay-1"), Value: []byte(value),
		Headers: []kafkago.Header{{Key: "content-type", Value: []byte("application/json")}, {Key: "event_id", Value: []byte("evt-1")}}}
}

func newTestConsumer(uc orderingport.PaymentEventHandler, w *fakeWriter, b Backoff, msgs ...kafkago.Message) (*Consumer, *fakeReader, *syncBuffer) {
	logs := &syncBuffer{}
	h := Handler{UseCase: uc, Log: slog.New(slog.NewJSONHandler(logs, nil))}
	r := &fakeReader{msgs: msgs}
	c := newConsumer(r, w, h, b)
	c.now = func() time.Time { return time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC) }
	return c, r, logs
}

func TestDLQAC01_MalformedMessageIsDeadLetteredThenCommitted(t *testing.T) {
	w := &fakeWriter{}
	uc := &recordingUseCase{}
	c, r, logs := newTestConsumer(uc, w, fastBackoff, sourceMsg(`not json`))
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })

	dl := w.written()
	if len(dl) != 1 || len(r.commits()) != 1 || r.commits()[0].Offset != 41 || len(uc.got) != 0 {
		t.Fatalf("dlq=%d commits=%v applied=%d", len(dl), r.commits(), len(uc.got))
	}
	m := dl[0]
	h := headersOf(m)
	want := map[string]string{"content-type": "application/json", "event_id": "evt-1",
		HeaderOriginalTopic: Topic, HeaderOriginalPartition: "2", HeaderOriginalOffset: "41",
		HeaderConsumerGroup: GroupID, HeaderFailedAt: "2026-09-30T10:00:00Z", HeaderReplayCount: "0"}
	for k, v := range want {
		if h[k] != v {
			t.Errorf("header %s = %q, want %q", k, h[k], v)
		}
	}
	if m.Topic != DLQTopic || string(m.Key) != "pay-1" || string(m.Value) != `not json` || h[HeaderReason] == "" {
		t.Fatalf("dlq message = %+v", m)
	}
	if !strings.Contains(logs.String(), "payment event dead-lettered") || !strings.Contains(logs.String(), `"alert":true,"alert_type":"DLQ_ALERT"`) {
		t.Fatalf("logs = %s", logs)
	}
}

func TestDLQAC03_ValidationRejectionIsDeadLettered(t *testing.T) {
	w := &fakeWriter{}
	uc := &recordingUseCase{errs: []error{errors.Join(errors.New("wrapped"), errValidation())}}
	c, r, _ := newTestConsumer(uc, w, fastBackoff, sourceMsg(validEvent))
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	if len(w.written()) != 1 || len(r.commits()) != 1 {
		t.Fatalf("dlq=%d commits=%d", len(w.written()), len(r.commits()))
	}
}

func TestDLQAC04_BusinessOutcomesAreNotDeadLettered(t *testing.T) {
	w := &fakeWriter{}
	c, r, _ := newTestConsumer(outcomeUseCase(orderingport.EventAmountMismatch), w, fastBackoff, sourceMsg(validEvent))
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	if len(w.written()) != 0 || len(r.commits()) != 1 {
		t.Fatalf("dlq=%d commits=%d", len(w.written()), len(r.commits()))
	}
}

func TestDLQAC05_DLQOutageBlocksCommitUntilTheWriteSucceeds(t *testing.T) {
	w := &fakeWriter{errs: []error{errors.New("broker down"), errors.New("broker down")}}
	c, r, logs := newTestConsumer(&recordingUseCase{}, w, fastBackoff, sourceMsg(`not json`))
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	if len(w.written()) != 1 || len(r.commits()) != 1 || strings.Count(logs.String(), "retrying") != 2 {
		t.Fatalf("dlq=%d commits=%d logs=%s", len(w.written()), len(r.commits()), logs)
	}
}

func TestDLQAC06_TransientErrorsRetryWithoutDLQ(t *testing.T) {
	w := &fakeWriter{}
	uc := &recordingUseCase{errs: []error{errors.New("db down"), errors.New("db down"), errors.New("db down")}}
	c, r, logs := newTestConsumer(uc, w, fastBackoff, sourceMsg(validEvent))
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	if len(w.written()) != 0 || len(r.commits()) != 1 || len(uc.got) != 4 {
		t.Fatalf("dlq=%d commits=%d attempts=%d", len(w.written()), len(r.commits()), len(uc.got))
	}
	for _, want := range []string{`"attempt":1`, `"attempt":3`, `"event_id":"evt-1"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("logs lack %s: %s", want, logs)
		}
	}
}

func TestDLQAC06_BackoffDelays(t *testing.T) {
	b := DefaultBackoff
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i, w := range want {
		if got := b.delay(i+1, 0.5); got != w*time.Second {
			t.Errorf("attempt %d: %v, want %v", i+1, got, w*time.Second)
		}
	}
	if lo, hi := b.delay(6, 0), b.delay(6, 1); lo != 24*time.Second || hi != 36*time.Second {
		t.Fatalf("jitter bounds = %v..%v, want 24s..36s", lo, hi)
	}
}

func TestDLQAC07_StallIsLoggedEveryStallPeriod(t *testing.T) {
	uc := &recordingUseCase{errs: []error{errors.New("db down"), errors.New("db down"), errors.New("db down")}}
	c, r, logs := newTestConsumer(uc, &fakeWriter{}, Backoff{Initial: time.Millisecond, Max: time.Millisecond, StallAfter: 5 * time.Minute},
		sourceMsg(validEvent))
	clock := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	c.now = func() time.Time { // every call is 3 minutes later
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(3 * time.Minute)
		return clock
	}
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	// Failures at +3, +6, +9 min after the start: stalls at 6 (≥5) and 9 is < 10, so once.
	if n := strings.Count(logs.String(), `"alert_type":"DLQ_ALERT"`); n != 1 {
		t.Fatalf("alert lines = %d: %s", n, logs)
	}
	if n := strings.Count(logs.String(), "payment event consumer stalled"); n != 1 {
		t.Fatalf("stall logs = %d: %s", n, logs)
	}
}

func TestDLQAC09_ShutdownDuringBackoffStopsQuicklyWithoutCommit(t *testing.T) {
	uc := &recordingUseCase{errs: []error{errors.New("db down")}}
	c, r, _ := newTestConsumer(uc, &fakeWriter{}, Backoff{Initial: time.Hour, Max: time.Hour, StallAfter: time.Hour}, sourceMsg(validEvent))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop within 1 s")
	}
	if time.Since(start) > time.Second || len(r.commits()) != 0 {
		t.Fatalf("commits = %v", r.commits())
	}
}

func TestDLQAC10_ReasonsAndLogsCarryNoPayloadValues(t *testing.T) {
	secretish := strings.Replace(validEvent, `"SUCCESS"`, `"PENDING"`, 1) // invalid status; payload has INV1, tr, 1190.00
	w := &fakeWriter{}
	c, r, logs := newTestConsumer(&recordingUseCase{}, w, fastBackoff, sourceMsg(secretish))
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	reason := headersOf(w.written()[0])[HeaderReason]
	for _, v := range []string{"INV1", "1190.00", "119000", `"tr"`} {
		if strings.Contains(reason, v) || strings.Contains(logs.String(), v) {
			t.Fatalf("payload value %s leaked: reason=%q logs=%s", v, reason, logs)
		}
	}
}

func TestDLQAC13_ReplayedPoisonReturnsWithItsReplayCount(t *testing.T) {
	m := sourceMsg(`not json`)
	m.Topic, m.Partition, m.Offset = RetryTopic, 0, 3
	m.Headers = append(m.Headers, kafkago.Header{Key: HeaderReplayCount, Value: []byte("1")},
		kafkago.Header{Key: HeaderReason, Value: []byte("stale reason from the first dead-letter")})
	w := &fakeWriter{}
	c, r, _ := newTestConsumer(&recordingUseCase{}, w, fastBackoff, m)
	runUntil(t, c, func() bool { return len(r.commits()) == 1 })
	h := headersOf(w.written()[0])
	if h[HeaderReplayCount] != "1" || h[HeaderOriginalTopic] != RetryTopic || strings.Contains(h[HeaderReason], "stale") ||
		strings.Contains(h[HeaderReason], "DUPLICATE") {
		t.Fatalf("headers = %v", h)
	}
}

// ---- replay (cmd/dlqreplay) ----

func dlqMsg(offset int64, eventID string, replays string) kafkago.Message {
	return kafkago.Message{Topic: DLQTopic, Offset: offset, Key: []byte("pay-" + eventID), Value: []byte(`{"raw":"` + eventID + `"}`),
		Headers: []kafkago.Header{
			{Key: "content-type", Value: []byte("application/json")}, {Key: "event_id", Value: []byte(eventID)},
			{Key: HeaderOriginalTopic, Value: []byte(Topic)}, {Key: HeaderOriginalPartition, Value: []byte("1")},
			{Key: HeaderOriginalOffset, Value: []byte("9")}, {Key: HeaderConsumerGroup, Value: []byte(GroupID)},
			{Key: HeaderReason, Value: []byte("malformed payment event")}, {Key: HeaderFailedAt, Value: []byte("2026-09-30T10:00:00Z")},
			{Key: HeaderReplayCount, Value: []byte(replays)},
		}}
}

func auditLines(t *testing.T, out *bytes.Buffer) []replayAudit {
	t.Helper()
	var lines []replayAudit
	dec := json.NewDecoder(out)
	for dec.More() {
		var a replayAudit
		if err := dec.Decode(&a); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, a)
	}
	return lines
}

func TestDLQAC11_ReplayDryRunListsAndChangesNothing(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{dlqMsg(0, "e1", "0"), dlqMsg(1, "e2", "0"), dlqMsg(2, "e3", "0")}}
	w := &fakeWriter{}
	var out bytes.Buffer
	res, err := Replay(context.Background(), r, w, ReplayOptions{DryRun: true, Limit: 100, EndOffsets: allOffsets, Wait: 20 * time.Millisecond, Operator: "admin"}, &out)
	lines := auditLines(t, &out)
	if err != nil || res.Matched != 3 || res.Replayed != 0 || len(w.written()) != 0 || len(r.commits()) != 0 || len(lines) != 3 {
		t.Fatalf("res=%+v err=%v writes=%d commits=%d lines=%d", res, err, len(w.written()), len(r.commits()), len(lines))
	}
	if lines[1].Action != "would-replay" || lines[1].EventID != "e2" || lines[1].OriginalOffset != "9" || lines[1].Reason == "" {
		t.Fatalf("line = %+v", lines[1])
	}
}

func TestDLQAC12_ReplayRepublishesToRetryTopicAndCommits(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{dlqMsg(0, "e1", "0"), dlqMsg(1, "e2", "2")}}
	w := &fakeWriter{}
	var out bytes.Buffer
	res, err := Replay(context.Background(), r, w, ReplayOptions{Limit: 100, EndOffsets: allOffsets, Wait: 20 * time.Millisecond, Operator: "admin"}, &out)
	if err != nil || res.Replayed != 2 || len(r.commits()) != 2 {
		t.Fatalf("res=%+v err=%v commits=%d", res, err, len(r.commits()))
	}
	got := w.written()
	h := headersOf(got[1])
	if got[1].Topic != RetryTopic || string(got[1].Key) != "pay-e2" || string(got[1].Value) != `{"raw":"e2"}` ||
		h[HeaderReplayCount] != "3" || h["event_id"] != "e2" || h["content-type"] != "application/json" ||
		h[HeaderReason] != "" || h[HeaderOriginalTopic] != "" {
		t.Fatalf("retry message = %+v headers %v", got[1], h)
	}
	// AC-15: one audit line per replayed message.
	lines := auditLines(t, &out)
	if len(lines) != 2 || lines[0].Action != "replayed" || lines[0].Operator != "admin" || lines[0].TargetTopic != RetryTopic ||
		lines[0].EventID != "e1" || lines[0].At == "" {
		t.Fatalf("audit = %+v", lines)
	}
}

func TestDLQAC12a_ReplayOneEventCommitsNothing(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{dlqMsg(0, "e1", "0"), dlqMsg(1, "e2", "0"), dlqMsg(2, "e3", "0")}}
	w := &fakeWriter{}
	res, err := Replay(context.Background(), r, w, ReplayOptions{Limit: 100, EventID: "e2", EndOffsets: allOffsets, Wait: 20 * time.Millisecond}, &bytes.Buffer{})
	if err != nil || res.Seen != 3 || res.Replayed != 1 || len(r.commits()) != 0 || headersOf(w.written()[0])["event_id"] != "e2" {
		t.Fatalf("res=%+v err=%v commits=%d", res, err, len(r.commits()))
	}
}

func TestDLQReplayStopsOnWriteFailureWithoutCommitting(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{dlqMsg(0, "e1", "0")}}
	w := &fakeWriter{errs: []error{errors.New("not authorized")}}
	if _, err := Replay(context.Background(), r, w, ReplayOptions{Limit: 100, EndOffsets: allOffsets, Wait: 20 * time.Millisecond}, &bytes.Buffer{}); err == nil || len(r.commits()) != 0 {
		t.Fatalf("err=%v commits=%d", err, len(r.commits()))
	}
}

func errValidation() error { return kernel.Invalid("event_id is required") }

// outcomeUseCase answers every event with out.
type outcomeUseCase orderingport.EventOutcome

func (o outcomeUseCase) HandlePaymentStatusChanged(context.Context, orderingport.PaymentStatusChanged) (orderingport.EventOutcome, error) {
	return orderingport.EventOutcome(o), nil
}

var allOffsets = map[int]int64{0: 1 << 62}

func TestDLQReplayStopsAtTheEndOffsetOfTheRunStart(t *testing.T) {
	// e1 and e2 were in the DLQ at the start (end offset 2); offset 2 was dead-lettered
	// again during the run (e.g. a replayed message that is still poison).
	r := &fakeReader{msgs: []kafkago.Message{dlqMsg(0, "e1", "0"), dlqMsg(1, "e2", "0"), dlqMsg(2, "e1", "1")}}
	w := &fakeWriter{}
	res, err := Replay(context.Background(), r, w, ReplayOptions{Limit: 100, EndOffsets: map[int]int64{0: 2},
		Wait: 20 * time.Millisecond}, &bytes.Buffer{})
	if err != nil || res.Replayed != 2 || len(w.written()) != 2 || len(r.commits()) != 2 {
		t.Fatalf("res=%+v err=%v writes=%d commits=%d", res, err, len(w.written()), len(r.commits()))
	}
}
