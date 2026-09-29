package domain

import (
	"fmt"
	"math"
)

// Product is something a customer can buy, priced by the catalog (never by the client).
type Product struct {
	ID    string
	Name  string
	Price Money
}

// OrderItem is one product and quantity the customer asked to pay for.
type OrderItem struct {
	ProductID string
	Quantity  int
}

// OrderLine is an item priced from the catalog.
type OrderLine struct {
	Product  Product
	Quantity int
	Total    Money
}

// Order limits (spec payment-checkout AC-02).
const (
	MaxOrderItems   = 20
	MaxItemQuantity = 99
)

// ValidateOrderItems checks the shape of a cart before any product is looked up.
func ValidateOrderItems(items []OrderItem) error {
	if len(items) == 0 || len(items) > MaxOrderItems {
		return Invalid(fmt.Sprintf("items must contain 1-%d products", MaxOrderItems))
	}
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		switch {
		case it.ProductID == "":
			return Invalid("productId is required")
		case it.Quantity < 1 || it.Quantity > MaxItemQuantity:
			return Invalid(fmt.Sprintf("quantity must be 1-%d", MaxItemQuantity))
		case seen[it.ProductID]:
			return Invalid("each productId may appear only once")
		}
		seen[it.ProductID] = true
	}
	return nil
}

// PriceOrder prices valid items with catalog prices and returns the lines and the
// total. The client never supplies an amount (spec payment-checkout AC-01, AC-12).
func PriceOrder(items []OrderItem, products map[string]Product) ([]OrderLine, Money, error) {
	if err := ValidateOrderItems(items); err != nil {
		return nil, Money{}, err
	}
	lines := make([]OrderLine, 0, len(items))
	var total Money
	for i, it := range items {
		p, ok := products[it.ProductID]
		if !ok {
			return nil, Money{}, Invalid("unknown productId")
		}
		if p.Price.Amount > math.MaxInt64/int64(it.Quantity) {
			return nil, Money{}, Invalid("amount overflow")
		}
		line := Money{Amount: p.Price.Amount * int64(it.Quantity), Currency: p.Price.Currency}
		if i == 0 {
			total = Money{Currency: line.Currency}
		}
		var err error
		if total, err = total.Add(line); err != nil {
			return nil, Money{}, err // mixed currencies or overflow
		}
		lines = append(lines, OrderLine{Product: p, Quantity: it.Quantity, Total: line})
	}
	return lines, total, nil
}
