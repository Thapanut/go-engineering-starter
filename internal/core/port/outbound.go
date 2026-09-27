// Package port defines the interfaces of the hexagon.
//
// Inbound ports (use cases) are implemented by internal/core/service and called by
// driving adapters (HTTP, gRPC, consumers). Put each feature's inbound interface in
// its own file, e.g. port/<feature>.go.
//
// Outbound ports are called by the core and implemented by driven adapters
// (internal/adapter/outbound/*).
package port

import (
	"context"
	"time"
)

// Repositories are the repositories bound to one unit of work.
// Add one field per repository port as features are introduced, and implement it
// in every outbound adapter (postgres, memory).
type Repositories struct {
	Payments PaymentRepository
	Outbox   OutboxRepository
}

// TxManager runs fn in one atomic unit of work. If fn returns an error, nothing is persisted.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, r Repositories) error) error
}

// Clock abstracts time for deterministic tests.
type Clock interface{ Now() time.Time }

// IDGenerator creates unique opaque ids (UUIDs).
type IDGenerator interface{ NewID() string }
