package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// paymentModel is the GORM persistence model for the payments table. It stays
// inside this adapter; the domain never sees gorm tags.
type paymentModel struct {
	ID           string    `gorm:"column:id;primaryKey"`
	InvoiceNo    string    `gorm:"column:invoice_no"`
	OrderID      string    `gorm:"column:order_id"`
	CustomerID   string    `gorm:"column:customer_id"`
	Amount       int64     `gorm:"column:amount"`
	Currency     string    `gorm:"column:currency"`
	Status       string    `gorm:"column:status"`
	ProviderRef  string    `gorm:"column:provider_ref"`
	ProviderCode string    `gorm:"column:provider_code"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (paymentModel) TableName() string { return "payments" }

func toPaymentModel(p domain.Payment) paymentModel {
	return paymentModel{
		ID: p.ID, InvoiceNo: p.InvoiceNo, OrderID: p.OrderID, CustomerID: p.CustomerID, Amount: p.Amount.Amount, Currency: string(p.Amount.Currency),
		Status: string(p.Status), ProviderRef: p.ProviderRef, ProviderCode: p.ProviderCode,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

func (m paymentModel) toDomain() domain.Payment {
	return domain.Payment{
		ID: m.ID, InvoiceNo: m.InvoiceNo, OrderID: m.OrderID, CustomerID: m.CustomerID,
		Amount:      domain.Money{Amount: m.Amount, Currency: domain.Currency(m.Currency)},
		Status:      domain.PaymentStatus(m.Status),
		ProviderRef: m.ProviderRef, ProviderCode: m.ProviderCode,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

// paymentRepo implements port.PaymentRepository on the transaction's *gorm.DB.
type paymentRepo struct{ db *gorm.DB }

func (r paymentRepo) Create(ctx context.Context, p domain.Payment) error {
	now := time.Now().UTC()
	m := toPaymentModel(p)
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = now
	}
	err := r.db.WithContext(ctx).Create(&m).Error
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("payment invoice no already exists: %w", domain.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("insert payment: %w", err)
	}
	return nil
}

// GetByInvoiceNoForUpdate takes a row lock (SELECT … FOR UPDATE) so concurrent
// deliveries of the same notification are serialized (spec AC-10).
func (r paymentRepo) GetByInvoiceNoForUpdate(ctx context.Context, invoiceNo string) (domain.Payment, error) {
	return takeByInvoiceNo(r.db.WithContext(ctx).Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}), invoiceNo)
}

// GetByInvoiceNo is a plain read for status queries; it takes no lock.
func (r paymentRepo) GetByInvoiceNo(ctx context.Context, invoiceNo string) (domain.Payment, error) {
	return takeByInvoiceNo(r.db.WithContext(ctx), invoiceNo)
}

func takeByInvoiceNo(q *gorm.DB, invoiceNo string) (domain.Payment, error) {
	var m paymentModel
	err := q.Where("invoice_no = ?", invoiceNo).Take(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Payment{}, fmt.Errorf("payment: %w", domain.ErrNotFound)
	}
	if err != nil {
		return domain.Payment{}, fmt.Errorf("select payment: %w", err)
	}
	return m.toDomain(), nil
}

// UpdateOutcome updates only a row that is still PENDING, a second guard next to
// the row lock and the domain's terminal-state rule.
func (r paymentRepo) UpdateOutcome(ctx context.Context, p domain.Payment) error {
	res := r.db.WithContext(ctx).Model(&paymentModel{}).
		Where("id = ? AND status = ?", p.ID, string(domain.PaymentPending)).
		Updates(map[string]any{
			"status":        string(p.Status),
			"provider_ref":  p.ProviderRef,
			"provider_code": p.ProviderCode,
			"updated_at":    p.UpdatedAt,
		})
	if res.Error != nil {
		return fmt.Errorf("update payment: %w", res.Error)
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("payment is no longer pending: %w", domain.ErrConflict)
	}
	return nil
}
