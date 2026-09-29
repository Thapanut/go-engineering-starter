package memory

import (
	"context"
	"slices"

	catalog "github.com/Thapanut/go-engineering-starter/internal/core/catalog/domain"
	catalogport "github.com/Thapanut/go-engineering-starter/internal/core/catalog/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Products implements the catalog's ProductRepository in memory (STORE=memory
// and tests). It is read-only.
type Products struct{ all []catalog.Product }

var _ catalogport.ProductRepository = Products{}

// NewProducts returns a repository holding products in listing order.
func NewProducts(products ...catalog.Product) Products { return Products{all: slices.Clone(products)} }

// SampleProducts is the demo catalog, the same rows as migrations/dev/20_seed_catalog.sql.
// Synthetic data only.
func SampleProducts() Products {
	thb := func(satang int64) kernel.Money { return kernel.Money{Amount: satang, Currency: kernel.THB} }
	return NewProducts(
		catalog.Product{ID: "04abef6a-166a-45f1-8004-904d9607a857", SKU: "COFFEE-BEANS-250G", Name: "Arabica Coffee Beans 250 g", Price: thb(45000), Active: true},
		catalog.Product{ID: "959e6207-8780-45c0-885b-be846a8f147f", SKU: "CERAMIC-MUG", Name: "Ceramic Mug 350 ml", Price: thb(29000), Active: true},
		catalog.Product{ID: "ff93e10e-646d-4dda-9072-601f73c69c0a", SKU: "POUR-OVER-DRIPPER", Name: "Pour-over Dripper", Price: thb(26000), Active: true},
		catalog.Product{ID: "bdb770cd-3bbe-4fe0-a0c6-2bea0db94c1c", SKU: "HAND-GRINDER", Name: "Hand Grinder (discontinued)", Price: thb(150000), Active: false},
	)
}

// ListActive returns every active product in listing order.
func (r Products) ListActive(ctx context.Context) ([]catalog.Product, error) {
	return r.FindActive(ctx, nil)
}

// FindActive returns active products among ids; nil ids means all active products.
func (r Products) FindActive(ctx context.Context, ids []string) ([]catalog.Product, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []catalog.Product
	for _, p := range r.all {
		if p.Active && (ids == nil || slices.Contains(ids, p.ID)) {
			out = append(out, p)
		}
	}
	return out, nil
}
