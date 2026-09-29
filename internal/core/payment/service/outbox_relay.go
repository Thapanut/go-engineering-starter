package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// MaxPublishAttempts is how many attempts a permanently failing outbox message gets
// before it is parked as FAILED, and the interval of "not publishable" alerts for
// transient failures (ADR-0004 amendment 1, spec outbox-attempt-tracking).
const MaxPublishAttempts = 10

// maxLastErrorLen bounds the stored error text.
const maxLastErrorLen = 512

// OutboxRelay publishes outbox messages to the broker (ADR-0004).
// See docs/02-specs/payment-events-outbox.md and outbox-attempt-tracking.md.
//
// Each batch is claimed, published, and settled in one transaction: published
// messages are marked, the others get a failed attempt (and a permanently failing
// one is parked after MaxPublishAttempts). If settling fails, the transaction rolls
// back and the batch is retried on the next poll. Delivery is at-least-once; only a
// parked message waits for an Admin.
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
	var (
		published int
		pubErr    error
		results   []port.AttemptResult
	)
	err := r.tx.WithinTx(ctx, func(ctx context.Context, repos port.Repositories) error {
		msgs, err := repos.Outbox.ClaimPending(ctx, r.batchSize)
		if err != nil {
			return fmt.Errorf("claim outbox messages: %w", err)
		}
		if len(msgs) == 0 {
			return nil
		}
		pubErr = r.publisher.Publish(ctx, msgs)
		ok, failed := split(msgs, pubErr)
		now := r.clock.Now().UTC()
		if len(ok) > 0 {
			if err := repos.Outbox.MarkPublished(ctx, ok, now); err != nil {
				return fmt.Errorf("mark outbox messages published: %w", err)
			}
		}
		if len(failed) > 0 {
			rs, err := repos.Outbox.RecordFailedAttempts(ctx, failed, now, MaxPublishAttempts)
			if err != nil {
				return fmt.Errorf("record failed publish attempts: %w", err)
			}
			results = rs
		}
		published = len(ok)
		return nil
	})
	if err != nil {
		return 0, err
	}
	r.alert(ctx, results)
	if pubErr != nil {
		return published, fmt.Errorf("publish outbox messages: %w", pubErr)
	}
	return published, nil
}

// split sorts a batch into published ids and failed attempts from Publish's error.
func split(msgs []port.OutboxMessage, pubErr error) (ok []string, failed []port.FailedAttempt) {
	var partial *port.PublishError
	isPartial := errors.As(pubErr, &partial)
	for _, m := range msgs {
		err := pubErr
		if isPartial {
			err = partial.Failed[m.ID]
		}
		if err == nil {
			ok = append(ok, m.ID)
			continue
		}
		reason := err.Error()
		if len(reason) > maxLastErrorLen {
			reason = reason[:maxLastErrorLen]
		}
		failed = append(failed, port.FailedAttempt{ID: m.ID, Reason: reason, Permanent: errors.Is(err, port.ErrPermanentPublish)})
	}
	return ok, failed
}

// alert logs parked messages, and every MaxPublishAttempts attempts of one that
// keeps failing. Logs carry ids and error text only, never payload.
func (r *OutboxRelay) alert(ctx context.Context, results []port.AttemptResult) {
	for _, a := range results {
		attrs := []any{slog.String("event_id", a.ID), slog.String("topic", a.Topic), slog.Int("attempts", a.Attempts),
			slog.String("last_error", a.LastError)}
		switch {
		case a.Parked:
			r.log.ErrorContext(ctx, "outbox message parked; re-queue after fixing the cause", attrs...)
		case a.Attempts%MaxPublishAttempts == 0:
			r.log.ErrorContext(ctx, "outbox message not publishable; still retrying", attrs...)
		}
	}
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
