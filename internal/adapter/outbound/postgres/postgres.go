// Package postgres implements the outbound ports on PostgreSQL with pgx.
// All SQL is parameterized. Transactions use READ COMMITTED; take explicit row
// locks (SELECT … FOR UPDATE, in a stable order) where a use case needs them.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// TxManager implements port.TxManager on a pgx pool.
type TxManager struct{ pool *pgxpool.Pool }

var _ port.TxManager = (*TxManager)(nil)

// NewTxManager returns a TxManager using pool.
func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// WithinTx commits if fn succeeds and rolls back otherwise.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context, r port.Repositories) error) error {
	return m.inTx(ctx, func(tx pgx.Tx) error { return fn(ctx, newRepositories(tx)) })
}

// newRepositories binds every repository to tx. Add one line per repository port,
// e.g. Accounts: accountRepo{tx: tx}.
func newRepositories(_ pgx.Tx) port.Repositories { return port.Repositories{} }

func (m *TxManager) inTx(ctx context.Context, fn func(tx pgx.Tx) error) (err error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
