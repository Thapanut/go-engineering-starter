// Package kafka implements port.MessagePublisher with segmentio/kafka-go (ADR-0004).
//
// Delivery is at-least-once: the outbox relay marks messages published only after
// every broker ack, and a crash between the ack and the commit publishes them again.
// Consumers must deduplicate on event_id.
package kafka

import (
	"context"
	"fmt"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
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

// Publish writes msgs and returns once all are acknowledged by the in-sync replicas.
func (p *Publisher) Publish(ctx context.Context, msgs []port.OutboxMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	if err := p.w.WriteMessages(ctx, toKafka(msgs)...); err != nil {
		return fmt.Errorf("kafka write %d messages: %w", len(msgs), err)
	}
	return nil
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
