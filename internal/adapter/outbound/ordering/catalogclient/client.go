// Package catalogclient implements the ordering module's PriceSource by calling
// the catalog module's inbound port. It is ordering's anti-corruption layer
// towards catalog (ADR-0005): today an in-process call, later an HTTP client,
// without changing ordering's core.
package catalogclient

import (
	"context"

	catalogport "github.com/Thapanut/go-engineering-starter/internal/core/catalog/port"
	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
)

// Client adapts catalogport.CatalogUseCase to orderingport.PriceSource.
type Client struct{ Catalog catalogport.CatalogUseCase }

var _ orderingport.PriceSource = Client{}

// FindProducts returns the orderable products among ids with their current prices.
func (c Client) FindProducts(ctx context.Context, ids []string) (map[string]ordering.PricedProduct, error) {
	found, err := c.Catalog.FindProducts(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ordering.PricedProduct, len(found))
	for id, p := range found {
		out[id] = ordering.PricedProduct{ID: p.ID, SKU: p.SKU, Name: p.Name, Price: p.Price}
	}
	return out, nil
}
