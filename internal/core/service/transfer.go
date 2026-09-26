// Package service implements the inbound ports. It depends on domain and port only.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// TransferService implements port.TransferUseCase and port.AccountQuery.
type TransferService struct {
	tx    port.TxManager
	clock port.Clock
	ids   port.IDGenerator
}

var (
	_ port.TransferUseCase = (*TransferService)(nil)
	_ port.AccountQuery    = (*TransferService)(nil)
)

// NewTransferService wires the service to its outbound ports.
func NewTransferService(tx port.TxManager, clock port.Clock, ids port.IDGenerator) *TransferService {
	return &TransferService{tx: tx, clock: clock, ids: ids}
}

// GetAccount returns the account if customerID owns it. Accounts owned by someone
// else are reported as not found so ids cannot be probed (spec AC-07).
func (s *TransferService) GetAccount(ctx context.Context, customerID, accountID string) (domain.Account, error) {
	if !domain.IsValidID(accountID) {
		return domain.Account{}, domain.Invalid("accountId must be a UUID")
	}
	accountID = domain.NormalizeID(accountID)
	var acc domain.Account
	err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		a, err := r.Accounts.GetByID(ctx, accountID)
		if err != nil {
			return err
		}
		if !a.OwnedBy(customerID) {
			return domain.ErrAccountNotFound
		}
		acc = a
		return nil
	})
	return acc, err
}

// GetTransfer returns the transfer if customerID requested it (spec AC-14).
func (s *TransferService) GetTransfer(ctx context.Context, customerID, transferID string) (domain.Transfer, error) {
	if !domain.IsValidID(transferID) {
		return domain.Transfer{}, domain.Invalid("transferId must be a UUID")
	}
	transferID = domain.NormalizeID(transferID)
	var t domain.Transfer
	err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		found, err := r.Transfers.GetByID(ctx, transferID)
		if err != nil {
			return err
		}
		if found.RequestedBy != customerID {
			return domain.ErrTransferNotFound
		}
		t = found
		return nil
	})
	return t, err
}

// CreateTransfer moves money exactly once per (customer, idempotency key).
// See docs/02-specs/intra-bank-transfer.md AC-01..AC-13.
func (s *TransferService) CreateTransfer(ctx context.Context, req domain.TransferRequest, traceID string) (port.TransferResult, error) {
	req = req.Normalized()
	if err := req.Validate(); err != nil {
		return port.TransferResult{}, err
	}
	res, err := s.createOnce(ctx, req, traceID)
	if errors.Is(err, port.ErrDuplicateIdempotencyKey) {
		// A concurrent request with the same key committed first. Retrying now
		// finds its transfer and replays it (spec AC-13).
		res, err = s.createOnce(ctx, req, traceID)
	}
	return res, err
}

func (s *TransferService) createOnce(ctx context.Context, req domain.TransferRequest, traceID string) (port.TransferResult, error) {
	fingerprint := req.Fingerprint()
	var res port.TransferResult

	err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		existing, found, err := r.Transfers.FindByIdempotencyKey(ctx, req.CustomerID, req.IdempotencyKey)
		if err != nil {
			return fmt.Errorf("find by idempotency key: %w", err)
		}
		if found {
			if existing.RequestHash != fingerprint {
				return domain.ErrIdempotencyKeyReused // AC-03
			}
			res = port.TransferResult{Transfer: existing, Replayed: true} // AC-02
			return nil
		}

		accounts, err := r.Accounts.GetForUpdate(ctx, req.FromAccountID, req.ToAccountID)
		if err != nil {
			return fmt.Errorf("lock accounts: %w", err)
		}
		from, ok := accounts[req.FromAccountID]
		if !ok || !from.OwnedBy(req.CustomerID) {
			return domain.ErrAccountNotFound // AC-07: never reveal that someone else owns it
		}
		to, ok := accounts[req.ToAccountID]
		if !ok {
			return domain.ErrAccountNotFound // AC-08
		}
		if from.Balance.Currency != req.Amount.Currency || to.Balance.Currency != req.Amount.Currency {
			return domain.ErrCurrencyMismatch // AC-10
		}
		if to.Status != domain.AccountActive {
			return domain.ErrAccountInactive // AC-09: check before debiting
		}
		before := map[string]any{"from": from.Balance.Amount, "to": to.Balance.Amount}
		if err := from.Debit(req.Amount); err != nil {
			return err // AC-04, AC-09
		}
		if err := to.Credit(req.Amount); err != nil {
			return err
		}

		now := s.clock.Now().UTC()
		from.UpdatedAt, to.UpdatedAt = now, now
		t := domain.Transfer{
			ID:             s.ids.NewID(),
			RequestedBy:    req.CustomerID,
			IdempotencyKey: req.IdempotencyKey,
			RequestHash:    fingerprint,
			FromAccountID:  from.ID,
			ToAccountID:    to.ID,
			Amount:         req.Amount,
			Reference:      req.Reference,
			Status:         domain.TransferCompleted,
			CreatedAt:      now,
		}
		if err := r.Transfers.Create(ctx, t); err != nil {
			if errors.Is(err, port.ErrDuplicateIdempotencyKey) {
				return err
			}
			return fmt.Errorf("create transfer: %w", err)
		}
		if err := r.Accounts.UpdateBalance(ctx, from); err != nil {
			return fmt.Errorf("update source balance: %w", err)
		}
		if err := r.Accounts.UpdateBalance(ctx, to); err != nil {
			return fmt.Errorf("update destination balance: %w", err)
		}
		if err := r.Audit.Append(ctx, domain.AuditEntry{ // AC-11
			ID:           s.ids.NewID(),
			Actor:        req.CustomerID,
			Action:       domain.AuditTransferCreated,
			ResourceType: "transfer",
			ResourceID:   t.ID,
			TraceID:      traceID,
			Before:       map[string]any{"balances": before},
			After: map[string]any{
				"balances": map[string]any{"from": from.Balance.Amount, "to": to.Balance.Amount},
				"amount":   t.Amount.Amount, "currency": string(t.Amount.Currency),
				"fromAccountId": t.FromAccountID, "toAccountId": t.ToAccountID,
			},
			OccurredAt: now,
		}); err != nil {
			return fmt.Errorf("append audit: %w", err)
		}
		res = port.TransferResult{Transfer: t}
		return nil
	})
	return res, err
}
