// Package domain holds entities, value objects, and business errors.
// It depends on the standard library only (ADR-0002).
package domain

import (
	"fmt"
	"math"
)

// Currency is an ISO 4217 code.
type Currency string

// THB is the only currency supported by this service.
const THB Currency = "THB"

// IsSupported reports whether the service can move money in c.
func (c Currency) IsSupported() bool { return c == THB }

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
		return Money{}, fmt.Errorf("money add overflow: %w", ErrValidation)
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

// String formats m for humans, e.g. "1500.00 THB".
func (m Money) String() string {
	sign := ""
	a := m.Amount
	if a < 0 {
		sign, a = "-", -a
	}
	return fmt.Sprintf("%s%d.%02d %s", sign, a/100, a%100, m.Currency)
}
