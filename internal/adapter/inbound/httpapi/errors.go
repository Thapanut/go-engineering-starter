package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

// errorBody is the contract's Error schema.
type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"traceId"`
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code }

var (
	errUnauthorized  = &apiError{http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid bearer token"}
	errInternal      = &apiError{http.StatusInternalServerError, "INTERNAL_ERROR", "internal error"}
	errRouteNotFound = &apiError{http.StatusNotFound, "NOT_FOUND", "route not found"}
)

// toAPIError maps domain errors to contract error codes. Unknown errors become
// INTERNAL_ERROR so internals never reach the client (spec AC-15).
func toAPIError(err error) (*apiError, bool) {
	var ae *apiError
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ae):
		return ae, true
	case errors.As(err, &ve):
		return &apiError{http.StatusBadRequest, "VALIDATION_ERROR", ve.Msg}, true
	case errors.Is(err, domain.ErrValidation):
		return &apiError{http.StatusBadRequest, "VALIDATION_ERROR", "invalid request"}, true
	case errors.Is(err, domain.ErrAccountNotFound):
		return &apiError{http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found"}, true
	case errors.Is(err, domain.ErrTransferNotFound):
		return &apiError{http.StatusNotFound, "TRANSFER_NOT_FOUND", "transfer not found"}, true
	case errors.Is(err, domain.ErrInsufficientFunds):
		return &apiError{http.StatusUnprocessableEntity, "INSUFFICIENT_FUNDS", "insufficient funds"}, true
	case errors.Is(err, domain.ErrAccountInactive):
		return &apiError{http.StatusUnprocessableEntity, "ACCOUNT_INACTIVE", "account is not active"}, true
	case errors.Is(err, domain.ErrCurrencyMismatch):
		return &apiError{http.StatusUnprocessableEntity, "CURRENCY_MISMATCH", "currency does not match account"}, true
	case errors.Is(err, domain.ErrIdempotencyKeyReused):
		return &apiError{http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED", "Idempotency-Key was already used with a different request"}, true
	default:
		return errInternal, false
	}
}

func writeError(c *gin.Context, log *slog.Logger, err error) {
	ae, known := toAPIError(err)
	if !known {
		log.Error("request failed", slog.String("error", err.Error()), slog.String("trace_id", traceIDOf(c)))
	}
	c.AbortWithStatusJSON(ae.status, errorBody{Code: ae.code, Message: ae.message, TraceID: traceIDOf(c)})
}
