// Package kafka implements port.MessagePublisher with segmentio/kafka-go (ADR-0004).
//
// Delivery is at-least-once: the outbox relay marks messages published only after
// every broker ack, and a crash between the ack and the commit publishes them again.
// Consumers must deduplicate on event_id.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// Publisher writes outbox messages to Kafka.
type Publisher struct{ w *kafkago.Writer }

var _ port.MessagePublisher = (*Publisher)(nil)

// NewPublisher returns a publisher for the given bootstrap brokers ("host:port").
func NewPublisher(brokers []string) *Publisher {
	return &Publisher{w: &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Balancer:               kafkago.Murmur2Balancer{}, // same key → partition mapping as Java clients
		RequiredAcks:           kafkago.RequireAll,        // durable before the outbox row is marked
		MaxAttempts:            3,                         // the relay retries on its next poll
		BatchTimeout:           10 * time.Millisecond,     // WriteMessages is synchronous; do not wait to fill batches
		WriteTimeout:           10 * time.Second,
		AllowAutoTopicCreation: false, // topics are provisioned with their ACLs and retention
	}}
}

// Publish writes msgs and returns once all are acknowledged by the in-sync
// replicas. When only some fail it returns a *port.PublishError; failures retrying
// cannot fix are wrapped with port.ErrPermanentPublish. A message over the client's
// size limit does not stop the others: they are written without it.
func (p *Publisher) Publish(ctx context.Context, msgs []port.OutboxMessage) error {
	failed := map[string]error{}
	pending := toKafka(msgs)
	for len(pending) > 0 {
		err := p.w.WriteMessages(ctx, pending...)
		var tooLarge kafkago.MessageTooLargeError
		var perMessage kafkago.WriteErrors
		switch {
		case err == nil:
			pending = nil
		case errors.As(err, &tooLarge):
			failed[eventID(tooLarge.Message)] = fmt.Errorf("%w: %w", port.ErrPermanentPublish, err)
			pending = tooLarge.Remaining
		case errors.As(err, &perMessage) && len(perMessage) == len(pending):
			for i, e := range perMessage {
				if e != nil {
					failed[eventID(pending[i])] = classify(e)
				}
			}
			pending = nil
		default: // nothing in this write can be assumed published
			if len(failed) == 0 {
				return fmt.Errorf("kafka write %d messages: %w", len(pending), err)
			}
			for _, m := range pending {
				failed[eventID(m)] = err
			}
			pending = nil
		}
	}
	if len(failed) > 0 {
		return &port.PublishError{Failed: failed}
	}
	return nil
}

// permanent lists broker error codes that retrying the same message cannot fix.
// Everything else, including authorization errors (fixable by ops, and affecting
// every message), is transient: the message is retried, never parked.
var permanent = map[kafkago.Error]bool{
	kafkago.MessageSizeTooLarge: true,
	kafkago.RecordListTooLarge:  true,
	kafkago.InvalidTopic:        true,
	kafkago.InvalidRecord:       true,
}

func classify(err error) error {
	var code kafkago.Error
	if errors.As(err, &code) && permanent[code] {
		return fmt.Errorf("%w: %w", port.ErrPermanentPublish, err)
	}
	return err
}

func eventID(m kafkago.Message) string {
	for _, h := range m.Headers {
		if h.Key == "event_id" {
			return string(h.Value)
		}
	}
	return ""
}

// Close flushes and closes the writer.
func (p *Publisher) Close() error { return p.w.Close() }

func toKafka(msgs []port.OutboxMessage) []kafkago.Message {
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		out[i] = kafkago.Message{
			Topic: m.Topic,
			Key:   []byte(m.Key),
			Value: m.Payload,
			Headers: []kafkago.Header{
				{Key: "content-type", Value: []byte("application/json")},
				{Key: "event_id", Value: []byte(m.ID)},
			},
		}
	}
	return out
}
