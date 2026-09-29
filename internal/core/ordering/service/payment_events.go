package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// PaymentEventService implements port.PaymentEventHandler.
// See docs/02-specs/order-flow-modules.md §3 "Event handling".
type PaymentEventService struct {
	tx    port.TxManager
	clock port.Clock
}

var _ port.PaymentEventHandler = (*PaymentEventService)(nil)

// NewPaymentEventService wires the handler to ordering's store.
func NewPaymentEventService(tx port.TxManager, clock port.Clock) *PaymentEventService {
	return &PaymentEventService{tx: tx, clock: clock}
}

// HandlePaymentStatusChanged records the event and applies it in one ordering
// transaction, so a redelivered event changes nothing (AC-07). Events that can
// never apply (unknown order, amount mismatch) are recorded too, so they are not
// retried forever (AC-09, AC-10).
func (s *PaymentEventService) HandlePaymentStatusChanged(ctx context.Context, e port.PaymentStatusChanged) (port.EventOutcome, error) {
	if e.EventID == "" {
		return "", kernel.Invalid("event_id is required")
	}
	now := s.clock.Now().UTC()
	var out port.EventOutcome
	err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		first, err := r.Events.MarkProcessed(ctx, e.EventID, now)
		if err != nil {
			return fmt.Errorf("record event: %w", err)
		}
		if !first {
			out = port.EventAlreadyProcessed
			return nil
		}
		if e.OrderID == "" {
			out = port.EventUnknownOrder
			return nil
		}
		o, err := r.Orders.GetForUpdate(ctx, e.OrderID)
		if errors.Is(err, kernel.ErrNotFound) {
			out = port.EventUnknownOrder
			return nil
		}
		if err != nil {
			return err
		}
		res, err := o.ApplyPayment(domain.PaymentResult{Succeeded: e.Succeeded, Amount: e.Amount}, now)
		switch {
		case errors.Is(err, domain.ErrAmountMismatch):
			out = port.EventAmountMismatch
			return nil
		case err != nil:
			return err
		case res == domain.ResultDuplicate:
			out = port.EventDuplicate
			return nil
		case res == domain.ResultConflictIgnored:
			out = port.EventConflictIgnored
			return nil
		}
		if err := r.Orders.UpdateStatus(ctx, o); err != nil {
			return fmt.Errorf("update order status: %w", err)
		}
		out = port.EventApplied
		return nil
	})
	if err != nil {
		return "", err
	}
	return out, nil
}
