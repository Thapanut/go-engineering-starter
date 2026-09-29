package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
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
	errTooLarge      = &apiError{http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large"}
)

// toAPIError maps errors to contract error codes. Unknown errors become
// INTERNAL_ERROR so internals never reach the client. Add feature-specific
// cases here together with their code in contracts/openapi.yaml.
func toAPIError(err error) (*apiError, bool) {
	var ae *apiError
	var ve *domain.ValidationError
	var fe *fiber.Error
	switch {
	case errors.As(err, &ae):
		return ae, true
	case errors.As(err, &ve):
		return &apiError{http.StatusBadRequest, "VALIDATION_ERROR", ve.Msg}, true
	case errors.Is(err, domain.ErrValidation):
		return &apiError{http.StatusBadRequest, "VALIDATION_ERROR", "invalid request"}, true
	case errors.Is(err, domain.ErrNotFound):
		return &apiError{http.StatusNotFound, "NOT_FOUND", "resource not found"}, true
	case errors.Is(err, domain.ErrInvalidSignature):
		return &apiError{http.StatusUnauthorized, "INVALID_SIGNATURE", "webhook signature is invalid"}, true
	case errors.Is(err, domain.ErrPaymentMismatch):
		return &apiError{http.StatusUnprocessableEntity, "PAYMENT_MISMATCH", "notification does not match the payment"}, true
	case errors.Is(err, domain.ErrConflict):
		return &apiError{http.StatusConflict, "CONFLICT", "request conflicts with current state"}, true
	case errors.As(err, &fe) && fe.Code == fiber.StatusRequestEntityTooLarge:
		return errTooLarge, true
	case errors.As(err, &fe) && fe.Code == fiber.StatusNotFound:
		return errRouteNotFound, true
	default:
		return errInternal, false
	}
}

// errorHandler is Fiber's central error handler: handlers just `return err`.
func errorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		ae, known := toAPIError(err)
		if !known {
			log.Error("request failed", slog.String("error", err.Error()), slog.String("trace_id", traceIDOf(c)))
		}
		return c.Status(ae.status).JSON(errorBody{Code: ae.code, Message: ae.message, TraceID: traceIDOf(c)})
	}
}
