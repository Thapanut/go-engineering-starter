package domain

import "github.com/Thapanut/go-engineering-starter/internal/kernel"

// Money and its helpers live in the shared kernel (ADR-0005); these aliases keep
// the payment domain's vocabulary.
type (
	// Money is an amount in minor units (satang for THB). Never use float for money.
	Money = kernel.Money
	// Currency is an ISO 4217 code.
	Currency = kernel.Currency
)

// THB is the Thai baht.
const THB = kernel.THB

// ErrCurrencyMismatch is returned when combining amounts in different currencies.
var ErrCurrencyMismatch = kernel.ErrCurrencyMismatch

// ParseDecimal converts a decimal amount in major units into minor units, never via float.
func ParseDecimal(s string) (int64, error) { return kernel.ParseDecimal(s) }
