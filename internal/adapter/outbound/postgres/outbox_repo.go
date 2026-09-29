package postgres

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/events"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// outboxModel is the GORM persistence model for the outbox table (ADR-0004).
type outboxModel struct {
	ID          string     `gorm:"column:id;primaryKey"`
	Topic       string     `gorm:"column:topic"`
	MessageKey  string     `gorm:"column:message_key"`
	Payload     []byte     `gorm:"column:payload"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	PublishedAt *time.Time `gorm:"column:published_at"`
	Status      string     `gorm:"column:status"`
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
	m := outboxModel{ID: msg.ID, Topic: msg.Topic, MessageKey: msg.Key, Payload: msg.Payload, CreatedAt: msg.CreatedAt,
		Status: string(port.OutboxPending)}
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
		Where("status = ?", port.OutboxPending).
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
		Where("id IN ? AND status = ?", ids, port.OutboxPending).
		Updates(map[string]any{"published_at": at, "status": port.OutboxPublished})
	if res.Error != nil {
		return fmt.Errorf("mark outbox messages published: %w", res.Error)
	}
	if res.RowsAffected != int64(len(ids)) {
		return fmt.Errorf("marked %d of %d outbox messages: %w", res.RowsAffected, len(ids), domain.ErrConflict)
	}
	return nil
}

// attemptRow is what RecordFailedAttempts reads back per message.
type attemptRow struct {
	ID        string
	Topic     string
	Attempts  int
	Status    string
	LastError string
}

// RecordFailedAttempts updates each claimed message in one statement (the rows are
// already locked by ClaimPending) and parks a permanent failure at maxAttempts.
func (r outboxRepo) RecordFailedAttempts(ctx context.Context, failed []port.FailedAttempt, at time.Time, maxAttempts int) ([]port.AttemptResult, error) {
	out := make([]port.AttemptResult, 0, len(failed))
	for _, f := range failed {
		var row attemptRow
		res := r.db.WithContext(ctx).Raw(`
UPDATE outbox
   SET attempts = attempts + 1, last_error = ?, last_attempt_at = ?,
       status = CASE WHEN ? AND attempts + 1 >= ? THEN 'FAILED' ELSE status END
 WHERE id = ? AND status = 'PENDING'
RETURNING id, topic, attempts, status, last_error`, f.Reason, at, f.Permanent, maxAttempts, f.ID).Scan(&row)
		if res.Error != nil {
			return nil, fmt.Errorf("record outbox attempt: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			return nil, fmt.Errorf("record attempt for outbox message %s: %w", f.ID, domain.ErrConflict)
		}
		out = append(out, port.AttemptResult{ID: row.ID, Topic: row.Topic, Attempts: row.Attempts,
			Parked: row.Status == string(port.OutboxFailed), LastError: row.LastError})
	}
	return out, nil
}

// ListByKey scans the outbox by message_key (no index; diagnostics only).
func (r outboxRepo) ListByKey(ctx context.Context, key string) ([]port.OutboxRecord, error) {
	var rows []outboxModel
	err := r.db.WithContext(ctx).Where("message_key = ?", key).Order("created_at, id").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("select outbox messages by key: %w", err)
	}
	recs := make([]port.OutboxRecord, len(rows))
	for i, m := range rows {
		recs[i] = port.OutboxRecord{Status: port.OutboxStatus(m.Status), Message: port.OutboxMessage{
			ID: m.ID, Topic: m.Topic, Key: m.MessageKey, Payload: m.Payload, CreatedAt: m.CreatedAt,
		}}
		if m.PublishedAt != nil {
			recs[i].PublishedAt = *m.PublishedAt
		}
	}
	return recs, nil
}
