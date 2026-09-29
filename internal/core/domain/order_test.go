package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

var testProducts = map[string]domain.Product{
	"BEANS": {ID: "BEANS", Name: "Beans", Price: thb(45000)},
	"MUG":   {ID: "MUG", Name: "Mug", Price: thb(29000)},
	"USD":   {ID: "USD", Name: "Import", Price: domain.Money{Amount: 100, Currency: "USD"}},
	"HUGE":  {ID: "HUGE", Name: "Huge", Price: thb(math.MaxInt64 / 2)},
}

func TestPriceOrderUsesCatalogPrices(t *testing.T) {
	lines, total, err := domain.PriceOrder([]domain.OrderItem{{"BEANS", 2}, {"MUG", 1}}, testProducts)
	if err != nil {
		t.Fatal(err)
	}
	if total != thb(119000) || len(lines) != 2 || lines[0].Total != thb(90000) || lines[1].Product.Name != "Mug" {
		t.Fatalf("total=%v lines=%+v", total, lines)
	}
}

func TestPriceOrderRejectsInvalidCarts(t *testing.T) {
	tooMany := make([]domain.OrderItem, domain.MaxOrderItems+1)
	for name, items := range map[string][]domain.OrderItem{
		"empty":          nil,
		"too many items": tooMany,
		"no product id":  {{"", 1}},
		"zero quantity":  {{"MUG", 0}},
		"quantity > max": {{"MUG", domain.MaxItemQuantity + 1}},
		"duplicate":      {{"MUG", 1}, {"MUG", 2}},
		"unknown":        {{"NOPE", 1}},
		"mixed currency": {{"MUG", 1}, {"USD", 1}},
		"overflow":       {{"HUGE", 3}},
	} {
		if _, _, err := domain.PriceOrder(items, testProducts); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}
