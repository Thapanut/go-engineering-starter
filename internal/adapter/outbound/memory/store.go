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
	"sync"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

type state struct{}

func (s state) clone() state { return state{} }

// Store implements port.TxManager; each tx gets repositories bound to its working copy.
type Store struct {
	mu sync.Mutex
	st state
}

var _ port.TxManager = (*Store)(nil)

// NewStore returns an empty store.
func NewStore() *Store { return &Store{} }

// WithinTx runs fn against a private copy and commits it only if fn succeeds.
func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context, r port.Repositories) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	work := s.st.clone()
	if err := fn(ctx, port.Repositories{}); err != nil {
		return err
	}
	s.st = work
	return nil
}
