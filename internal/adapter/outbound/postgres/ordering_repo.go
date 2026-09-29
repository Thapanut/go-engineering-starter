package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// GORM models for the ordering schema. Only ordering's repositories touch it (ADR-0005).
type orderModel struct {
	ID         string    `gorm:"column:id;primaryKey"`
	CustomerID string    `gorm:"column:customer_id"`
	Status     string    `gorm:"column:status"`
	Amount     int64     `gorm:"column:amount"`
	Currency   string    `gorm:"column:currency"`
	InvoiceNo  *string   `gorm:"column:invoice_no"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (orderModel) TableName() string { return "ordering.orders" }

type orderLineModel struct {
	OrderID   string `gorm:"column:order_id;primaryKey"`
	LineNo    int    `gorm:"column:line_no;primaryKey"`
	ProductID string `gorm:"column:product_id"`
	Name      string `gorm:"column:name"`
	UnitPrice int64  `gorm:"column:unit_price"`
	Quantity  int    `gorm:"column:quantity"`
	LineTotal int64  `gorm:"column:line_total"`
	Currency  string `gorm:"column:currency"`
}

func (orderLineModel) TableName() string { return "ordering.order_lines" }

// OrderingTxManager implements the ordering module's TxManager on the shared pool.
// It never joins a payment or catalog transaction.
type OrderingTxManager struct{ db *gorm.DB }

var _ orderingport.TxManager = (*OrderingTxManager)(nil)

// NewOrderingTxManager returns a TxManager for the ordering schema.
func NewOrderingTxManager(db *gorm.DB) *OrderingTxManager { return &OrderingTxManager{db: db} }

// WithinTx commits if fn succeeds and rolls back otherwise (including on panic).
func (m *OrderingTxManager) WithinTx(ctx context.Context, fn func(context.Context, orderingport.Repositories) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(ctx, orderingport.Repositories{Orders: orderRepo{db: tx}})
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

type orderRepo struct{ db *gorm.DB }

func (r orderRepo) Create(ctx context.Context, o ordering.Order) error {
	m := orderModel{ID: o.ID, CustomerID: o.CustomerID, Status: string(o.Status), Amount: o.Amount.Amount,
		Currency: string(o.Amount.Currency), CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}
	if o.InvoiceNo != "" {
		m.InvoiceNo = &o.InvoiceNo
	}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	lines := make([]orderLineModel, len(o.Lines))
	for i, l := range o.Lines {
		lines[i] = orderLineModel{OrderID: o.ID, LineNo: l.No, ProductID: l.ProductID, Name: l.Name,
			UnitPrice: l.UnitPrice.Amount, Quantity: l.Quantity, LineTotal: l.Total.Amount, Currency: string(l.Total.Currency)}
	}
	if err := r.db.WithContext(ctx).Create(&lines).Error; err != nil {
		return fmt.Errorf("insert order lines: %w", err)
	}
	return nil
}

func (r orderRepo) Get(ctx context.Context, id string) (ordering.Order, error) {
	var m orderModel
	err := r.db.WithContext(ctx).Where("id = ?", id).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ordering.Order{}, fmt.Errorf("order: %w", kernel.ErrNotFound)
	}
	if err != nil {
		return ordering.Order{}, fmt.Errorf("select order: %w", err)
	}
	var lines []orderLineModel
	if err := r.db.WithContext(ctx).Where("order_id = ?", id).Order("line_no").Find(&lines).Error; err != nil {
		return ordering.Order{}, fmt.Errorf("select order lines: %w", err)
	}
	return m.toDomain(lines), nil
}

func (r orderRepo) SetInvoiceNo(ctx context.Context, id, invoiceNo string, at time.Time) error {
	res := r.db.WithContext(ctx).Model(&orderModel{}).Where("id = ?", id).
		Updates(map[string]any{"invoice_no": invoiceNo, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("update order invoice: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("order: %w", kernel.ErrNotFound)
	}
	return nil
}

func (m orderModel) toDomain(lines []orderLineModel) ordering.Order {
	money := func(amount int64, ccy string) kernel.Money {
		return kernel.Money{Amount: amount, Currency: kernel.Currency(ccy)}
	}
	o := ordering.Order{ID: m.ID, CustomerID: m.CustomerID, Status: ordering.Status(m.Status),
		Amount: money(m.Amount, m.Currency), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
	if m.InvoiceNo != nil {
		o.InvoiceNo = *m.InvoiceNo
	}
	o.Lines = make([]ordering.Line, len(lines))
	for i, l := range lines {
		o.Lines[i] = ordering.Line{No: l.LineNo, ProductID: l.ProductID, Name: l.Name, Quantity: l.Quantity,
			UnitPrice: money(l.UnitPrice, l.Currency), Total: money(l.LineTotal, l.Currency)}
	}
	return o
}
