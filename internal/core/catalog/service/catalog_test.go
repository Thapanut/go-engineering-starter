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
		if !p.Active || p.ID == "bdb770cd-3bbe-4fe0-a0c6-2bea0db94c1c" {
			t.Fatalf("inactive product listed: %+v", p)
		}
	}
}

func TestFindProductsSkipsUnknownAndInactive(t *testing.T) {
	svc := service.NewCatalogService(memory.SampleProducts())
	found, err := svc.FindProducts(context.Background(), []string{"959e6207-8780-45c0-885b-be846a8f147f", "bdb770cd-3bbe-4fe0-a0c6-2bea0db94c1c", "NOPE"})
	if err != nil || len(found) != 1 || found["959e6207-8780-45c0-885b-be846a8f147f"].Price.Amount != 29000 {
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
	if _, err := svc.FindProducts(context.Background(), []string{"00000000-0000-4000-8000-000000000001"}); !errors.Is(err, errBoom) {
		t.Fatalf("find err = %v", err)
	}
}
