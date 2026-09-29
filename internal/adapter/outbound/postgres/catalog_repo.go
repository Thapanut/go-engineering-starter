package postgres

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	catalog "github.com/Thapanut/go-engineering-starter/internal/core/catalog/domain"
	catalogport "github.com/Thapanut/go-engineering-starter/internal/core/catalog/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// productModel is the GORM model for catalog.products. Only the catalog module's
// repository touches the catalog schema (ADR-0005).
type productModel struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Name      string    `gorm:"column:name"`
	Price     int64     `gorm:"column:price_minor"`
	Currency  string    `gorm:"column:currency"`
	Active    bool      `gorm:"column:active"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (productModel) TableName() string { return "catalog.products" }

func (m productModel) toDomain() catalog.Product {
	return catalog.Product{ID: m.ID, Name: m.Name, Active: m.Active,
		Price: kernel.Money{Amount: m.Price, Currency: kernel.Currency(m.Currency)}}
}

// ProductRepository implements the catalog's ProductRepository. Reads need no
// transaction, so it holds the pool directly.
type ProductRepository struct{ db *gorm.DB }

var _ catalogport.ProductRepository = (*ProductRepository)(nil)

// NewProductRepository returns a repository on db.
func NewProductRepository(db *gorm.DB) *ProductRepository { return &ProductRepository{db: db} }

// ListActive returns active products ordered by id.
func (r *ProductRepository) ListActive(ctx context.Context) ([]catalog.Product, error) {
	return r.find(r.db.WithContext(ctx).Where("active"))
}

// FindActive returns the active products among ids.
func (r *ProductRepository) FindActive(ctx context.Context, ids []string) ([]catalog.Product, error) {
	return r.find(r.db.WithContext(ctx).Where("active AND id IN ?", ids))
}

func (r *ProductRepository) find(q *gorm.DB) ([]catalog.Product, error) {
	var rows []productModel
	if err := q.Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("select products: %w", err)
	}
	out := make([]catalog.Product, len(rows))
	for i, m := range rows {
		out[i] = m.toDomain()
	}
	return out, nil
}
