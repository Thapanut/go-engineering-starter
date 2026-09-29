package httpapi

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// PaymentModule exposes the catalog, payment checkout, and status polling under /v1.
// See docs/02-specs/payment-checkout.md.
type PaymentModule struct {
	UseCase port.CheckoutUseCase
}

var _ Module = PaymentModule{}

// Register adds GET /v1/products, POST /v1/payments, and GET /v1/payments/:invoiceNo/status.
func (m PaymentModule) Register(v1 fiber.Router) {
	v1.Get("/products", m.products)
	v1.Post("/payments", m.create)
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

type productResponse struct {
	ProductID  string `json:"productId"`
	Name       string `json:"name"`
	Price      string `json:"price"`
	PriceMinor int64  `json:"priceMinor"`
	Currency   string `json:"currency"`
}

type productListResponse struct {
	Products []productResponse `json:"products"`
}

type orderItemRequest struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// createPaymentRequest has no amount: the backend prices the items (AC-12).
type createPaymentRequest struct {
	OrderID string             `json:"orderId"`
	Items   []orderItemRequest `json:"items"`
}

type orderLineResponse struct {
	ProductID      string `json:"productId"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPrice      string `json:"unitPrice"`
	UnitPriceMinor int64  `json:"unitPriceMinor"`
	LineTotal      string `json:"lineTotal"`
	LineTotalMinor int64  `json:"lineTotalMinor"`
	Currency       string `json:"currency"`
}

type paymentCheckoutResponse struct {
	PaymentID string              `json:"paymentId"`
	InvoiceNo string              `json:"invoiceNo"`
	OrderID   string              `json:"orderId"`
	Status    string              `json:"status"`
	Lines     []orderLineResponse `json:"lines"`
	amountFields
	PaymentToken string `json:"paymentToken"`
	CheckoutURL  string `json:"checkoutUrl"`
	CreatedAt    string `json:"createdAt"`
}

type paymentStatusResponse struct {
	InvoiceNo string `json:"invoiceNo"`
	OrderID   string `json:"orderId"`
	Status    string `json:"status"`
	amountFields
	UpdatedAt string `json:"updatedAt"`
}

func (m PaymentModule) products(c *fiber.Ctx) error {
	products, err := m.UseCase.ListProducts(c.UserContext())
	if err != nil {
		return err
	}
	out := productListResponse{Products: make([]productResponse, len(products))}
	for i, p := range products {
		out.Products[i] = productResponse{ProductID: p.ID, Name: p.Name, Price: p.Price.Decimal(),
			PriceMinor: p.Price.Amount, Currency: string(p.Price.Currency)}
	}
	return c.JSON(out)
}

func (m PaymentModule) create(c *fiber.Ctx) error {
	var req createPaymentRequest
	if err := decodeStrict(c, &req); err != nil {
		return err
	}
	items := make([]domain.OrderItem, len(req.Items))
	for i, it := range req.Items {
		items[i] = domain.OrderItem{ProductID: it.ProductID, Quantity: it.Quantity}
	}
	co, err := m.UseCase.CreatePayment(c.UserContext(), port.CreatePaymentCommand{
		CustomerID: customerIDOf(c), OrderID: req.OrderID, Items: items,
	})
	if err != nil {
		return err
	}
	p := co.Payment
	lines := make([]orderLineResponse, len(co.Lines))
	for i, l := range co.Lines {
		lines[i] = orderLineResponse{
			ProductID: l.Product.ID, Name: l.Product.Name, Quantity: l.Quantity,
			UnitPrice: l.Product.Price.Decimal(), UnitPriceMinor: l.Product.Price.Amount,
			LineTotal: l.Total.Decimal(), LineTotalMinor: l.Total.Amount, Currency: string(l.Total.Currency),
		}
	}
	return c.Status(http.StatusCreated).JSON(paymentCheckoutResponse{
		PaymentID: p.ID, InvoiceNo: p.InvoiceNo, OrderID: p.OrderID, Status: string(p.Status), Lines: lines,
		amountFields: toAmountFields(p.Amount), PaymentToken: co.Session.Token, CheckoutURL: co.Session.CheckoutURL,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
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
