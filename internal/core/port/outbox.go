package port

import (
	"context"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// OutboxMessage is an encoded event waiting in the outbox to be published.
type OutboxMessage struct {
	ID        string // the event id; consumers deduplicate on it
	Topic     string
	Key       string // partition key, so events for one aggregate stay in order
	Payload   []byte
	CreatedAt time.Time
}

// OutboxRepository stores events in the same unit of work as the state change that
// raised them (transactional outbox, ADR-0004) and hands them to the relay.
type OutboxRepository interface {
	// AddPaymentStatusChanged encodes e as described in contracts/asyncapi.yaml and
	// stores it unpublished.
	AddPaymentStatusChanged(ctx context.Context, e domain.PaymentStatusChanged) error
	// ClaimPending locks up to limit unpublished messages, oldest first, until the
	// transaction ends. Messages already locked by another relay are skipped.
	ClaimPending(ctx context.Context, limit int) ([]OutboxMessage, error)
	// MarkPublished records that the claimed messages were published.
	MarkPublished(ctx context.Context, ids []string, at time.Time) error
}

// MessagePublisher delivers outbox messages to the message broker (outbound port).
// It returns nil only after the broker has acknowledged every message.
type MessagePublisher interface {
	Publish(ctx context.Context, msgs []OutboxMessage) error
}
