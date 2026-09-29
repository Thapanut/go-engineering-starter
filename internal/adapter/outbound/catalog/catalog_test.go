package catalog

import (
	"context"
	"testing"
)

func TestSampleCatalog(t *testing.T) {
	c := Sample()
	all, err := c.ListProducts(context.Background())
	if err != nil || len(all) != 3 {
		t.Fatalf("products = %+v, err = %v", all, err)
	}
	all[0].Name = "mutated"
	if again, _ := c.ListProducts(context.Background()); again[0].Name == "mutated" {
		t.Fatal("ListProducts exposes internal state")
	}
	found, err := c.FindProducts(context.Background(), []string{"CERAMIC-MUG", "NOPE"})
	if err != nil || len(found) != 1 || found["CERAMIC-MUG"].Price.Amount != 29000 {
		t.Fatalf("found = %+v, err = %v", found, err)
	}
}

func TestCatalogHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Sample().ListProducts(ctx); err == nil {
		t.Fatal("want error")
	}
	if _, err := Sample().FindProducts(ctx, nil); err == nil {
		t.Fatal("want error")
	}
}
