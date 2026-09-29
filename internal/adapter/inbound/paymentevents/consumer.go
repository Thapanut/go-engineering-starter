package paymentevents

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// reader is the part of *kafkago.Reader the consumer uses.
type reader interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// writer is the part of *kafkago.Writer the consumer and the replay use.
type writer interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// Dead-letter headers (spec ordering-consumer-dlq). Original headers are kept.
const (
	HeaderOriginalTopic     = "dlq-original-topic"
	HeaderOriginalPartition = "dlq-original-partition"
	HeaderOriginalOffset    = "dlq-original-offset"
	HeaderConsumerGroup     = "dlq-consumer-group"
	HeaderReason            = "dlq-reason"
	HeaderFailedAt          = "dlq-failed-at"
	HeaderReplayCount       = "dlq-replay-count"
	headerDLQPrefix         = "dlq-"
	maxReasonLen            = 256
)

// alertAttrs mark a log line for log collectors (Fluent Bit, Promtail) to route to
// Slack or Opsgenie: structured JSON fields "alert": true, "alert_type": "DLQ_ALERT".
var alertAttrs = []any{slog.Bool("alert", true), slog.String("alert_type", "DLQ_ALERT")}

// Backoff is the retry policy for transient failures: Initial doubling up to Max,
// each delay varied by ±Jitter, and a "stalled" ERROR every StallAfter on one message.
type Backoff struct {
	Initial    time.Duration
	Max        time.Duration
	Jitter     float64
	StallAfter time.Duration
}

// DefaultBackoff is the owner-approved policy: 1 s, 2 s, 4 s … 30 s (±20 %), stall
// alert after 5 min.
var DefaultBackoff = Backoff{Initial: time.Second, Max: 30 * time.Second, Jitter: 0.2, StallAfter: 5 * time.Minute}

// delay returns the wait before retry number attempt (1-based).
func (b Backoff) delay(attempt int, rnd float64) time.Duration {
	d := b.Initial
	for i := 1; i < attempt && d < b.Max; i++ {
		d *= 2
	}
	d = min(d, b.Max)
	return time.Duration(float64(d) * (1 + b.Jitter*(2*rnd-1)))
}

// Consumer reads payment events from Kafka in consumer group "ordering", from the
// source topic and the retry topic. Delivery is at-least-once: the offset is
// committed only after Handle succeeds or the message is safely in the DLQ;
// ordering deduplicates on event_id.
type Consumer struct {
	r       reader
	dlq     writer
	h       Handler
	log     *slog.Logger
	backoff Backoff
	now     func() time.Time
	rnd     func() float64
}

// NewConsumer returns a consumer for the given bootstrap brokers.
func NewConsumer(brokers []string, h Handler) *Consumer {
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		GroupID:     GroupID,
		GroupTopics: []string{Topic, RetryTopic},
		MinBytes:    1,
		MaxBytes:    1 << 20,
		MaxWait:     500 * time.Millisecond,
		StartOffset: kafkago.FirstOffset, // a new group starts from the oldest event
	})
	return newConsumer(r, NewWriter(brokers), h, DefaultBackoff)
}

// NewWriter returns a synchronous writer with the same partitioning as the relay
// (murmur2 on the key) that returns only after all in-sync replicas acknowledged.
func NewWriter(brokers []string) *kafkago.Writer {
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Balancer:               kafkago.Murmur2Balancer{},
		RequiredAcks:           kafkago.RequireAll,
		MaxAttempts:            3,
		BatchTimeout:           10 * time.Millisecond,
		WriteTimeout:           10 * time.Second,
		AllowAutoTopicCreation: false, // topics are provisioned with their ACLs and retention
	}
}

func newConsumer(r reader, dlq writer, h Handler, b Backoff) *Consumer {
	return &Consumer{r: r, dlq: dlq, h: h, log: h.Log, backoff: b, now: time.Now, rnd: rand.Float64}
}

