// Package postgres implements the outbound ports on PostgreSQL with GORM (ADR-0003).
//
// Rules for repositories in this package:
//   - Always use the *gorm.DB handed to newRepositories (it is bound to the tx).
//   - Parameterized queries only: Where("id = ?", id), never fmt.Sprintf into SQL.
//   - Use GORM models local to this package and map to/from domain types; domain
//     types carry no gorm tags.
//   - Lock rows explicitly when a use case needs it:
//     tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id")…
//   - Schema changes go through migrations/, never AutoMigrate.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// Pool settings; tune per service and database limits.
const (
	maxOpenConns    = 20
	maxIdleConns    = 10
	connMaxLifetime = 30 * time.Minute
	slowQuery       = 200 * time.Millisecond
)

// Open connects to PostgreSQL and verifies the connection.
// SQL is logged only when slow or failing, with parameters redacted (no PII in logs).
func Open(ctx context.Context, dsn string, log *slog.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(gormpg.Open(dsn), &gorm.Config{
		TranslateError: true, // unique violations → gorm.ErrDuplicatedKey
		Logger: gormlogger.New(slog.NewLogLogger(log.Handler(), slog.LevelWarn), gormlogger.Config{
			SlowThreshold:             slowQuery,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	if err := Ping(ctx, db); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Ping checks that the database is reachable.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("postgres pool: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func Close(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// TxManager implements port.TxManager with GORM transactions.
type TxManager struct{ db *gorm.DB }

var _ port.TxManager = (*TxManager)(nil)

// NewTxManager returns a TxManager using db.
func NewTxManager(db *gorm.DB) *TxManager { return &TxManager{db: db} }

// WithinTx commits if fn succeeds and rolls back otherwise (including on panic).
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context, r port.Repositories) error) error {
	return m.inTx(ctx, func(tx *gorm.DB) error { return fn(ctx, newRepositories(tx)) })
}

// newRepositories binds every repository to tx. Add one line per repository port.
func newRepositories(tx *gorm.DB) port.Repositories {
	return port.Repositories{Payments: paymentRepo{db: tx}, Outbox: outboxRepo{db: tx}}
}

func (m *TxManager) inTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return m.db.WithContext(ctx).Transaction(fn, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
