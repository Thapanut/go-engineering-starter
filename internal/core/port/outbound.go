package port

import (
	"context"
	"errors"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// ErrDuplicateIdempotencyKey is returned by TransferRepository.Create when another
// transaction already stored a transfer with the same (customer, key).
var ErrDuplicateIdempotencyKey = errors.New("duplicate idempotency key")

// AccountRepository persists accounts.
type AccountRepository interface {
	// GetByID returns domain.ErrAccountNotFound if the account does not exist.
	GetByID(ctx context.Context, id string) (domain.Account, error)
	// GetForUpdate locks the given accounts until the transaction ends, in ascending
	// id order to avoid deadlocks. Missing ids are absent from the result.
	GetForUpdate(ctx context.Context, ids ...string) (map[string]domain.Account, error)
	UpdateBalance(ctx context.Context, a domain.Account) error
}

// TransferRepository persists transfers.
type TransferRepository interface {
	// Create returns ErrDuplicateIdempotencyKey on a (customer, key) conflict.
	Create(ctx context.Context, t domain.Transfer) error
	// GetByID returns domain.ErrTransferNotFound if the transfer does not exist.
	GetByID(ctx context.Context, id string) (domain.Transfer, error)
	FindByIdempotencyKey(ctx context.Context, customerID, key string) (domain.Transfer, bool, error)
}

// AuditRepository appends immutable audit entries.
type AuditRepository interface {
	Append(ctx context.Context, e domain.AuditEntry) error
}

// Repositories are the repositories bound to one unit of work.
type Repositories struct {
	Accounts  AccountRepository
	Transfers TransferRepository
	Audit     AuditRepository
}

// TxManager runs fn in one atomic unit of work. If fn returns an error, nothing is persisted.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, r Repositories) error) error
}

// Clock abstracts time for deterministic tests.
type Clock interface{ Now() time.Time }

// IDGenerator creates unique opaque ids (UUIDs).
type IDGenerator interface{ NewID() string }
