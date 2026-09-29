package kernel

import "errors"

// Generic business errors. Inbound adapters map them to contract error codes;
// anything else is treated as an internal error. Feature-specific errors should
// wrap one of these (e.g. fmt.Errorf("account %w", ErrNotFound)) or be added here
// together with their contract code.
var (
	ErrValidation = errors.New("validation failed")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
)

// ValidationError carries a client-safe message and matches ErrValidation.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Is makes errors.Is(err, ErrValidation) true.
func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

// Invalid builds a ValidationError.
func Invalid(msg string) error { return &ValidationError{Msg: msg} }
