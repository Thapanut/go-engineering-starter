package kernel_test

import (
	"errors"
	"math"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

func thb(v int64) kernel.Money { return kernel.Money{Amount: v, Currency: kernel.THB} }

func TestMoneyAddSub(t *testing.T) {
	sum, err := thb(150).Add(thb(50))
	if err != nil || sum.Amount != 200 {
		t.Fatalf("Add = %v, %v", sum, err)
	}
	diff, err := thb(150).Sub(thb(200))
	if err != nil || diff.Amount != -50 {
		t.Fatalf("Sub = %v, %v", diff, err)
	}
	usd := kernel.Money{Amount: 1, Currency: "USD"}
	if _, err := thb(1).Add(usd); !errors.Is(err, kernel.ErrCurrencyMismatch) || !errors.Is(err, kernel.ErrValidation) {
		t.Fatalf("Add mismatched currency err = %v", err)
	}
	if _, err := thb(math.MaxInt64).Add(thb(1)); !errors.Is(err, kernel.ErrValidation) {
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

func TestParseDecimal(t *testing.T) {
	for in, want := range map[string]int64{"1000.00": 100000, "230.87": 23087, "230.8": 23080, "5": 500, "0.05": 5, "0": 0} {
		if got, err := kernel.ParseDecimal(in); err != nil || got != want {
			t.Errorf("%q → %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "-1.00", "1.001", "1,000.00", "1e3", " 1.00", "1.", ".5", "1000000000000000"} {
		if _, err := kernel.ParseDecimal(in); !errors.Is(err, kernel.ErrValidation) {
			t.Errorf("%q: err = %v, want ErrValidation", in, err)
		}
	}
}

func TestMoneyDecimalRoundTrips(t *testing.T) {
	for _, v := range []int64{100000, 23087, 5, 0} {
		back, err := kernel.ParseDecimal(thb(v).Decimal())
		if err != nil || back != v {
			t.Errorf("%d → %q → %d, %v", v, thb(v).Decimal(), back, err)
		}
	}
}
