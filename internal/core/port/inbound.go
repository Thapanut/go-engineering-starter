// Package port defines the interfaces of the hexagon.
// Inbound ports are implemented by the core and called by driving adapters (HTTP, gRPC, consumers).
// Outbound ports are called by the core and implemented by driven adapters (Postgres, memory, …).
package port

import (
	"context"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// TransferResult is the outcome of CreateTransfer.
type TransferResult struct {
	Transfer domain.Transfer
	Replayed bool // true when an earlier request with the same key is returned (spec AC-02)
}

// TransferUseCase moves money between accounts.
type TransferUseCase interface {
	CreateTransfer(ctx context.Context, req domain.TransferRequest, traceID string) (TransferResult, error)
	GetTransfer(ctx context.Context, customerID, transferID string) (domain.Transfer, error)
}

// AccountQuery reads accounts on behalf of a customer.
type AccountQuery interface {
	GetAccount(ctx context.Context, customerID, accountID string) (domain.Account, error)
}
