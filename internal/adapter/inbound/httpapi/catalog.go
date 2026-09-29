package httpapi

import (
	"github.com/gofiber/fiber/v2"

	catalogport "github.com/Thapanut/go-engineering-starter/internal/core/catalog/port"
)

// CatalogModule exposes the catalog module's products under /v1.
// See docs/02-specs/order-flow-modules.md.
type CatalogModule struct {
	UseCase catalogport.CatalogUseCase
}

var _ Module = CatalogModule{}

// Register adds GET /v1/products.
func (m CatalogModule) Register(v1 fiber.Router) {
	v1.Get("/products", m.products)
}

type productResponse struct {
	ProductID  string `json:"productId"`
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	Price      string `json:"price"`
	PriceMinor int64  `json:"priceMinor"`
	Currency   string `json:"currency"`
}

type productListResponse struct {
	Products []productResponse `json:"products"`
}

func (m CatalogModule) products(c *fiber.Ctx) error {
	products, err := m.UseCase.ListProducts(c.UserContext())
	if err != nil {
		return err
	}
	out := productListResponse{Products: make([]productResponse, len(products))}
	for i, p := range products {
		out.Products[i] = productResponse{ProductID: p.ID, SKU: p.SKU, Name: p.Name, Price: p.Price.Decimal(),
			PriceMinor: p.Price.Amount, Currency: string(p.Price.Currency)}
	}
	return c.JSON(out)
}
