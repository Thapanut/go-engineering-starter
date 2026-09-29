package port

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
)

// OutboxMessage is an encoded event waiting in the outbox to be published.
type OutboxMessage struct {
	ID        string // the event id; consumers deduplicate on it
	Topic     string
	Key       string // partition key, so events for one aggregate stay in order
	Payload   []byte
	CreatedAt time.Time
}

// OutboxStatus is where an outbox message is in its life (ADR-0004 amendment 1).
type OutboxStatus string

// Outbox statuses. FAILED messages are parked: the relay no longer claims them
// until an Admin re-queues them.
const (
	OutboxPending   OutboxStatus = "PENDING"
	OutboxPublished OutboxStatus = "PUBLISHED"
	OutboxFailed    OutboxStatus = "FAILED"
)

// OutboxRecord is an outbox message, its status, and when the relay published it
// (zero: not yet).
type OutboxRecord struct {
	Message     OutboxMessage
	Status      OutboxStatus
	PublishedAt time.Time
}

// FailedAttempt is one message the broker did not accept in a publish attempt.
type FailedAttempt struct {
	ID        string
	Reason    string // error text, never payload
	Permanent bool   // retrying cannot fix it (see ErrPermanentPublish)
}

// AttemptResult is a message's state after a failed attempt was recorded.
type AttemptResult struct {
	ID        string
	Topic     string
	Attempts  int
	Parked    bool // now FAILED
	LastError string
}

// OutboxRepository stores events in the same unit of work as the state change that
// raised them (transactional outbox, ADR-0004) and hands them to the relay.
type OutboxRepository interface {
	// AddPaymentStatusChanged encodes e as described in contracts/asyncapi.yaml and
	// stores it unpublished.
	AddPaymentStatusChanged(ctx context.Context, e domain.PaymentStatusChanged) error
	// ClaimPending locks up to limit PENDING messages, oldest first, until the
	// transaction ends. Messages already locked by another relay are skipped.
	ClaimPending(ctx context.Context, limit int) ([]OutboxMessage, error)
	// MarkPublished records that the claimed messages were published.
	MarkPublished(ctx context.Context, ids []string, at time.Time) error
	// RecordFailedAttempts adds one attempt to each claimed message with its
	// reason, and parks (FAILED) a message whose failure is permanent once it has
	// maxAttempts attempts. It returns each message's new state.
	RecordFailedAttempts(ctx context.Context, failed []FailedAttempt, at time.Time, maxAttempts int) ([]AttemptResult, error)
	// ListByKey returns every message with the given key (aggregate id), oldest
	// first, published or not. For diagnostics; it is not indexed.
	ListByKey(ctx context.Context, key string) ([]OutboxRecord, error)
}

// MessagePublisher delivers outbox messages to the message broker (outbound port).
// It returns nil only after the broker has acknowledged every message. When only
// some messages failed it returns a *PublishError; any other error means none of
// the messages can be assumed published.
type MessagePublisher interface {
	Publish(ctx context.Context, msgs []OutboxMessage) error
}

// ErrPermanentPublish marks a publish failure that retrying cannot fix, such as a
// message larger than the broker accepts. Adapters wrap such errors with it.
var ErrPermanentPublish = errors.New("permanent publish failure")

// PublishError reports the messages of a batch the broker did not accept, by
// message id. Every other message of the batch was published.
type PublishError struct {
	Failed map[string]error
}

func (e *PublishError) Error() string {
	return fmt.Sprintf("%d messages not published", len(e.Failed))
}
