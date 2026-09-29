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
	if all[0].ID != "CERAMIC-MUG" || all[0].Price.Amount != 29000 || all[0].Price.Currency != "THB" {
		t.Fatalf("first (by id) = %+v", all[0])
	}
	found, err := repo.FindActive(context.Background(), []string{"COFFEE-BEANS-250G", "HAND-GRINDER", "NOPE"})
	if err != nil || len(found) != 1 || found[0].Name != "Arabica Coffee Beans 250 g" {
		t.Fatalf("found = %+v, err = %v", found, err)
	}
}
