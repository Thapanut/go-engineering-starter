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
	errNotReady      = &apiError{http.StatusServiceUnavailable, "NOT_READY", "service is not ready"}
	errRouteNotFound = &apiError{http.StatusNotFound, "NOT_FOUND", "route not found"}
)

// toAPIError maps domain errors to contract error codes. Unknown errors become
// INTERNAL_ERROR so internals never reach the client. Add feature-specific
// cases here together with their code in contracts/openapi.yaml.
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
	case errors.Is(err, domain.ErrNotFound):
		return &apiError{http.StatusNotFound, "NOT_FOUND", "resource not found"}, true
	case errors.Is(err, domain.ErrConflict):
		return &apiError{http.StatusConflict, "CONFLICT", "request conflicts with current state"}, true
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
