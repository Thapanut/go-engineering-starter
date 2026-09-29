// Package port defines the ordering module's inbound and outbound interfaces.
// Ordering reaches catalog and payment only through its own outbound ports
// (PriceSource, PaymentStarter); adapters implement them (ADR-0005).
package port

import (
	"context"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
)

// PlaceOrderCommand is a customer's cart. It carries no prices.
type PlaceOrderCommand struct {
	CustomerID string
	Items      []domain.Item
}

// PaymentHandoff is what the browser needs to pay for an order.
type PaymentHandoff struct {
	InvoiceNo   string
	Token       string
	CheckoutURL string
}

// PlacedOrder is a stored order and the payment started for it.
type PlacedOrder struct {
	Order   domain.Order
	Payment PaymentHandoff
}

// OrderUseCase places and reads orders (inbound port).
// See docs/02-specs/order-flow-modules.md.
type OrderUseCase interface {
	PlaceOrder(ctx context.Context, cmd PlaceOrderCommand) (PlacedOrder, error)
	// GetOrder returns the customer's order, or kernel.ErrNotFound
	// for an unknown order and for another customer's order alike.
	GetOrder(ctx context.Context, customerID, orderID string) (domain.Order, error)
}

// PriceSource returns current prices for products that can be ordered, keyed by
// id; unknown or unorderable ids are absent (outbound port; the catalog module).
type PriceSource interface {
	FindProducts(ctx context.Context, ids []string) (map[string]domain.PricedProduct, error)
}

// PaymentStarter asks the payment module to collect an order's amount (outbound port).
type PaymentStarter interface {
	StartPayment(ctx context.Context, o domain.Order) (PaymentHandoff, error)
}

// OrderRepository persists orders with their lines (outbound port).
type OrderRepository interface {
	Create(ctx context.Context, o domain.Order) error
	// Get returns kernel.ErrNotFound if there is no such order.
	Get(ctx context.Context, id string) (domain.Order, error)
	// GetForUpdate is Get with a row lock until the transaction ends.
	GetForUpdate(ctx context.Context, id string) (domain.Order, error)
	// SetInvoiceNo records the payment started for an order.
	SetInvoiceNo(ctx context.Context, id, invoiceNo string, at time.Time) error
	// UpdateStatus persists an AWAITING_PAYMENT → final transition. It must only
	// update an order still awaiting payment and returns kernel.ErrConflict otherwise.
	UpdateStatus(ctx context.Context, o domain.Order) error
}

// ProcessedEvents remembers which events were applied (outbound port).
type ProcessedEvents interface {
	// MarkProcessed records eventID and reports whether this is its first time.
	MarkProcessed(ctx context.Context, eventID string, at time.Time) (first bool, err error)
}

// Repositories are ordering's repositories bound to one unit of work.
type Repositories struct {
	Orders OrderRepository
	Events ProcessedEvents
}

// TxManager runs fn in one ordering transaction.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, r Repositories) error) error
}

// Clock abstracts time for deterministic tests.
type Clock interface{ Now() time.Time }

// IDGenerator creates unique opaque ids (UUIDs).
type IDGenerator interface{ NewID() string }
