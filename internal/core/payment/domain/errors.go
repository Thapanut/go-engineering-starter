// Package domain holds the payment context's entities, value objects, and business
// errors. It depends on the standard library and the shared kernel only (ADR-0002, ADR-0005).
package domain

import "github.com/Thapanut/go-engineering-starter/internal/kernel"

// Generic business errors, shared by every module through the kernel. Inbound
// adapters map them to contract error codes; anything else is an internal error.
var (
	ErrValidation = kernel.ErrValidation
	ErrNotFound   = kernel.ErrNotFound
	ErrConflict   = kernel.ErrConflict
)

// ValidationError carries a client-safe message and matches ErrValidation.
type ValidationError = kernel.ValidationError

// Invalid builds a ValidationError.
func Invalid(msg string) error { return kernel.Invalid(msg) }
