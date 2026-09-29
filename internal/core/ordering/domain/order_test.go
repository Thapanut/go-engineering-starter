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

// Product ids are UUIDs; the SKUs are what a person reads.
const (
	beans = "00000000-0000-4000-8000-000000000001"
	mug   = "00000000-0000-4000-8000-000000000002"
	usd   = "00000000-0000-4000-8000-000000000003"
	huge  = "00000000-0000-4000-8000-000000000004"
	nope  = "00000000-0000-4000-8000-0000000000ff"
)

var products = map[string]domain.PricedProduct{
	beans: {ID: beans, SKU: "BEANS", Name: "Beans", Price: thb(45000)},
	mug:   {ID: mug, SKU: "MUG", Name: "Mug", Price: thb(29000)},
	usd:   {ID: usd, SKU: "USD", Name: "Import", Price: kernel.Money{Amount: 100, Currency: "USD"}},
	huge:  {ID: huge, SKU: "HUGE", Name: "Huge", Price: thb(math.MaxInt64 / 2)},
}

func TestOrderFlowAC02_NewOrderPricesTheCart(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	o, err := domain.NewOrder("o1", "cust", []domain.Item{{beans, 2}, {mug, 1}}, products, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != domain.AwaitingPayment || o.Amount != thb(119000) || len(o.Lines) != 2 || o.InvoiceNo != "" ||
		o.Lines[0] != (domain.Line{No: 1, ProductID: beans, SKU: "BEANS", Name: "Beans", UnitPrice: thb(45000), Quantity: 2, Total: thb(90000)}) ||
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
		"zero quantity":  {{mug, 0}},
		"quantity > max": {{mug, domain.MaxQuantity + 1}},
		"duplicate":      {{mug, 1}, {mug, 2}},
		"not a uuid":     {{"MUG", 1}},
		"unknown":        {{nope, 1}},
		"mixed currency": {{mug, 1}, {usd, 1}},
		"overflow":       {{huge, 3}},
	} {
		if _, err := domain.NewOrder("o1", "cust", items, products, time.Now()); !errors.Is(err, kernel.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	if _, err := domain.NewOrder("o1", "", []domain.Item{{mug, 1}}, products, time.Now()); !errors.Is(err, kernel.ErrValidation) {
		t.Errorf("no customer: err = %v", err)
	}
}
