package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/core/catalog/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/catalog/service"
)

func TestOrderFlowAC01_ListsActiveProductsOnly(t *testing.T) {
	svc := service.NewCatalogService(memory.SampleProducts())
	products, err := svc.ListProducts(context.Background())
	if err != nil || len(products) != 3 {
		t.Fatalf("products = %+v, err = %v", products, err)
	}
	for _, p := range products {
		if !p.Active || p.ID == "HAND-GRINDER" {
			t.Fatalf("inactive product listed: %+v", p)
		}
	}
}

func TestFindProductsSkipsUnknownAndInactive(t *testing.T) {
	svc := service.NewCatalogService(memory.SampleProducts())
	found, err := svc.FindProducts(context.Background(), []string{"CERAMIC-MUG", "HAND-GRINDER", "NOPE"})
	if err != nil || len(found) != 1 || found["CERAMIC-MUG"].Price.Amount != 29000 {
		t.Fatalf("found = %+v, err = %v", found, err)
	}
	if none, err := svc.FindProducts(context.Background(), nil); err != nil || len(none) != 0 {
		t.Fatalf("empty ids: %+v, %v", none, err)
	}
}

type failingRepo struct{}

func (failingRepo) ListActive(context.Context) ([]domain.Product, error) { return nil, errBoom }
func (failingRepo) FindActive(context.Context, []string) ([]domain.Product, error) {
	return nil, errBoom
}

var errBoom = errors.New("db down")

func TestCatalogWrapsRepositoryErrors(t *testing.T) {
	svc := service.NewCatalogService(failingRepo{})
	if _, err := svc.ListProducts(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("list err = %v", err)
	}
	if _, err := svc.FindProducts(context.Background(), []string{"X"}); !errors.Is(err, errBoom) {
		t.Fatalf("find err = %v", err)
	}
}
