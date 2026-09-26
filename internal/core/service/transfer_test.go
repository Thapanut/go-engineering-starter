package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
)

// Synthetic test data only.
const (
	alice    = "cust-alice"
	bob      = "cust-bob"
	accAlice = "11111111-1111-4111-8111-111111111111"
	accBob   = "22222222-2222-4222-8222-222222222222"
	accOther = "33333333-3333-4333-8333-333333333333" // owned by bob
	missing  = "99999999-9999-4999-8999-999999999999"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }

type seqIDs struct{ n atomic.Int64 }

func (s *seqIDs) NewID() string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.n.Add(1))
}

func thb(satang int64) domain.Money { return domain.Money{Amount: satang, Currency: domain.THB} }

func setup(t *testing.T) (*service.TransferService, *memory.Store) {
	t.Helper()
	st := memory.NewStore()
	st.SeedAccount(domain.Account{ID: accAlice, CustomerID: alice, Balance: thb(100_000), Status: domain.AccountActive})
	st.SeedAccount(domain.Account{ID: accBob, CustomerID: bob, Balance: thb(5_000), Status: domain.AccountActive})
	st.SeedAccount(domain.Account{ID: accOther, CustomerID: bob, Balance: thb(9_000), Status: domain.AccountActive})
	return service.NewTransferService(st, fixedClock{}, &seqIDs{}), st
}

func req(key, from, to string, amount int64) domain.TransferRequest {
	return domain.TransferRequest{CustomerID: alice, IdempotencyKey: key, FromAccountID: from, ToAccountID: to, Amount: thb(amount)}
}

func balance(t *testing.T, st *memory.Store, id string) int64 {
	t.Helper()
	a, ok := st.Account(id)
	if !ok {
		t.Fatalf("account %s missing", id)
	}
	return a.Balance.Amount
}

func TestAC01_TransferMovesMoney(t *testing.T) {
	svc, st := setup(t)
	res, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 25_000), "trace-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Replayed || res.Transfer.Status != domain.TransferCompleted {
		t.Fatalf("got %+v", res)
	}
	if got := balance(t, st, accAlice); got != 75_000 {
		t.Errorf("alice balance = %d, want 75000", got)
	}
	if got := balance(t, st, accBob); got != 30_000 {
		t.Errorf("bob balance = %d, want 30000", got)
	}
}

func TestAC02_SameKeySameBodyReplays(t *testing.T) {
	svc, st := setup(t)
	ctx := context.Background()
	first, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 25_000), "t1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 25_000), "t2")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.Transfer.ID != first.Transfer.ID {
		t.Fatalf("expected replay of %s, got %+v", first.Transfer.ID, second)
	}
	if got := balance(t, st, accAlice); got != 75_000 {
		t.Errorf("debited twice: alice balance = %d", got)
	}
}

func TestAC03_SameKeyDifferentBodyRejected(t *testing.T) {
	svc, st := setup(t)
	ctx := context.Background()
	if _, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 25_000), "t1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 99_000), "t2")
	if !errors.Is(err, domain.ErrIdempotencyKeyReused) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyReused", err)
	}
	if got := balance(t, st, accAlice); got != 75_000 {
		t.Errorf("alice balance = %d, want 75000", got)
	}
}

func TestAC04_InsufficientFundsChangesNothing(t *testing.T) {
	svc, st := setup(t)
	_, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 100_001), "t1")
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
	if balance(t, st, accAlice) != 100_000 || balance(t, st, accBob) != 5_000 || st.TransferCount() != 0 || len(st.AuditEntries()) != 0 {
		t.Error("state changed after a rejected transfer")
	}
}

func TestAC05_ValidationErrors(t *testing.T) {
	svc, _ := setup(t)
	long := make([]rune, domain.MaxReferenceLen+1)
	for i := range long {
		long[i] = 'ก'
	}
	cases := map[string]domain.TransferRequest{
		"empty key":            req("", accAlice, accBob, 1),
		"key with space":       req("bad key", accAlice, accBob, 1),
		"key too long":         req(string(make([]byte, 65)), accAlice, accBob, 1),
		"zero amount":          req("k", accAlice, accBob, 0),
		"negative amount":      req("k", accAlice, accBob, -1),
		"same account":         req("k", accAlice, accAlice, 1),
		"bad from id":          req("k", "not-a-uuid", accBob, 1),
		"bad to id":            req("k", accAlice, "not-a-uuid", 1),
		"unsupported currency": func() domain.TransferRequest { r := req("k", accAlice, accBob, 1); r.Amount.Currency = "USD"; return r }(),
		"reference too long": func() domain.TransferRequest {
			r := req("k", accAlice, accBob, 1)
			r.Reference = string(long)
			return r
		}(),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.CreateTransfer(context.Background(), r, "t"); !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
}

func TestUppercaseIDsAreNormalized(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	up := strings.ToUpper(accAlice)
	if _, err := svc.GetAccount(ctx, alice, up); err != nil {
		t.Fatalf("GetAccount(upper) = %v", err)
	}
	first, err := svc.CreateTransfer(ctx, req("k1", up, accBob, 1_000), "t")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 1_000), "t")
	if err != nil || !second.Replayed || second.Transfer.ID != first.Transfer.ID {
		t.Fatalf("lower-case retry should replay: %+v, %v", second, err)
	}
}

