// Package paymentclient implements the ordering module's PaymentStarter by
// calling the payment module's inbound port. It is ordering's anti-corruption
// layer towards payment (ADR-0005): today an in-process call, later an HTTP or
// gRPC client, without changing ordering's core.
package paymentclient

import (
	"context"

	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	paymentport "github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// Client adapts paymentport.CheckoutUseCase to orderingport.PaymentStarter.
type Client struct{ Payments paymentport.CheckoutUseCase }

var _ orderingport.PaymentStarter = Client{}

// StartPayment asks payment to collect the order's total for its customer.
func (c Client) StartPayment(ctx context.Context, o ordering.Order) (orderingport.PaymentHandoff, error) {
	co, err := c.Payments.CreatePayment(ctx, paymentport.CreatePaymentCommand{
		CustomerID: o.CustomerID, OrderID: o.ID, Amount: o.Amount,
	})
	if err != nil {
		return orderingport.PaymentHandoff{}, err
	}
	return orderingport.PaymentHandoff{InvoiceNo: co.Payment.InvoiceNo, Token: co.Session.Token,
		CheckoutURL: co.Session.CheckoutURL}, nil
}
