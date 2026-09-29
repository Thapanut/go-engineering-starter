// Package memory is an in-process implementation of the outbound ports, used for
// fast unit tests and for running the service locally without PostgreSQL.
//
// Transactions are serialized with a mutex and applied copy-on-write: WithinTx
// clones state, runs fn against the clone, and swaps it in only on success, so a
// failed unit of work leaves no trace. Add a map per aggregate to state (and to
// clone), and a repository type bound to *state per repository port.
package memory

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

type state struct {
	payments  map[string]domain.Payment // id → payment
	byInvoice map[string]string         // invoice no → id
	outbox    []outboxEntry             // insertion order = oldest first
}

func (s state) clone() state {
	return state{payments: maps.Clone(s.payments), byInvoice: maps.Clone(s.byInvoice), outbox: slices.Clone(s.outbox)}
}

// Store implements port.TxManager; each tx gets repositories bound to its working copy.
type Store struct {
	mu sync.Mutex
	st state
}

var _ port.TxManager = (*Store)(nil)

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{st: state{payments: map[string]domain.Payment{}, byInvoice: map[string]string{}}}
}

// WithinTx runs fn against a private copy and commits it only if fn succeeds.
func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context, r port.Repositories) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.st.clone()
	if err := fn(ctx, port.Repositories{Payments: paymentRepo{st: &work}, Outbox: outboxRepo{st: &work}}); err != nil {
		return err
	}
	s.st = work
	return nil
}

type paymentRepo struct{ st *state }

func (r paymentRepo) Create(_ context.Context, p domain.Payment) error {
	if _, dup := r.st.byInvoice[p.InvoiceNo]; dup {
		return domain.ErrConflict
	}
	r.st.payments[p.ID] = p
	r.st.byInvoice[p.InvoiceNo] = p.ID
	return nil
}

// GetByInvoiceNoForUpdate needs no lock: Store serializes transactions.
func (r paymentRepo) GetByInvoiceNoForUpdate(ctx context.Context, invoiceNo string) (domain.Payment, error) {
	return r.GetByInvoiceNo(ctx, invoiceNo)
}

func (r paymentRepo) GetByInvoiceNo(_ context.Context, invoiceNo string) (domain.Payment, error) {
	id, ok := r.st.byInvoice[invoiceNo]
	if !ok {
		return domain.Payment{}, domain.ErrNotFound
	}
	return r.st.payments[id], nil
}

func (r paymentRepo) UpdateOutcome(_ context.Context, p domain.Payment) error {
	cur, ok := r.st.payments[p.ID]
	if !ok || cur.Status != domain.PaymentPending {
		return domain.ErrConflict
	}
	cur.Status, cur.ProviderRef, cur.ProviderCode, cur.UpdatedAt = p.Status, p.ProviderRef, p.ProviderCode, p.UpdatedAt
	r.st.payments[p.ID] = cur
	return nil
}
