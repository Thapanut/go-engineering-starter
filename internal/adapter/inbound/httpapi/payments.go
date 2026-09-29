package httpapi

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// PaymentModule exposes payment status polling under /v1. Payments are created
// by the ordering module only (ADR-0005), so there is no public create route.
// See docs/02-specs/payment-checkout.md.
type PaymentModule struct {
	UseCase port.CheckoutUseCase
}

var _ Module = PaymentModule{}

// Register adds GET /v1/payments/:invoiceNo/status.
func (m PaymentModule) Register(v1 fiber.Router) {
	v1.Get("/payments/:invoiceNo/status", m.status)
}

// Money on the wire: the decimal a person reads ("1000.00"), the same value in
// minor units with a "Minor" suffix (100000), and the currency.
type amountFields struct {
	Amount      string `json:"amount"`
	AmountMinor int64  `json:"amountMinor"`
	Currency    string `json:"currency"`
}

func toAmountFields(m domain.Money) amountFields {
	return amountFields{Amount: m.Decimal(), AmountMinor: m.Amount, Currency: string(m.Currency)}
}

type paymentStatusResponse struct {
	InvoiceNo string `json:"invoiceNo"`
	OrderID   string `json:"orderId"`
	Status    string `json:"status"`
	amountFields
	UpdatedAt string `json:"updatedAt"`
}

// status serves the frontend's polling. The invoice number is only used during
// this call, so the fasthttp-backed param needs no copy.
func (m PaymentModule) status(c *fiber.Ctx) error {
	p, err := m.UseCase.GetPayment(c.UserContext(), customerIDOf(c), c.Params("invoiceNo"))
	if err != nil {
		return err
	}
	return c.JSON(paymentStatusResponse{
		InvoiceNo: p.InvoiceNo, OrderID: p.OrderID, Status: string(p.Status),
		amountFields: toAmountFields(p.Amount), UpdatedAt: p.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}
