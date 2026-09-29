package domain_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

func thb(v int64) kernel.Money { return kernel.Money{Amount: v, Currency: kernel.THB} }

var products = map[string]domain.PricedProduct{
	"BEANS": {ID: "BEANS", Name: "Beans", Price: thb(45000)},
	"MUG":   {ID: "MUG", Name: "Mug", Price: thb(29000)},
	"USD":   {ID: "USD", Name: "Import", Price: kernel.Money{Amount: 100, Currency: "USD"}},
	"HUGE":  {ID: "HUGE", Name: "Huge", Price: thb(math.MaxInt64 / 2)},
}

func TestOrderFlowAC02_NewOrderPricesTheCart(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	o, err := domain.NewOrder("o1", "cust", []domain.Item{{"BEANS", 2}, {"MUG", 1}}, products, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != domain.AwaitingPayment || o.Amount != thb(119000) || len(o.Lines) != 2 || o.InvoiceNo != "" ||
		o.Lines[0] != (domain.Line{No: 1, ProductID: "BEANS", Name: "Beans", UnitPrice: thb(45000), Quantity: 2, Total: thb(90000)}) ||
		!o.CreatedAt.Equal(now) {
		t.Fatalf("order = %+v", o)
	}
}

func TestOrderFlowAC03_NewOrderRejectsInvalidCarts(t *testing.T) {
	tooMany := make([]domain.Item, domain.MaxItems+1)
	for name, items := range map[string][]domain.Item{
		"empty":          nil,
		"too many":       tooMany,
		"no product id":  {{"", 1}},
		"zero quantity":  {{"MUG", 0}},
		"quantity > max": {{"MUG", domain.MaxQuantity + 1}},
		"duplicate":      {{"MUG", 1}, {"MUG", 2}},
		"unknown":        {{"NOPE", 1}},
		"mixed currency": {{"MUG", 1}, {"USD", 1}},
		"overflow":       {{"HUGE", 3}},
	} {
		if _, err := domain.NewOrder("o1", "cust", items, products, time.Now()); !errors.Is(err, kernel.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	if _, err := domain.NewOrder("o1", "", []domain.Item{{"MUG", 1}}, products, time.Now()); !errors.Is(err, kernel.ErrValidation) {
		t.Errorf("no customer: err = %v", err)
	}
}
