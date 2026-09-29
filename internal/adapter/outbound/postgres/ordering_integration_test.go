//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/catalogclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/paymentclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	catalogservice "github.com/Thapanut/go-engineering-starter/internal/core/catalog/service"
	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	orderingservice "github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	paymentservice "github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
)

// Spec order-flow-modules AC-02 on PostgreSQL: each module writes only its own schema.
func TestIntegration_PlaceOrderAcrossModules(t *testing.T) {
	_, db := setupPayments(t)
	checkout := paymentservice.NewCheckoutService(NewTxManager(db), twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	orders := orderingservice.NewOrderService(NewOrderingTxManager(db),
		catalogclient.Client{Catalog: catalogservice.NewCatalogService(NewProductRepository(db))},
		paymentclient.Client{Payments: checkout}, system.Clock{}, system.UUIDGenerator{})
	ctx := context.Background()
	placed, err := orders.PlaceOrder(ctx, orderingport.PlaceOrderCommand{CustomerID: "cust-it",
		Items: []ordering.Item{{ProductID: "COFFEE-BEANS-250G", Quantity: 2}, {ProductID: "CERAMIC-MUG", Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := orders.GetOrder(ctx, "cust-it", placed.Order.ID)
	if err != nil || got.Status != ordering.AwaitingPayment || got.Amount.Amount != 119000 || len(got.Lines) != 2 ||
		got.Lines[1].Name != "Ceramic Mug 350 ml" || got.InvoiceNo != placed.Payment.InvoiceNo {
		t.Fatalf("order = %+v, err = %v", got, err)
	}
	var pay paymentModel
	if err := db.Where("invoice_no = ?", placed.Payment.InvoiceNo).Take(&pay).Error; err != nil {
		t.Fatal(err)
	}
	if pay.OrderID != placed.Order.ID || pay.Amount != 119000 || pay.CustomerID != "cust-it" || pay.Status != "PENDING" {
		t.Fatalf("payment = %+v", pay)
	}
}
