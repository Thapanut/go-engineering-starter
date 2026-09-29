// Package domain holds the catalog context's entities (ADR-0005). It depends on
// the standard library and the shared kernel only.
package domain

import (
	"regexp"

	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Product is something a customer can buy. The catalog is the owner of its price.
type Product struct {
	ID     string // UUID; stable, never reused
	SKU    string // merchant's product code, e.g. CERAMIC-MUG; unique, may change
	Name   string
	Price  kernel.Money
	Active bool // inactive products are not listed and cannot be ordered
}

var idRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsProductID reports whether s has the form of a product id (lowercase UUID).
func IsProductID(s string) bool { return idRe.MatchString(s) }
