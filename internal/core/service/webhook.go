package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// WebhookService implements port.WebhookUseCase.
// See docs/02-specs/2c2p-payment-webhook.md.
type WebhookService struct {
	validator port.WebhookSignatureValidator
	tx        port.TxManager
	clock     port.Clock
	log       *slog.Logger
}

var _ port.WebhookUseCase = (*WebhookService)(nil)

// NewWebhookService wires the service to its outbound ports.
func NewWebhookService(v port.WebhookSignatureValidator, tx port.TxManager, clock port.Clock, log *slog.Logger) *WebhookService {
	return &WebhookService{validator: v, tx: tx, clock: clock, log: log}
}

// HandlePaymentNotification verifies the webhook, then applies it to the payment
// in one transaction. Duplicate deliveries return OutcomeDuplicate without any
// write (spec AC-03, AC-10).
func (s *WebhookService) HandlePaymentNotification(ctx context.Context, rawBody []byte) (port.WebhookResult, error) {
	n, err := s.validator.Verify(ctx, rawBody)
	if err != nil {
		return port.WebhookResult{}, err // AC-05, AC-06: nothing touches the DB
	}
	if err := n.Validate(); err != nil {
		return port.WebhookResult{}, err // AC-09
	}

	var res port.WebhookResult
	err = s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		p, err := r.Payments.GetByInvoiceNoForUpdate(ctx, n.InvoiceNo)
		if err != nil {
			return err // AC-07: domain.ErrNotFound
		}
		outcome, err := p.Apply(n, s.clock.Now().UTC())
		if err != nil {
			return err // AC-08: domain.ErrPaymentMismatch
		}
		res = port.WebhookResult{PaymentID: p.ID, Outcome: outcome}
		if outcome != domain.OutcomeProcessed {
			return nil // AC-03, AC-04: terminal payments are never written again
		}
		if err := r.Payments.UpdateOutcome(ctx, p); err != nil {
			return fmt.Errorf("update payment outcome: %w", err)
		}
		return nil
	})
	if err != nil {
		return port.WebhookResult{}, err
	}
	if res.Outcome == domain.OutcomeConflictIgnored {
		s.log.WarnContext(ctx, "payment notification conflicts with final state; needs reconciliation",
			slog.String("payment_id", res.PaymentID), slog.String("notified_outcome", string(n.Outcome)))
	}
	return res, nil
}
