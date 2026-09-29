package paymentevents

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// reader is the part of *kafkago.Reader the consumer uses.
type reader interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
	Close() error
}

// Consumer reads payment events from Kafka in consumer group "ordering".
// Delivery is at-least-once: the offset is committed only after Handle succeeds
// (AC-12); ordering deduplicates on event_id.
type Consumer struct {
	r       reader
	h       Handler
	log     *slog.Logger
	backoff time.Duration
}

// NewConsumer returns a consumer for the given bootstrap brokers.
func NewConsumer(brokers []string, h Handler) *Consumer {
	return newConsumer(kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     brokers,
		GroupID:     GroupID,
		Topic:       Topic,
		MinBytes:    1,
		MaxBytes:    1 << 20,
		MaxWait:     500 * time.Millisecond,
		StartOffset: kafkago.FirstOffset, // a new group starts from the oldest event
	}), h, time.Second)
}

func newConsumer(r reader, h Handler, backoff time.Duration) *Consumer {
	return &Consumer{r: r, h: h, log: h.Log, backoff: backoff}
}

// Run consumes until ctx is cancelled. A message whose handling fails is retried
// with a backoff and never skipped, so partition order is kept.
func (c *Consumer) Run(ctx context.Context) {
	for {
		m, err := c.r.FetchMessage(ctx)
		if ctx.Err() != nil || errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			c.log.WarnContext(ctx, "fetch payment event failed", slog.String("error", err.Error()))
			if !sleep(ctx, c.backoff) {
				return
			}
			continue
		}
		for {
			err := c.h.Handle(ctx, m.Value)
			if err == nil {
				break
			}
			c.log.WarnContext(ctx, "handle payment event failed; retrying", slog.String("error", err.Error()))
			if !sleep(ctx, c.backoff) {
				return
			}
		}
		if err := c.r.CommitMessages(ctx, m); err != nil && ctx.Err() == nil {
			// Not fatal: the message is redelivered and deduplicated on event_id.
			c.log.WarnContext(ctx, "commit payment event offset failed", slog.String("error", err.Error()))
		}
	}
}

// Close releases the Kafka connection.
func (c *Consumer) Close() error { return c.r.Close() }

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
