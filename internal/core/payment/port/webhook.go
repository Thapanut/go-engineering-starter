package port

import (
	"context"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
)

// WebhookResult is the outcome of handling one payment notification.
type WebhookResult struct {
	PaymentID string
	Outcome   domain.WebhookOutcome
}

// WebhookUseCase handles payment-provider webhooks (inbound port).
type WebhookUseCase interface {
	// HandlePaymentNotification verifies and applies one raw webhook body.
	// It is safe to call repeatedly with the same body (idempotent).
	HandlePaymentNotification(ctx context.Context, rawBody []byte) (WebhookResult, error)
}

// WebhookVerifier authenticates a raw provider webhook and decodes it
// into a provider-agnostic notification (outbound port). It returns
// domain.ErrInvalidSignature when the body cannot be trusted.
type WebhookVerifier interface {
	Verify(ctx context.Context, rawBody []byte) (domain.PaymentNotification, error)
}

// PaymentRepository persists payments (outbound port).
type PaymentRepository interface {
	Create(ctx context.Context, p domain.Payment) error
	// GetByInvoiceNo reads a payment without locking it.
	// It returns domain.ErrNotFound if no payment has that invoice number.
	GetByInvoiceNo(ctx context.Context, invoiceNo string) (domain.Payment, error)
	// GetByInvoiceNoForUpdate locks the payment until the transaction ends.
	// It returns domain.ErrNotFound if no payment has that invoice number.
	GetByInvoiceNoForUpdate(ctx context.Context, invoiceNo string) (domain.Payment, error)
	// UpdateOutcome persists a PENDING → terminal transition. It must only update
	// a row that is still PENDING and returns domain.ErrConflict otherwise.
	UpdateOutcome(ctx context.Context, p domain.Payment) error
}
