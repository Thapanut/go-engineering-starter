// Package port defines the catalog module's inbound and outbound interfaces.
// Other modules use the catalog only through CatalogUseCase (ADR-0005).
package port

import (
	"context"

	"github.com/Thapanut/go-engineering-starter/internal/core/catalog/domain"
)

// CatalogUseCase lists and looks up products that can be ordered (inbound port).
type CatalogUseCase interface {
	// ListProducts returns the active products.
	ListProducts(ctx context.Context) ([]domain.Product, error)
	// FindProducts returns the active products among ids, keyed by id. Unknown
	// and inactive ids are absent.
	FindProducts(ctx context.Context, ids []string) (map[string]domain.Product, error)
}

// ProductRepository reads products from the catalog's store (outbound port).
type ProductRepository interface {
	ListActive(ctx context.Context) ([]domain.Product, error)
	FindActive(ctx context.Context, ids []string) ([]domain.Product, error)
}