func TestAC07_CannotUseOrSeeOthersAccount(t *testing.T) {
	svc, st := setup(t)
	ctx := context.Background()
	if _, err := svc.CreateTransfer(ctx, req("k1", accOther, accAlice, 1_000), "t"); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("transfer from bob's account: err = %v, want ErrAccountNotFound", err)
	}
	if _, err := svc.GetAccount(ctx, alice, accOther); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("get bob's account: err = %v, want ErrAccountNotFound", err)
	}
	if got := balance(t, st, accOther); got != 9_000 {
		t.Errorf("bob balance changed: %d", got)
	}
	a, err := svc.GetAccount(ctx, alice, accAlice)
	if err != nil || a.Balance.Amount != 100_000 {
		t.Fatalf("own account: %+v, %v", a, err)
	}
}

func TestAC08_UnknownDestination(t *testing.T) {
	svc, _ := setup(t)
	if _, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, missing, 1), "t"); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestAC09_InactiveAccounts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frozen string
	}{{"source frozen", accAlice}, {"destination closed", accBob}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, st := setup(t)
			a, _ := st.Account(tc.frozen)
			a.Status = domain.AccountFrozen
			if tc.frozen == accBob {
				a.Status = domain.AccountClosed
			}
			st.SeedAccount(a)
			_, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 1_000), "t")
			if !errors.Is(err, domain.ErrAccountInactive) {
				t.Fatalf("err = %v, want ErrAccountInactive", err)
			}
			if balance(t, st, accAlice) != 100_000 {
				t.Error("source debited")
			}
		})
	}
}

func TestAC10_CurrencyMismatch(t *testing.T) {
	svc, st := setup(t)
	a, _ := st.Account(accBob)
	a.Balance.Currency = "USD"
	st.SeedAccount(a)
	if _, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 1_000), "t"); !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("err = %v, want ErrCurrencyMismatch", err)
	}
}

func TestAC11_AuditEntryWritten(t *testing.T) {
	svc, st := setup(t)
	res, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 25_000), "trace-xyz")
	if err != nil {
		t.Fatal(err)
	}
	entries := st.AuditEntries()
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Action != domain.AuditTransferCreated || e.Actor != alice || e.ResourceID != res.Transfer.ID || e.TraceID != "trace-xyz" {
		t.Errorf("unexpected audit entry %+v", e)
	}
	before := e.Before["balances"].(map[string]any)
	after := e.After["balances"].(map[string]any)
	if before["from"] != int64(100_000) || after["from"] != int64(75_000) || before["to"] != int64(5_000) || after["to"] != int64(30_000) {
		t.Errorf("before/after = %v / %v", before, after)
	}
	// A replay must not add another audit entry.
	if _, err := svc.CreateTransfer(context.Background(), req("k1", accAlice, accBob, 25_000), "t2"); err != nil {
		t.Fatal(err)
	}
	if n := len(st.AuditEntries()); n != 1 {
		t.Errorf("audit entries after replay = %d, want 1", n)
	}
}

func TestAC12_ConcurrentDebitsNeverOverdraw(t *testing.T) {
	svc, st := setup(t)
	var ok atomic.Int64
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.CreateTransfer(context.Background(), req(fmt.Sprintf("k%d", i), accAlice, accBob, 10_000), "t")
			if err == nil {
				ok.Add(1)
			} else if !errors.Is(err, domain.ErrInsufficientFunds) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 10 {
		t.Errorf("successful transfers = %d, want 10", ok.Load())
	}
	if a, b := balance(t, st, accAlice), balance(t, st, accBob); a != 0 || a+b != 105_000 {
		t.Errorf("balances alice=%d bob=%d; money created or destroyed", a, b)
	}
}

func TestAC13_ConcurrentSameKeyDebitsOnce(t *testing.T) {
	svc, st := setup(t)
	ids := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.CreateTransfer(context.Background(), req("same", accAlice, accBob, 10_000), "t")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			ids <- res.Transfer.ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		} else if id != first {
			t.Fatalf("different transfer ids %s and %s", first, id)
		}
	}
	if got := balance(t, st, accAlice); got != 90_000 {
		t.Errorf("alice balance = %d, want 90000 (single debit)", got)
	}
}

func TestAC14_TransferVisibleOnlyToRequester(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	res, err := svc.CreateTransfer(ctx, req("k1", accAlice, accBob, 1_000), "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetTransfer(ctx, alice, res.Transfer.ID); err != nil {
		t.Fatalf("requester: %v", err)
	}
	if _, err := svc.GetTransfer(ctx, bob, res.Transfer.ID); !errors.Is(err, domain.ErrTransferNotFound) {
		t.Fatalf("other customer: err = %v, want ErrTransferNotFound", err)
	}
}

// TestAC13_DuplicateKeyRaceIsReplayed simulates losing the race on the unique index:
// the first attempt fails with ErrDuplicateIdempotencyKey, the retry must replay the winner.
func TestAC13_DuplicateKeyRaceIsReplayed(t *testing.T) {
	_, st := setup(t)
	svc := service.NewTransferService(st, fixedClock{}, &seqIDs{})
	ctx := context.Background()
	winner, err := svc.CreateTransfer(ctx, req("race", accAlice, accBob, 1_000), "t")
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	racer := service.NewTransferService(txFunc(func(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
		var raced bool
		once.Do(func() { raced = true })
		if raced {
			return port.ErrDuplicateIdempotencyKey
		}
		return st.WithinTx(ctx, fn)
	}), fixedClock{}, &seqIDs{})
	res, err := racer.CreateTransfer(ctx, req("race", accAlice, accBob, 1_000), "t")
	if err != nil || !res.Replayed || res.Transfer.ID != winner.Transfer.ID {
		t.Fatalf("got %+v, %v; want replay of %s", res, err, winner.Transfer.ID)
	}
}

type txFunc func(ctx context.Context, fn func(context.Context, port.Repositories) error) error

func (f txFunc) WithinTx(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return f(ctx, fn)
}
