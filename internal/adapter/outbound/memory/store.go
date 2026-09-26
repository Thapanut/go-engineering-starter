// Package memory is an in-process implementation of the outbound ports, used for
// fast tests and for running the service locally without PostgreSQL.
// Transactions are serialized with a mutex and applied copy-on-write, so a failed
// unit of work leaves no trace.
package memory

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

type state struct {
	accounts  map[string]domain.Account
	transfers map[string]domain.Transfer
	byKey     map[string]string // customer + "\x00" + key → transfer id
	audit     []domain.AuditEntry
}

func (s state) clone() state {
	return state{
		accounts:  maps.Clone(s.accounts),
		transfers: maps.Clone(s.transfers),
		byKey:     maps.Clone(s.byKey),
		audit:     slices.Clone(s.audit),
	}
}

// Store implements port.TxManager; each tx gets repositories bound to its working copy.
type Store struct {
	mu sync.Mutex
	st state
}

var _ port.TxManager = (*Store)(nil)

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{st: state{
		accounts:  map[string]domain.Account{},
		transfers: map[string]domain.Transfer{},
		byKey:     map[string]string{},
	}}
}

// WithinTx runs fn against a private copy and commits it only if fn succeeds.
func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context, r port.Repositories) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.st.clone()
	repos := port.Repositories{
		Accounts:  accountRepo{st: &work},
		Transfers: transferRepo{st: &work},
		Audit:     auditRepo{st: &work},
	}
	if err := fn(ctx, repos); err != nil {
		return err
	}
	s.st = work
	return nil
}

// SeedAccount inserts or replaces an account (test/local setup only).
func (s *Store) SeedAccount(a domain.Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.accounts[a.ID] = a
}

// Account returns a committed account snapshot (test helper).
func (s *Store) Account(id string) (domain.Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.st.accounts[id]
	return a, ok
}

// AuditEntries returns committed audit entries (test helper).
func (s *Store) AuditEntries() []domain.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.st.audit)
}

// TransferCount returns the number of committed transfers (test helper).
func (s *Store) TransferCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.st.transfers)
}

type accountRepo struct{ st *state }

func (r accountRepo) GetByID(_ context.Context, id string) (domain.Account, error) {
	a, ok := r.st.accounts[id]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return a, nil
}

func (r accountRepo) GetForUpdate(_ context.Context, ids ...string) (map[string]domain.Account, error) {
	out := make(map[string]domain.Account, len(ids))
	for _, id := range ids {
		if a, ok := r.st.accounts[id]; ok {
			out[id] = a
		}
	}
	return out, nil
}

func (r accountRepo) UpdateBalance(_ context.Context, a domain.Account) error {
	cur, ok := r.st.accounts[a.ID]
	if !ok {
		return domain.ErrAccountNotFound
	}
	cur.Balance, cur.UpdatedAt = a.Balance, a.UpdatedAt
	r.st.accounts[a.ID] = cur
	return nil
}

type transferRepo struct{ st *state }

func (r transferRepo) Create(_ context.Context, t domain.Transfer) error {
	k := t.RequestedBy + "\x00" + t.IdempotencyKey
	if _, dup := r.st.byKey[k]; dup {
		return port.ErrDuplicateIdempotencyKey
	}
	r.st.transfers[t.ID] = t
	r.st.byKey[k] = t.ID
	return nil
}

func (r transferRepo) GetByID(_ context.Context, id string) (domain.Transfer, error) {
	t, ok := r.st.transfers[id]
	if !ok {
		return domain.Transfer{}, domain.ErrTransferNotFound
	}
	return t, nil
}

func (r transferRepo) FindByIdempotencyKey(_ context.Context, customerID, key string) (domain.Transfer, bool, error) {
	id, ok := r.st.byKey[customerID+"\x00"+key]
	if !ok {
		return domain.Transfer{}, false, nil
	}
	return r.st.transfers[id], true, nil
}

type auditRepo struct{ st *state }

func (r auditRepo) Append(_ context.Context, e domain.AuditEntry) error {
	r.st.audit = append(r.st.audit, e)
	return nil
}
