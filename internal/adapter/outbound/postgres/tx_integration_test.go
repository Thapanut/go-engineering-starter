//go:build integration

// Integration tests run against a real PostgreSQL.
// Run: make test-integration   (needs TEST_DB_DSN; see Makefile)
package postgres

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

func setup(t *testing.T) (*TxManager, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Fatal("TEST_DB_DSN is not set; run via `make test-integration`")
	}
	db, err := Open(context.Background(), dsn, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(db) })
	if err := db.Exec(`DROP TABLE IF EXISTS tx_probe; CREATE TABLE tx_probe (v int NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS tx_probe`) })
	return NewTxManager(db), db
}

func rows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Table("tx_probe").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func insert(tx *gorm.DB) error { return tx.Exec(`INSERT INTO tx_probe (v) VALUES (?)`, 1).Error }

func TestIntegration_TxCommitsOnSuccess(t *testing.T) {
	m, db := setup(t)
	if err := m.inTx(context.Background(), insert); err != nil {
		t.Fatal(err)
	}
	if n := rows(t, db); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestIntegration_TxRollsBackOnError(t *testing.T) {
	m, db := setup(t)
	boom := errors.New("boom")
	err := m.inTx(context.Background(), func(tx *gorm.DB) error {
		if err := insert(tx); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := rows(t, db); n != 0 {
		t.Fatalf("rows = %d, want 0 (rolled back)", n)
	}
}

func TestIntegration_TxRollsBackOnPanic(t *testing.T) {
	m, db := setup(t)
	func() {
		defer func() { _ = recover() }()
		_ = m.inTx(context.Background(), func(tx *gorm.DB) error {
			if err := insert(tx); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if n := rows(t, db); n != 0 {
		t.Fatalf("rows = %d, want 0 (rolled back)", n)
	}
}

func TestIntegration_WithinTxPropagatesError(t *testing.T) {
	m, _ := setup(t)
	boom := errors.New("boom")
	err := m.WithinTx(context.Background(), func(context.Context, port.Repositories) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestIntegration_UniqueViolationIsTranslated(t *testing.T) {
	_, db := setup(t)
	if err := db.Exec(`ALTER TABLE tx_probe ADD CONSTRAINT uq_tx_probe_v UNIQUE (v)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := insert(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("tx_probe").Create(map[string]any{"v": 1}).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("err = %v, want gorm.ErrDuplicatedKey", err)
	}
}
