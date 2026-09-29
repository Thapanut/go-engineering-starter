package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// CheckoutService implements port.CheckoutUseCase and port.PaymentEventsUseCase.
// See docs/02-specs/payment-checkout.md.
type CheckoutService struct {
	tx      port.TxManager
	catalog port.ProductCatalog
	gateway port.PaymentGateway
	clock   port.Clock
	ids     port.IDGenerator
}

var (
	_ port.CheckoutUseCase      = (*CheckoutService)(nil)
	_ port.PaymentEventsUseCase = (*CheckoutService)(nil)
)

// NewCheckoutService wires the service to its outbound ports.
func NewCheckoutService(tx port.TxManager, catalog port.ProductCatalog, gw port.PaymentGateway,
	clock port.Clock, ids port.IDGenerator) *CheckoutService {
	return &CheckoutService{tx: tx, catalog: catalog, gateway: gw, clock: clock, ids: ids}
}

// ListProducts returns the catalog.
func (s *CheckoutService) ListProducts(ctx context.Context) ([]domain.Product, error) {
	products, err := s.catalog.ListProducts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}

// CreatePayment prices the cart from the catalog (the client sends no amount,
// AC-12), stores the payment, and only then opens the provider session, outside
// the transaction: the provider can never notify us about an invoice we have not
// stored. If the gateway fails, the payment stays PENDING without a session (AC-05).
func (s *CheckoutService) CreatePayment(ctx context.Context, cmd port.CreatePaymentCommand) (port.Checkout, error) {
	if err := domain.ValidateOrderItems(cmd.Items); err != nil {
		return port.Checkout{}, err // AC-02: before any lookup
	}
	ids := make([]string, len(cmd.Items))
	for i, it := range cmd.Items {
		ids[i] = it.ProductID
	}
	products, err := s.catalog.FindProducts(ctx, ids)
	if err != nil {
		return port.Checkout{}, fmt.Errorf("find products: %w", err)
	}
	lines, total, err := domain.PriceOrder(cmd.Items, products)
	if err != nil {
		return port.Checkout{}, err // AC-02: unknown product, mixed currency
	}
	p, err := domain.NewPendingPayment(s.ids.NewID(), newInvoiceNo(s.ids.NewID()), cmd.OrderID, cmd.CustomerID,
		total, s.clock.Now().UTC())
	if err != nil {
		return port.Checkout{}, err // AC-02
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
	return port.Checkout{Payment: p, Lines: lines, Session: sess}, nil
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
