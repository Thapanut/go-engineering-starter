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
	usd := domain.Money{Amount: 1, Currency: "USD"}
	if _, err := thb(1).Add(usd); !errors.Is(err, domain.ErrCurrencyMismatch) || !errors.Is(err, domain.ErrValidation) {
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
