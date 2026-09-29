// Package service implements the catalog module's use cases.
package service

import (
	"context"
	"fmt"

	"github.com/Thapanut/go-engineering-starter/internal/core/catalog/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/catalog/port"
)

// CatalogService implements port.CatalogUseCase.
// See docs/02-specs/order-flow-modules.md.
type CatalogService struct{ products port.ProductRepository }

var _ port.CatalogUseCase = (*CatalogService)(nil)

// NewCatalogService wires the service to its repository.
func NewCatalogService(products port.ProductRepository) *CatalogService {
	return &CatalogService{products: products}
}

// ListProducts returns the active products (AC-01).
func (s *CatalogService) ListProducts(ctx context.Context) ([]domain.Product, error) {
	products, err := s.products.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}

// FindProducts returns the active products among ids.
func (s *CatalogService) FindProducts(ctx context.Context, ids []string) (map[string]domain.Product, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if domain.IsProductID(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return map[string]domain.Product{}, nil
	}
	found, err := s.products.FindActive(ctx, valid)
	if err != nil {
		return nil, fmt.Errorf("find products: %w", err)
	}
	out := make(map[string]domain.Product, len(found))
	for _, p := range found {
		out[p.ID] = p
	}
	return out, nil
}
