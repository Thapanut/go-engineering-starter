package port

import (
	"context"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// CreatePaymentCommand is a customer's request to pay for a cart. It carries no
// amount: the service prices the items from the catalog.
type CreatePaymentCommand struct {
	CustomerID string
	OrderID    string
	Items      []domain.OrderItem
}

// PaymentSession is what the provider needs the customer's browser to use to pay.
type PaymentSession struct {
	Token       string
	CheckoutURL string
}

// Checkout is a created payment, the priced order lines, and its provider session.
type Checkout struct {
	Payment domain.Payment
	Lines   []domain.OrderLine
	Session PaymentSession
}

// CheckoutUseCase starts payments and reports their status (inbound port).
// See docs/02-specs/payment-checkout.md.
type CheckoutUseCase interface {
	// ListProducts returns the catalog the customer can buy from.
	ListProducts(ctx context.Context) ([]domain.Product, error)
	// CreatePayment prices cmd.Items, stores a new PENDING payment owned by
	// cmd.CustomerID for the total, and opens a provider session for it.
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

// ProductCatalog is the price owner (outbound port).
type ProductCatalog interface {
	ListProducts(ctx context.Context) ([]domain.Product, error)
	// FindProducts returns the known products among ids, keyed by id. Unknown ids
	// are simply absent.
	FindProducts(ctx context.Context, ids []string) (map[string]domain.Product, error)
}
