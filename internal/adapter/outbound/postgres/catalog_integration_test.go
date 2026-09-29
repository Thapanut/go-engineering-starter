//go:build integration

package postgres

import (
	"context"
	"testing"
)

// Spec order-flow-modules AC-01 on PostgreSQL: catalog.products with the dev seed.
func TestIntegration_CatalogReadsActiveProducts(t *testing.T) {
	_, db := setupPayments(t) // applies every migration and the catalog seed
	repo := NewProductRepository(db)
	all, err := repo.ListActive(context.Background())
	if err != nil || len(all) != 3 {
		t.Fatalf("active = %+v, err = %v", all, err)
	}
	if all[0].ID != "959e6207-8780-45c0-885b-be846a8f147f" || all[0].SKU != "CERAMIC-MUG" ||
		all[0].Price.Amount != 29000 || all[0].Price.Currency != "THB" {
		t.Fatalf("first (by SKU) = %+v", all[0])
	}
	found, err := repo.FindActive(context.Background(), []string{"04abef6a-166a-45f1-8004-904d9607a857", "bdb770cd-3bbe-4fe0-a0c6-2bea0db94c1c", "00000000-0000-4000-8000-0000000000ff"})
	if err != nil || len(found) != 1 || found[0].Name != "Arabica Coffee Beans 250 g" {
		t.Fatalf("found = %+v, err = %v", found, err)
	}
}
