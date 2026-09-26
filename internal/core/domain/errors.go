package domain

import "errors"

// Business errors. Inbound adapters map them to contract error codes;
// anything else is treated as an internal error.
var (
	ErrValidation           = errors.New("validation failed")
	ErrAccountNotFound      = errors.New("account not found")
	ErrTransferNotFound     = errors.New("transfer not found")
	ErrInsufficientFunds    = errors.New("insufficient funds")
	ErrAccountInactive      = errors.New("account is not active")
	ErrCurrencyMismatch     = errors.New("currency mismatch")
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different request")
)

// ValidationError carries a client-safe message and matches ErrValidation.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Is makes errors.Is(err, ErrValidation) true.
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

// Invalid builds a ValidationError.
func Invalid(msg string) error { return &ValidationError{Msg: msg} }
