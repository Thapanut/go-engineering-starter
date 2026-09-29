package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/events"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

type outboxEntry struct {
	msg           port.OutboxMessage
	status        port.OutboxStatus
	publishedAt   time.Time
	attempts      int
	lastError     string
	lastAttemptAt time.Time
}

// outboxRepo implements port.OutboxRepository on the transaction's working copy.
// Transactions are serialized by Store, so a claim needs no row lock.
type outboxRepo struct{ st *state }

func (r outboxRepo) AddPaymentStatusChanged(_ context.Context, e domain.PaymentStatusChanged) error {
	msg, err := events.PaymentStatusChanged(e)
	if err != nil {
		return err
	}
	r.st.outbox = append(r.st.outbox, outboxEntry{msg: msg, status: port.OutboxPending})
	return nil
}

func (r outboxRepo) ClaimPending(_ context.Context, limit int) ([]port.OutboxMessage, error) {
	var msgs []port.OutboxMessage
	for _, e := range r.st.outbox {
		if len(msgs) == limit {
			break
		}
		if e.status == port.OutboxPending {
			msgs = append(msgs, e.msg)
		}
	}
	return msgs, nil
}

func (r outboxRepo) MarkPublished(_ context.Context, ids []string, at time.Time) error {
	marked := 0
	for i := range r.st.outbox {
		if r.st.outbox[i].status == port.OutboxPending && slices.Contains(ids, r.st.outbox[i].msg.ID) {
			r.st.outbox[i].status, r.st.outbox[i].publishedAt = port.OutboxPublished, at
			marked++
		}
	}
	if marked != len(ids) {
		return fmt.Errorf("marked %d of %d outbox messages: %w", marked, len(ids), domain.ErrConflict)
	}
	return nil
}

func (r outboxRepo) RecordFailedAttempts(_ context.Context, failed []port.FailedAttempt, at time.Time, maxAttempts int) ([]port.AttemptResult, error) {
	out := make([]port.AttemptResult, 0, len(failed))
	for _, f := range failed {
		i := slices.IndexFunc(r.st.outbox, func(e outboxEntry) bool { return e.msg.ID == f.ID && e.status == port.OutboxPending })
		if i < 0 {
			return nil, fmt.Errorf("record attempt for outbox message %s: %w", f.ID, domain.ErrConflict)
		}
		e := &r.st.outbox[i]
		e.attempts, e.lastError, e.lastAttemptAt = e.attempts+1, f.Reason, at
		if f.Permanent && e.attempts >= maxAttempts {
			e.status = port.OutboxFailed
		}
		out = append(out, port.AttemptResult{ID: f.ID, Topic: e.msg.Topic, Attempts: e.attempts,
			Parked: e.status == port.OutboxFailed, LastError: e.lastError})
	}
	return out, nil
}

// Requeue puts a parked message back to PENDING with zero attempts, as an Admin
// does with SQL on PostgreSQL. For STORE=memory tests.
func (s *Store) Requeue(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.st.outbox {
		if e := &s.st.outbox[i]; e.msg.ID == id && e.status == port.OutboxFailed {
			e.status, e.attempts = port.OutboxPending, 0
			return true
		}
	}
	return false
}

func (r outboxRepo) ListByKey(_ context.Context, key string) ([]port.OutboxRecord, error) {
	var recs []port.OutboxRecord
	for _, e := range r.st.outbox {
		if e.msg.Key == key {
			recs = append(recs, port.OutboxRecord{Message: e.msg, Status: e.status, PublishedAt: e.publishedAt})
		}
	}
	return recs, nil
}

// Publisher is an in-process port.MessagePublisher for STORE=memory and tests: it
// keeps published messages instead of sending them to a broker.
//
// With Deliver set, it also hands every message to an in-process consumer (the
// ordering module with STORE=memory), standing in for Kafka. A Deliver error fails
// the publish, so the relay retries it like a broker error.
type Publisher struct {
	Deliver func(ctx context.Context, m port.OutboxMessage) error

	mu   sync.Mutex
	msgs []port.OutboxMessage
}

var _ port.MessagePublisher = (*Publisher)(nil)

// Publish records msgs.
func (p *Publisher) Publish(ctx context.Context, msgs []port.OutboxMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Deliver != nil {
		for _, m := range msgs {
			if err := p.Deliver(ctx, m); err != nil {
				return fmt.Errorf("deliver in-process: %w", err)
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, msgs...)
	return nil
}

// Messages returns everything published so far, oldest first.
func (p *Publisher) Messages() []port.OutboxMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.msgs)
}
