//go:build integration

// Integration tests run against a real PostgreSQL.
// Run: make test-integration   (needs TEST_DB_DSN; see Makefile)
package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

func setup(t *testing.T) (*TxManager, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Fatal("TEST_DB_DSN is not set; run via `make test-integration`")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS tx_probe; CREATE TABLE tx_probe (v int NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS tx_probe`) })
	return NewTxManager(pool), pool
}

func rows(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tx_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegration_TxCommitsOnSuccess(t *testing.T) {
	m, pool := setup(t)
	err := m.inTx(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `INSERT INTO tx_probe (v) VALUES ($1)`, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := rows(t, pool); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestIntegration_TxRollsBackOnError(t *testing.T) {
	m, pool := setup(t)
	boom := errors.New("boom")
	err := m.inTx(context.Background(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), `INSERT INTO tx_probe (v) VALUES ($1)`, 1); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := rows(t, pool); n != 0 {
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
