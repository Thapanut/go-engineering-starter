// Package postgres implements the outbound ports on PostgreSQL with pgx.
// All SQL is parameterized. Transactions use READ COMMITTED plus explicit row locks.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

const (
	pgUniqueViolation     = "23505"
	uqTransferIdempotency = "uq_transfers_idempotency"
)

// TxManager implements port.TxManager on a pgx pool.
type TxManager struct{ pool *pgxpool.Pool }

var _ port.TxManager = (*TxManager)(nil)

// NewTxManager returns a TxManager using pool.
func NewTxManager(pool *pgxpool.Pool) *TxManager { return &TxManager{pool: pool} }

// WithinTx commits if fn succeeds and rolls back otherwise.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context, r port.Repositories) error) (err error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	q := queries{tx: tx}
	if err = fn(ctx, port.Repositories{Accounts: accountRepo{q}, Transfers: transferRepo{q}, Audit: auditRepo{q}}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

type queries struct{ tx pgx.Tx }

// ---- accounts ----

type accountRepo struct{ queries }

const accountCols = `id::text, customer_id, currency, balance, status, updated_at`

func scanAccount(row pgx.Row) (domain.Account, error) {
	var a domain.Account
	var currency, status string
	if err := row.Scan(&a.ID, &a.CustomerID, &currency, &a.Balance.Amount, &status, &a.UpdatedAt); err != nil {
		return domain.Account{}, err
	}
	a.Balance.Currency, a.Status = domain.Currency(currency), domain.AccountStatus(status)
	return a, nil
}

func (r accountRepo) GetByID(ctx context.Context, id string) (domain.Account, error) {
	a, err := scanAccount(r.tx.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("select account: %w", err)
	}
	return a, nil
}

// GetForUpdate locks rows in ascending id order: PostgreSQL sorts before locking,
// so two transfers A→B and B→A cannot deadlock.
func (r accountRepo) GetForUpdate(ctx context.Context, ids ...string) (map[string]domain.Account, error) {
	sorted := slices.Sorted(slices.Values(ids))
	rows, err := r.tx.Query(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, sorted)
	if err != nil {
		return nil, fmt.Errorf("lock accounts: %w", err)
	}
	defer rows.Close()
	out := make(map[string]domain.Account, len(ids))
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}

func (r accountRepo) UpdateBalance(ctx context.Context, a domain.Account) error {
	tag, err := r.tx.Exec(ctx, `UPDATE accounts SET balance = $2, updated_at = $3 WHERE id = $1`, a.ID, a.Balance.Amount, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update balance: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrAccountNotFound
	}
	return nil
}

// ---- transfers ----

type transferRepo struct{ queries }

const transferCols = `id::text, requested_by, idempotency_key, request_hash, from_account_id::text,
	to_account_id::text, amount, currency, reference, status, created_at`

func scanTransfer(row pgx.Row) (domain.Transfer, error) {
	var t domain.Transfer
	var currency, status string
	err := row.Scan(&t.ID, &t.RequestedBy, &t.IdempotencyKey, &t.RequestHash, &t.FromAccountID,
		&t.ToAccountID, &t.Amount.Amount, &currency, &t.Reference, &status, &t.CreatedAt)
	t.Amount.Currency, t.Status = domain.Currency(currency), domain.TransferStatus(status)
	return t, err
}

func (r transferRepo) Create(ctx context.Context, t domain.Transfer) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO transfers (id, requested_by, idempotency_key, request_hash,
		from_account_id, to_account_id, amount, currency, reference, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		t.ID, t.RequestedBy, t.IdempotencyKey, t.RequestHash, t.FromAccountID, t.ToAccountID,
		t.Amount.Amount, string(t.Amount.Currency), t.Reference, string(t.Status), t.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == uqTransferIdempotency {
		return port.ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return fmt.Errorf("insert transfer: %w", err)
	}
	return nil
}

func (r transferRepo) GetByID(ctx context.Context, id string) (domain.Transfer, error) {
	t, err := scanTransfer(r.tx.QueryRow(ctx, `SELECT `+transferCols+` FROM transfers WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Transfer{}, domain.ErrTransferNotFound
	}
	if err != nil {
		return domain.Transfer{}, fmt.Errorf("select transfer: %w", err)
	}
	return t, nil
}

func (r transferRepo) FindByIdempotencyKey(ctx context.Context, customerID, key string) (domain.Transfer, bool, error) {
	t, err := scanTransfer(r.tx.QueryRow(ctx, `SELECT `+transferCols+` FROM transfers
		WHERE requested_by = $1 AND idempotency_key = $2`, customerID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Transfer{}, false, nil
	}
	if err != nil {
		return domain.Transfer{}, false, fmt.Errorf("select transfer by key: %w", err)
	}
	return t, true, nil
}

// ---- audit ----

type auditRepo struct{ queries }

func (r auditRepo) Append(ctx context.Context, e domain.AuditEntry) error {
	before, err := json.Marshal(e.Before)
	if err != nil {
		return fmt.Errorf("marshal audit before: %w", err)
	}
	after, err := json.Marshal(e.After)
	if err != nil {
		return fmt.Errorf("marshal audit after: %w", err)
	}
	_, err = r.tx.Exec(ctx, `INSERT INTO audit_log (id, actor, action, resource_type, resource_id, trace_id, before, after, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.ID, e.Actor, e.Action, e.ResourceType, e.ResourceID, e.TraceID, before, after, e.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}