// Run consumes until ctx is cancelled. A poison message is dead-lettered; any
// other failure is retried with backoff and never skipped, so partition order is
// kept.
func (c *Consumer) Run(ctx context.Context) {
	for {
		m, err := c.r.FetchMessage(ctx)
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			c.log.WarnContext(ctx, "fetch payment event failed", slog.String("error", err.Error()))
			if !sleep(ctx, c.backoff.Initial) {
				return
			}
			continue
		}
		if !c.process(ctx, m) {
			return // shutting down; the message is redelivered after restart
		}
		if err := c.r.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
			// Not fatal: the message is redelivered and deduplicated on event_id.
			c.log.WarnContext(ctx, "commit payment event offset failed", slog.String("error", err.Error()))
		}
	}
}

// process handles m until it is applied or dead-lettered (true), or ctx ends (false).
func (c *Consumer) process(ctx context.Context, m kafkago.Message) bool {
	start := c.now()
	nextStall := c.backoff.StallAfter
	for attempt := 1; ; attempt++ {
		err := c.h.Handle(ctx, m.Value)
		if errors.Is(err, ErrPoison) {
			reason := PoisonReason(err)
			if err = c.deadLetter(ctx, m, reason); err == nil {
				c.log.ErrorContext(ctx, "payment event dead-lettered", c.attrs(m, append([]any{slog.String("dlq_topic", DLQTopic),
					slog.String("reason", reason)}, alertAttrs...)...)...)
				return true
			}
			err = errors.Join(errors.New("write to DLQ"), err) // a DLQ outage is transient: retry, no commit
		}
		if err == nil {
			return true
		}
		c.log.WarnContext(ctx, "handle payment event failed; retrying", c.attrs(m, slog.Int("attempt", attempt),
			slog.String("error", err.Error()))...)
		if stalled := c.now().Sub(start); stalled >= nextStall {
			c.log.ErrorContext(ctx, "payment event consumer stalled", c.attrs(m, append([]any{slog.Duration("stalled_for", stalled),
				slog.Int("attempt", attempt)}, alertAttrs...)...)...)
			nextStall += c.backoff.StallAfter
		}
		if !sleep(ctx, c.backoff.delay(attempt, c.rnd())) {
			return false
		}
	}
}

// deadLetter writes m unchanged to the DLQ with diagnostic headers.
func (c *Consumer) deadLetter(ctx context.Context, m kafkago.Message, reason string) error {
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	replays := header(m, HeaderReplayCount)
	if replays == "" {
		replays = "0"
	}
	headers := withoutDLQHeaders(m.Headers)
	headers = append(headers,
		kafkago.Header{Key: HeaderOriginalTopic, Value: []byte(m.Topic)},
		kafkago.Header{Key: HeaderOriginalPartition, Value: []byte(strconv.Itoa(m.Partition))},
		kafkago.Header{Key: HeaderOriginalOffset, Value: []byte(strconv.FormatInt(m.Offset, 10))},
		kafkago.Header{Key: HeaderConsumerGroup, Value: []byte(GroupID)},
		kafkago.Header{Key: HeaderReason, Value: []byte(reason)},
		kafkago.Header{Key: HeaderFailedAt, Value: []byte(c.now().UTC().Format(time.RFC3339Nano))},
		kafkago.Header{Key: HeaderReplayCount, Value: []byte(replays)},
	)
	return c.dlq.WriteMessages(ctx, kafkago.Message{Topic: DLQTopic, Key: m.Key, Value: m.Value, Headers: headers})
}

// attrs identifies m in logs by ids and position only, never payload values.
func (c *Consumer) attrs(m kafkago.Message, extra ...any) []any {
	return append([]any{slog.String("event_id", header(m, "event_id")), slog.String("topic", m.Topic),
		slog.Int("partition", m.Partition), slog.Int64("offset", m.Offset)}, extra...)
}

// Close releases the Kafka connections.
func (c *Consumer) Close() error { return errors.Join(c.r.Close(), c.dlq.Close()) }

func header(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func withoutDLQHeaders(hs []kafkago.Header) []kafkago.Header {
	out := make([]kafkago.Header, 0, len(hs)+7)
	for _, h := range hs {
		if !strings.HasPrefix(h.Key, headerDLQPrefix) {
			out = append(out, h)
		}
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
