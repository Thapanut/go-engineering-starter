// Package domain holds the ordering context's entities and rules (ADR-0005). It
// depends on the standard library and the shared kernel only.
package domain

import (
	"fmt"
	"math"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Status is the lifecycle state of an order. PAID and PAYMENT_FAILED are final.
type Status string

// Order statuses.
const (
	AwaitingPayment Status = "AWAITING_PAYMENT"
	Paid            Status = "PAID"
	PaymentFailed   Status = "PAYMENT_FAILED"
)

// IsFinal reports whether no further transition is allowed.
func (s Status) IsFinal() bool { return s == Paid || s == PaymentFailed }

// Cart limits (spec order-flow-modules AC-03).
const (
	MaxItems    = 20
	MaxQuantity = 99
)

// Item is one product and quantity the customer wants.
type Item struct {
	ProductID string
	Quantity  int
}

// PricedProduct is ordering's view of a catalog product at order time.
type PricedProduct struct {
	ID    string
	Name  string
	Price kernel.Money
}

// Line is an ordered product. It snapshots the name and unit price, so later
// catalog changes do not alter the order.
type Line struct {
	No        int
	ProductID string
	Name      string
	UnitPrice kernel.Money
	Quantity  int
	Total     kernel.Money
}

// Order is what the customer bought and what ordering asked payment to collect.
type Order struct {
	ID         string
	CustomerID string
	Status     Status
	Lines      []Line
	Amount     kernel.Money // sum of the lines
	InvoiceNo  string       // payment's reference, empty until payment has started
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ValidateItems checks the shape of a cart before any product is looked up.
func ValidateItems(items []Item) error {
	if len(items) == 0 || len(items) > MaxItems {
		return kernel.Invalid(fmt.Sprintf("items must contain 1-%d products", MaxItems))
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		switch {
		case it.ProductID == "":
			return kernel.Invalid("productId is required")
		case it.Quantity < 1 || it.Quantity > MaxQuantity:
			return kernel.Invalid(fmt.Sprintf("quantity must be 1-%d", MaxQuantity))
		case seen[it.ProductID]:
			return kernel.Invalid("each productId may appear only once")
		}
		seen[it.ProductID] = true
	}
	return nil
}

// NewOrder prices items with the given products and returns an order awaiting
// payment. An item whose product is absent (unknown or inactive) is invalid.
func NewOrder(id, customerID string, items []Item, products map[string]PricedProduct, now time.Time) (Order, error) {
	if customerID == "" {
		return Order{}, kernel.Invalid("customer is required")
	}
	if err := ValidateItems(items); err != nil {
		return Order{}, err
	}
	lines := make([]Line, 0, len(items))
	var total kernel.Money
	for i, it := range items {
		p, ok := products[it.ProductID]
		if !ok {
			return Order{}, kernel.Invalid("unknown productId")
		}
		if p.Price.Amount <= 0 || p.Price.Amount > math.MaxInt64/int64(it.Quantity) {
			return Order{}, kernel.Invalid("amount out of range")
		}
		lineTotal := kernel.Money{Amount: p.Price.Amount * int64(it.Quantity), Currency: p.Price.Currency}
		if i == 0 {
			total = kernel.Money{Currency: lineTotal.Currency}
		}
		var err error
		if total, err = total.Add(lineTotal); err != nil {
			return Order{}, err // mixed currencies or overflow
		}
		lines = append(lines, Line{No: i + 1, ProductID: p.ID, Name: p.Name, UnitPrice: p.Price, Quantity: it.Quantity, Total: lineTotal})
	}
	return Order{ID: id, CustomerID: customerID, Status: AwaitingPayment, Lines: lines, Amount: total,
		CreatedAt: now, UpdatedAt: now}, nil
}
