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
	msg         port.OutboxMessage
	published   bool
	publishedAt time.Time
}

// outboxRepo implements port.OutboxRepository on the transaction's working copy.
// Transactions are serialized by Store, so a claim needs no row lock.
type outboxRepo struct{ st *state }

func (r outboxRepo) AddPaymentStatusChanged(_ context.Context, e domain.PaymentStatusChanged) error {
	msg, err := events.PaymentStatusChanged(e)
	if err != nil {
		return err
	}
	r.st.outbox = append(r.st.outbox, outboxEntry{msg: msg})
	return nil
}

func (r outboxRepo) ClaimPending(_ context.Context, limit int) ([]port.OutboxMessage, error) {
	var msgs []port.OutboxMessage
	for _, e := range r.st.outbox {
		if len(msgs) == limit {
			break
		}
		if !e.published {
			msgs = append(msgs, e.msg)
		}
	}
	return msgs, nil
}

func (r outboxRepo) MarkPublished(_ context.Context, ids []string, at time.Time) error {
	marked := 0
	for i := range r.st.outbox {
		if !r.st.outbox[i].published && slices.Contains(ids, r.st.outbox[i].msg.ID) {
			r.st.outbox[i].published, r.st.outbox[i].publishedAt = true, at
			marked++
		}
	}
	if marked != len(ids) {
		return fmt.Errorf("marked %d of %d outbox messages: %w", marked, len(ids), domain.ErrConflict)
	}
	return nil
}

func (r outboxRepo) ListByKey(_ context.Context, key string) ([]port.OutboxRecord, error) {
	var recs []port.OutboxRecord
	for _, e := range r.st.outbox {
		if e.msg.Key == key {
			recs = append(recs, port.OutboxRecord{Message: e.msg, PublishedAt: e.publishedAt})
		}
	}
	return recs, nil
}

// Publisher is an in-process port.MessagePublisher for STORE=memory and tests: it
// keeps published messages instead of sending them to a broker.
type Publisher struct {
	mu   sync.Mutex
	msgs []port.OutboxMessage
}

var _ port.MessagePublisher = (*Publisher)(nil)

// Publish records msgs.
func (p *Publisher) Publish(ctx context.Context, msgs []port.OutboxMessage) error {
	if err := ctx.Err(); err != nil {
		return err
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
