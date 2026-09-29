// Package service implements the ordering module's use cases.
package service

import (
	"context"
	"fmt"

	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// OrderService implements port.OrderUseCase.
// See docs/02-specs/order-flow-modules.md.
type OrderService struct {
	tx       port.TxManager
	prices   port.PriceSource
	payments port.PaymentStarter
	clock    port.Clock
	ids      port.IDGenerator
}

var _ port.OrderUseCase = (*OrderService)(nil)

// NewOrderService wires the service to its outbound ports.
func NewOrderService(tx port.TxManager, prices port.PriceSource, payments port.PaymentStarter,
	clock port.Clock, ids port.IDGenerator) *OrderService {
	return &OrderService{tx: tx, prices: prices, payments: payments, clock: clock, ids: ids}
}

// PlaceOrder prices the cart with the catalog, stores the order, then asks payment
// to collect its total. These are separate local transactions around the payment
// call (no distributed transaction): if payment fails to start, the order stays
// AWAITING_PAYMENT without an invoice (AC-04).
func (s *OrderService) PlaceOrder(ctx context.Context, cmd port.PlaceOrderCommand) (port.PlacedOrder, error) {
	if err := domain.ValidateItems(cmd.Items); err != nil {
		return port.PlacedOrder{}, err // AC-03: before any call to the catalog
	}
	ids := make([]string, len(cmd.Items))
	for i, it := range cmd.Items {
		ids[i] = it.ProductID
	}
	products, err := s.prices.FindProducts(ctx, ids)
	if err != nil {
		return port.PlacedOrder{}, fmt.Errorf("find prices: %w", err)
	}
	o, err := domain.NewOrder(s.ids.NewID(), cmd.CustomerID, cmd.Items, products, s.clock.Now().UTC())
	if err != nil {
		return port.PlacedOrder{}, err // AC-03: unknown or inactive product
	}
	if err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		return r.Orders.Create(ctx, o)
	}); err != nil {
		return port.PlacedOrder{}, fmt.Errorf("create order: %w", err)
	}
	handoff, err := s.payments.StartPayment(ctx, o)
	if err != nil {
		return port.PlacedOrder{}, fmt.Errorf("start payment: %w", err)
	}
	now := s.clock.Now().UTC()
	if err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		return r.Orders.SetInvoiceNo(ctx, o.ID, handoff.InvoiceNo, now)
	}); err != nil {
		return port.PlacedOrder{}, fmt.Errorf("record payment on order: %w", err)
	}
	o.InvoiceNo, o.UpdatedAt = handoff.InvoiceNo, now
	return port.PlacedOrder{Order: o, Payment: handoff}, nil
}

// GetOrder hides other customers' orders behind NOT_FOUND (AC-05).
func (s *OrderService) GetOrder(ctx context.Context, customerID, orderID string) (domain.Order, error) {
	var o domain.Order
	err := s.tx.WithinTx(ctx, func(ctx context.Context, r port.Repositories) error {
		var err error
		o, err = r.Orders.Get(ctx, orderID)
		return err
	})
	if err != nil {
		return domain.Order{}, err
	}
	if customerID == "" || o.CustomerID != customerID {
		return domain.Order{}, fmt.Errorf("order: %w", kernel.ErrNotFound)
	}
	return o, nil
}
