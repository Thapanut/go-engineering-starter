// Package kernel is the shared kernel of the modules (ADR-0005): value objects and
// generic business errors that catalog, ordering, and payment all use. It depends
// on the standard library only and must stay small.
package kernel

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Currency is an ISO 4217 code.
type Currency string

// THB is the Thai baht.
const THB Currency = "THB"

// ErrCurrencyMismatch is returned when combining amounts in different currencies.
var ErrCurrencyMismatch = Invalid("currency mismatch")

// Money is an amount in minor units (satang for THB). Never use float for money.
type Money struct {
	Amount   int64
	Currency Currency
}

// Add returns m+o. It fails on currency mismatch or int64 overflow.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	if o.Amount > 0 && m.Amount > math.MaxInt64-o.Amount {
		return Money{}, Invalid("amount overflow")
	}
	return Money{Amount: m.Amount + o.Amount, Currency: m.Currency}, nil
}

// Sub returns m-o. It fails on currency mismatch; the result may be negative.
func (m Money) Sub(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{Amount: m.Amount - o.Amount, Currency: m.Currency}, nil
}

// Decimal formats the amount in major units with two decimals, e.g. 100000 → "1000.00".
func (m Money) Decimal() string {
	sign := ""
	a := m.Amount
	if a < 0 {
		sign, a = "-", -a
	}
	return fmt.Sprintf("%s%d.%02d", sign, a/100, a%100)
}

// String formats m for humans, e.g. "1500.00 THB".
func (m Money) String() string { return m.Decimal() + " " + string(m.Currency) }

var decimalRe = regexp.MustCompile(`^(\d{1,15})(?:\.(\d{1,2}))?$`)

// ParseDecimal converts a non-negative decimal amount in major units, such as
// "1000.00", "230.8", or "5", into minor units (100000, 23080, 500) with string
// arithmetic only, never float. Every currency is assumed to have two decimal
// places (UNCONFIRMED for zero-decimal currencies such as JPY).
func ParseDecimal(s string) (int64, error) {
	m := decimalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, Invalid("amount must be a non-negative decimal with at most 2 places")
	}
	whole, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || whole > (math.MaxInt64-99)/100 {
		return 0, Invalid("amount out of range")
	}
	frac := int64(0)
	if m[2] != "" {
		f, _ := strconv.ParseInt(m[2], 10, 64) // regex guarantees 1-2 digits
		if len(m[2]) == 1 {
			f *= 10
		}
		frac = f
	}
	return whole*100 + frac, nil
}
