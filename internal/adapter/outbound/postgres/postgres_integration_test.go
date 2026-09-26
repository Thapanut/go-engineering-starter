//go:build integration

// Integration tests run the real service against PostgreSQL, proving the
// concurrency guarantees that the in-memory adapter cannot (spec AC-11..AC-13).
// Run: make test-integration   (needs TEST_DB_DSN; see Makefile)
package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/postgres"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
)

// Synthetic test data only.
const (
	alice    = "it-alice"
	bob      = "it-bob"
	accAlice = "11111111-1111-4111-8111-111111111111"
	accBob   = "22222222-2222-4222-8222-222222222222"
)

func setup(t *testing.T) (*service.TransferService, *pgxpool.Pool) {
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
	for _, f := range []string{"../../../../migrations/0001_init.down.sql", "../../../../migrations/0001_init.up.sql"} {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO accounts (id, customer_id, currency, balance, status) VALUES
		($1, $2, 'THB', 10000, 'ACTIVE'), ($3, $4, 'THB', 5000, 'ACTIVE')`, accAlice, alice, accBob, bob)
	if err != nil {
		t.Fatal(err)
	}
	return service.NewTransferService(postgres.NewTxManager(pool), system.Clock{}, system.UUIDGenerator{}), pool
}

func req(key, from, to string, amount int64) domain.TransferRequest {
	return domain.TransferRequest{CustomerID: alice, IdempotencyKey: key, FromAccountID: from, ToAccountID: to,
		Amount: domain.Money{Amount: amount, Currency: domain.THB}}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegration_AC01_AC02_AC11_TransferReplayAudit(t *testing.T) {
	svc, pool := setup(t)
	ctx := context.Background()
	first, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 2_500), "trace-it")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 2_500), "trace-it-2")
	if err != nil || !second.Replayed || second.Transfer.ID != first.Transfer.ID {
		t.Fatalf("replay = %+v, %v", second, err)
	}
	if b := count(t, pool, `SELECT balance FROM accounts WHERE id = $1`, accAlice); b != 7_500 {
		t.Errorf("alice balance = %d, want 7500", b)
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND trace_id = 'trace-it'
		AND action = 'TRANSFER_CREATED' AND (before->'balances'->>'from')::bigint = 10000
		AND (after->'balances'->>'from')::bigint = 7500`, first.Transfer.ID); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Error("audit_log accepted DELETE; it must be append-only")
	}
	got, err := svc.GetTransfer(ctx, alice, first.Transfer.ID)
	if err != nil || got.Amount.Amount != 2_500 || got.RequestHash != first.Transfer.RequestHash {
		t.Fatalf("GetTransfer = %+v, %v", got, err)
	}
}

func TestIntegration_AC04_RollbackOnBusinessError(t *testing.T) {
	svc, pool := setup(t)
	_, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 10_001), "t")
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM transfers`); n != 0 {
		t.Errorf("transfers = %d, want 0 (rolled back)", n)
	}
}

func TestIntegration_AC12_ConcurrentDebitsNeverOverdraw(t *testing.T) {
	svc, pool := setup(t)
	var ok atomic.Int64
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Alternate directions to also prove there is no deadlock (ordered locking).
			from, to, cust := accAlice, accBob, alice
			if i%5 == 0 {
				from, to, cust = accBob, accAlice, bob
			}
			r := req(fmt.Sprintf("k%d", i), from, to, 1_000)
			r.CustomerID = cust
			if _, err := svc.CreateTransfer(context.Background(), r, "t"); err == nil {
				ok.Add(1)
			} else if !errors.Is(err, domain.ErrInsufficientFunds) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	total := count(t, pool, `SELECT sum(balance) FROM accounts`)
	if total != 15_000 {
		t.Errorf("sum of balances = %d, want 15000 (money created or destroyed)", total)
	}
	if n := count(t, pool, `SELECT count(*) FROM transfers`); n != ok.Load() {
		t.Errorf("transfer rows = %d, successes = %d", n, ok.Load())
	}
	if neg := count(t, pool, `SELECT count(*) FROM accounts WHERE balance < 0`); neg != 0 {
		t.Error("negative balance")
	}
}

func TestIntegration_AC13_ConcurrentSameKeyDebitsOnce(t *testing.T) {
	svc, pool := setup(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]int{}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.CreateTransfer(context.Background(), req("same-key", accAlice, accBob, 1_000), "t")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			mu.Lock()
			ids[res.Transfer.ID]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 1 {
		t.Fatalf("distinct transfer ids = %v, want exactly 1", ids)
	}
	if b := count(t, pool, `SELECT balance FROM accounts WHERE id = $1`, accAlice); b != 9_000 {
		t.Errorf("alice balance = %d, want 9000 (single debit)", b)
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_log`); n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}
