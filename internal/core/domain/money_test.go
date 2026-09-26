package domain_test

import (
	"errors"
	"math"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

func thb(v int64) domain.Money { return domain.Money{Amount: v, Currency: domain.THB} }

func TestMoneyAddSub(t *testing.T) {
	sum, err := thb(150).Add(thb(50))
	if err != nil || sum.Amount != 200 {
		t.Fatalf("Add = %v, %v", sum, err)
	}
	diff, err := thb(150).Sub(thb(200))
	if err != nil || diff.Amount != -50 {
		t.Fatalf("Sub = %v, %v", diff, err)
	}
	if _, err := thb(1).Add(domain.Money{Amount: 1, Currency: "USD"}); !errors.Is(err, domain.ErrCurrencyMismatch) {
		t.Fatalf("Add mismatched currency err = %v", err)
	}
	if _, err := thb(math.MaxInt64).Add(thb(1)); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("overflow err = %v", err)
	}
}

func TestMoneyString(t *testing.T) {
	for in, want := range map[int64]string{150000: "1500.00 THB", 5: "0.05 THB", -1234: "-12.34 THB"} {
		if got := thb(in).String(); got != want {
			t.Errorf("%d → %q, want %q", in, got, want)
		}
	}
}

func TestAccountDebitCredit(t *testing.T) {
	a := domain.Account{Balance: thb(1000), Status: domain.AccountActive}
	if err := a.Debit(thb(1001)); !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("overdraw err = %v", err)
	}
	if err := a.Debit(thb(1000)); err != nil || a.Balance.Amount != 0 {
		t.Fatalf("debit to zero: %v, balance %d", err, a.Balance.Amount)
	}
	a.Status = domain.AccountFrozen
	if err := a.Credit(thb(1)); !errors.Is(err, domain.ErrAccountInactive) {
		t.Fatalf("credit frozen err = %v", err)
	}
}

func TestFingerprintChangesWithBody(t *testing.T) {
	base := domain.TransferRequest{FromAccountID: "a", ToAccountID: "b", Amount: thb(100), Reference: "x"}
	other := base
	other.Amount = thb(101)
	if base.Fingerprint() == other.Fingerprint() {
		t.Fatal("fingerprint ignores amount")
	}
	// Length-prefixing prevents "ab"+"c" colliding with "a"+"bc".
	x := domain.TransferRequest{FromAccountID: "ab", ToAccountID: "c", Amount: thb(1)}
	y := domain.TransferRequest{FromAccountID: "a", ToAccountID: "bc", Amount: thb(1)}
	if x.Fingerprint() == y.Fingerprint() {
		t.Fatal("fingerprint is ambiguous")
	}
}
