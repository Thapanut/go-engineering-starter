package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// CheckoutService implements port.CheckoutUseCase and port.PaymentEventsUseCase.
// See docs/02-specs/payment-checkout.md.
type CheckoutService struct {
	tx      port.TxManager
	gateway port.PaymentGateway
	clock   port.Clock
	ids     port.IDGenerator
}

var (
	_ port.CheckoutUseCase      = (*CheckoutService)(nil)
	_ port.PaymentEventsUseCase = (*CheckoutService)(nil)
)

// NewCheckoutService wires the service to its outbound ports.
func NewCheckoutService(tx port.TxManager, gw port.PaymentGateway, clock port.Clock, ids port.IDGenerator) *CheckoutService {
	return &CheckoutService{tx: tx, gateway: gw, clock: clock, ids: ids}
}

// CreatePayment stores the payment and only then opens the provider session,
// outside the transaction: the provider can never notify us about an invoice we
// have not stored. If the gateway fails, the payment stays PENDING without a
// session. The amount is trusted: only the ordering module calls this (ADR-0005).
func (s *CheckoutService) CreatePayment(ctx context.Context, cmd port.CreatePaymentCommand) (port.Checkout, error) {
	p, err := domain.NewPendingPayment(s.ids.NewID(), newInvoiceNo(s.ids.NewID()), cmd.OrderID, cmd.CustomerID,
		cmd.Amount, s.clock.Now().UTC())
	if err != nil {
		return port.Checkout{}, err
	}
	err = s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		return r.Payments.Create(ctx, p)
	})
	if err != nil {
		return port.Checkout{}, fmt.Errorf("create payment: %w", err)
	}
	sess, err := s.gateway.CreateSession(ctx, p)
	if err != nil {
		return port.Checkout{}, fmt.Errorf("create payment session: %w", err)
	}
	return port.Checkout{Payment: p, Session: sess}, nil
}

// GetPayment hides other customers' payments behind NOT_FOUND (AC-07).
func (s *CheckoutService) GetPayment(ctx context.Context, customerID, invoiceNo string) (domain.Payment, error) {
	var p domain.Payment
	err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		var err error
		p, err = r.Payments.GetByInvoiceNo(ctx, invoiceNo)
		return err
	})
	if err != nil {
		return domain.Payment{}, err
	}
	if customerID == "" || p.CustomerID != customerID {
		return domain.Payment{}, fmt.Errorf("payment: %w", domain.ErrNotFound)
	}
	return p, nil
}

// ListPaymentEvents returns the outbox events of the customer's payment, which
// are keyed by payment id (AC-13).
func (s *CheckoutService) ListPaymentEvents(ctx context.Context, customerID, invoiceNo string) ([]port.OutboxRecord, error) {
	p, err := s.GetPayment(ctx, customerID, invoiceNo)
	if err != nil {
		return nil, err
	}
	var recs []port.OutboxRecord
	err = s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		var err error
		recs, err = r.Outbox.ListByKey(ctx, p.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list payment events: %w", err)
	}
	return recs, nil
}

// newInvoiceNo derives the reference shared with 2C2P from a UUID: "INV" and 32
// upper-case hex characters, alphanumeric only.
func newInvoiceNo(uuid string) string {
	return "INV" + strings.ToUpper(strings.ReplaceAll(uuid, "-", ""))
}
