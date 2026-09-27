package postgres

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/events"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// outboxModel is the GORM persistence model for the outbox table (ADR-0004).
type outboxModel struct {
	ID          string     `gorm:"column:id;primaryKey"`
	Topic       string     `gorm:"column:topic"`
	MessageKey  string     `gorm:"column:message_key"`
	Payload     []byte     `gorm:"column:payload"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	PublishedAt *time.Time `gorm:"column:published_at"`
}

func (outboxModel) TableName() string { return "outbox" }

// outboxRepo implements port.OutboxRepository on the transaction's *gorm.DB, so an
// event is committed or rolled back together with the state change that raised it.
type outboxRepo struct{ db *gorm.DB }

func (r outboxRepo) AddPaymentStatusChanged(ctx context.Context, e domain.PaymentStatusChanged) error {
	msg, err := events.PaymentStatusChanged(e)
	if err != nil {
		return err
	}
	m := outboxModel{ID: msg.ID, Topic: msg.Topic, MessageKey: msg.Key, Payload: msg.Payload, CreatedAt: msg.CreatedAt}
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert outbox message: %w", err)
	}
	return nil
}

// ClaimPending uses FOR UPDATE SKIP LOCKED so several relays (one per API instance)
// never publish the same message concurrently.
func (r outboxRepo) ClaimPending(ctx context.Context, limit int) ([]port.OutboxMessage, error) {
	var rows []outboxModel
	err := r.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate, Options: clause.LockingOptionsSkipLocked}).
		Where("published_at IS NULL").
		Order("created_at, id").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("select outbox messages: %w", err)
	}
	msgs := make([]port.OutboxMessage, len(rows))
	for i, m := range rows {
		msgs[i] = port.OutboxMessage{ID: m.ID, Topic: m.Topic, Key: m.MessageKey, Payload: m.Payload, CreatedAt: m.CreatedAt}
	}
	return msgs, nil
}

func (r outboxRepo) MarkPublished(ctx context.Context, ids []string, at time.Time) error {
	res := r.db.WithContext(ctx).Model(&outboxModel{}).
		Where("id IN ? AND published_at IS NULL", ids).
		Update("published_at", at)
	if res.Error != nil {
		return fmt.Errorf("mark outbox messages published: %w", res.Error)
	}
	if res.RowsAffected != int64(len(ids)) {
		return fmt.Errorf("marked %d of %d outbox messages: %w", res.RowsAffected, len(ids), domain.ErrConflict)
	}
	return nil
}
