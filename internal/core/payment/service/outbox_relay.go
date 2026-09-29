package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// OutboxRelay publishes outbox messages to the broker (ADR-0004).
// See docs/02-specs/payment-events-outbox.md.
//
// Each batch is claimed, published, and marked in one transaction: if publishing
// or marking fails, the transaction rolls back and the batch is retried on the next
// poll. Delivery is therefore at-least-once, never lost.
type OutboxRelay struct {
	tx        port.TxManager
	publisher port.MessagePublisher
	clock     port.Clock
	log       *slog.Logger
	batchSize int
}

// NewOutboxRelay wires the relay to its outbound ports.
func NewOutboxRelay(tx port.TxManager, p port.MessagePublisher, clock port.Clock, log *slog.Logger, batchSize int) *OutboxRelay {
	return &OutboxRelay{tx: tx, publisher: p, clock: clock, log: log, batchSize: batchSize}
}

// RelayOnce publishes up to one batch and returns how many messages it published.
func (r *OutboxRelay) RelayOnce(ctx context.Context) (int, error) {
	published := 0
	err := r.tx.WithinTx(ctx, func(ctx context.Context, repos port.Repositories) error {
		msgs, err := repos.Outbox.ClaimPending(ctx, r.batchSize)
		if err != nil {
			return fmt.Errorf("claim outbox messages: %w", err)
		}
		if len(msgs) == 0 {
			return nil
		}
		if err := r.publisher.Publish(ctx, msgs); err != nil {
			return fmt.Errorf("publish outbox messages: %w", err)
		}
		ids := make([]string, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID
		}
		if err := repos.Outbox.MarkPublished(ctx, ids, r.clock.Now().UTC()); err != nil {
			return fmt.Errorf("mark outbox messages published: %w", err)
		}
		published = len(msgs)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return published, nil
}

// Run relays until ctx is done. It polls every interval, and immediately again
// while batches come back full, so a backlog drains without waiting.
func (r *OutboxRelay) Run(ctx context.Context, interval time.Duration) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		n, err := r.RelayOnce(ctx)
		if err != nil && ctx.Err() == nil {
			// The error names the broker or database failure only, never a payload.
			r.log.WarnContext(ctx, "outbox relay failed; retrying on next poll", slog.String("error", err.Error()))
		}
		next := interval
		if err == nil && n == r.batchSize {
			next = 0
		}
		timer.Reset(next)
	}
}
