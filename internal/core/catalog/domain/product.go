// Package domain holds the catalog context's entities (ADR-0005). It depends on
// the standard library and the shared kernel only.
package domain

import "github.com/Thapanut/go-engineering-starter/internal/kernel"

// Product is something a customer can buy. The catalog is the owner of its price.
type Product struct {
	ID     string
	Name   string
	Price  kernel.Money
	Active bool // inactive products are not listed and cannot be ordered
}
