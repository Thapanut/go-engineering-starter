package httpapi

import (
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
)

// OrderModule exposes the ordering module under /v1: the browser's entry point
// for checkout (ADR-0005). See docs/02-specs/order-flow-modules.md.
type OrderModule struct {
	UseCase orderingport.OrderUseCase
}

var _ Module = OrderModule{}

// Register adds POST /v1/orders and GET /v1/orders/:orderId.
func (m OrderModule) Register(v1 fiber.Router) {
	v1.Post("/orders", m.place)
	v1.Get("/orders/:orderId", m.get)
}

type orderItemRequest struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

// placeOrderRequest has no prices and no order id: ordering prices the items
// with the catalog and generates the id (AC-03).
type placeOrderRequest struct {
	Items []orderItemRequest `json:"items"`
}

type orderLineResponse struct {
	ProductID      string `json:"productId"`
	SKU            string `json:"sku"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	UnitPrice      string `json:"unitPrice"`
	UnitPriceMinor int64  `json:"unitPriceMinor"`
	LineTotal      string `json:"lineTotal"`
	LineTotalMinor int64  `json:"lineTotalMinor"`
	Currency       string `json:"currency"`
}

type orderResponse struct {
	OrderID string              `json:"orderId"`
	Status  string              `json:"status"`
	Lines   []orderLineResponse `json:"lines"`
	amountFields
	InvoiceNo string `json:"invoiceNo,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type paymentHandoffResponse struct {
	InvoiceNo    string `json:"invoiceNo"`
	PaymentToken string `json:"paymentToken"`
	CheckoutURL  string `json:"checkoutUrl"`
}

type placedOrderResponse struct {
	orderResponse
	Payment paymentHandoffResponse `json:"payment"`
}

func toOrderResponse(o ordering.Order) orderResponse {
	lines := make([]orderLineResponse, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = orderLineResponse{ProductID: l.ProductID, SKU: l.SKU, Name: l.Name, Quantity: l.Quantity,
			UnitPrice: l.UnitPrice.Decimal(), UnitPriceMinor: l.UnitPrice.Amount,
			LineTotal: l.Total.Decimal(), LineTotalMinor: l.Total.Amount, Currency: string(l.Total.Currency)}
	}
	return orderResponse{OrderID: o.ID, Status: string(o.Status), Lines: lines, amountFields: toAmountFields(o.Amount),
		InvoiceNo: o.InvoiceNo, CreatedAt: o.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: o.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func (m OrderModule) place(c *fiber.Ctx) error {
	var req placeOrderRequest
	if err := decodeStrict(c, &req); err != nil {
		return err
	}
	items := make([]ordering.Item, len(req.Items))
	for i, it := range req.Items {
		items[i] = ordering.Item{ProductID: it.ProductID, Quantity: it.Quantity}
	}
	placed, err := m.UseCase.PlaceOrder(c.UserContext(), orderingport.PlaceOrderCommand{CustomerID: customerIDOf(c), Items: items})
	if err != nil {
		return err
	}
	return c.Status(http.StatusCreated).JSON(placedOrderResponse{
		orderResponse: toOrderResponse(placed.Order),
		Payment: paymentHandoffResponse{InvoiceNo: placed.Payment.InvoiceNo, PaymentToken: placed.Payment.Token,
			CheckoutURL: placed.Payment.CheckoutURL},
	})
}

// get serves the frontend's polling. The id is only used during this call.
func (m OrderModule) get(c *fiber.Ctx) error {
	o, err := m.UseCase.GetOrder(c.UserContext(), customerIDOf(c), c.Params("orderId"))
	if err != nil {
		return err
	}
	return c.JSON(toOrderResponse(o))
}
