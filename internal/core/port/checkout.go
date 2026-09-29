package port

import (
	"context"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// CreatePaymentCommand asks payment to collect an amount for an order. It comes
// from the ordering module, which priced the order (ADR-0005), never from a browser.
type CreatePaymentCommand struct {
	CustomerID string
	OrderID    string
	Amount     domain.Money
}

// PaymentSession is what the provider needs the customer's browser to use to pay.
type PaymentSession struct {
	Token       string
	CheckoutURL string
}

// Checkout is a created payment and its provider session.
type Checkout struct {
	Payment domain.Payment
	Session PaymentSession
}

// CheckoutUseCase starts payments and reports their status (inbound port).
// See docs/02-specs/payment-checkout.md and order-flow-modules.md.
type CheckoutUseCase interface {
	// CreatePayment stores a new PENDING payment owned by cmd.CustomerID for
	// cmd.Amount and opens a provider session for it.
	CreatePayment(ctx context.Context, cmd CreatePaymentCommand) (Checkout, error)
	// GetPayment returns the customer's payment. It returns domain.ErrNotFound for
	// an unknown invoice and for another customer's payment alike.
	GetPayment(ctx context.Context, customerID, invoiceNo string) (domain.Payment, error)
}

// PaymentEventsUseCase shows the outbox events raised for one of the customer's
// payments (inbound port). Used by the local demo page only.
type PaymentEventsUseCase interface {
	ListPaymentEvents(ctx context.Context, customerID, invoiceNo string) ([]OutboxRecord, error)
}

// PaymentGateway opens a payment session with the provider (outbound port).
type PaymentGateway interface {
	CreateSession(ctx context.Context, p domain.Payment) (PaymentSession, error)
}
