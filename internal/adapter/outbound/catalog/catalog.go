// Package catalog implements port.ProductCatalog with a fixed list of sample
// products. It stands in for a product/pricing service until one exists (spec
// payment-checkout, open questions). Prices are in minor units (satang).
package catalog

import (
	"context"
	"slices"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// Static is a read-only in-memory catalog.
type Static struct{ products []domain.Product }

var _ port.ProductCatalog = Static{}

// NewStatic returns a catalog of products; order is kept for listing.
func NewStatic(products ...domain.Product) Static { return Static{products: slices.Clone(products)} }

// Sample returns the demo catalog. Synthetic data only.
func Sample() Static {
	thb := func(satang int64) domain.Money { return domain.Money{Amount: satang, Currency: domain.THB} }
	return NewStatic(
		domain.Product{ID: "COFFEE-BEANS-250G", Name: "Arabica Coffee Beans 250 g", Price: thb(45000)},
		domain.Product{ID: "CERAMIC-MUG", Name: "Ceramic Mug 350 ml", Price: thb(29000)},
		domain.Product{ID: "POUR-OVER-DRIPPER", Name: "Pour-over Dripper", Price: thb(26000)},
	)
}

// ListProducts returns every product.
func (c Static) ListProducts(ctx context.Context) ([]domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return slices.Clone(c.products), nil
}

// FindProducts returns the known products among ids.
func (c Static) FindProducts(ctx context.Context, ids []string) (map[string]domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]domain.Product, len(ids))
	for _, p := range c.products {
		if slices.Contains(ids, p.ID) {
			out[p.ID] = p
		}
	}
	return out, nil
}
