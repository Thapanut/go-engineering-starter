// Package paymentevents is the ordering module's driving adapter for the payment
// module's payment.status-changed event (contracts/asyncapi.yaml, ADR-0005). It
// decodes the message and calls ordering's PaymentEventHandler; Consumer feeds it
// from Kafka (consumer group "ordering"), and with STORE=memory the in-process
// publisher calls Handle directly.
package paymentevents

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"

	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Topic and consumer group of this adapter.
const (
	Topic   = "payments.v1.status-changed"
	GroupID = "ordering"
)

// message is the part of the PaymentStatusChangedV1 payload that ordering uses.
type message struct {
	EventID     string `json:"event_id"`
	OrderID     string `json:"order_id"`
	Status      string `json:"status"`
	AmountMinor *int64 `json:"amount_minor"`
	Currency    string `json:"currency"`
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func decode(payload []byte) (orderingport.PaymentStatusChanged, error) {
	var m message
	if err := json.Unmarshal(payload, &m); err != nil {
		return orderingport.PaymentStatusChanged{}, kernel.Invalid("malformed payment event")
	}
	switch {
	case m.EventID == "":
		return orderingport.PaymentStatusChanged{}, kernel.Invalid("event_id is required")
	case m.Status != "SUCCESS" && m.Status != "FAILED":
		return orderingport.PaymentStatusChanged{}, kernel.Invalid("status must be SUCCESS or FAILED")
	case m.AmountMinor == nil || !currencyRe.MatchString(m.Currency):
		return orderingport.PaymentStatusChanged{}, kernel.Invalid("amount_minor and currency are required")
	}
	return orderingport.PaymentStatusChanged{EventID: m.EventID, OrderID: m.OrderID, Succeeded: m.Status == "SUCCESS",
		Amount: kernel.Money{Amount: *m.AmountMinor, Currency: kernel.Currency(m.Currency)}}, nil
}

// Handler decodes payment events and applies them to orders.
type Handler struct {
	UseCase orderingport.PaymentEventHandler
	Log     *slog.Logger
}

// Handle applies one message. It returns an error only when the message should
// be retried; malformed messages are logged and skipped so they cannot block the
// partition. Logs carry ids only.
func (h Handler) Handle(ctx context.Context, payload []byte) error {
	e, err := decode(payload)
	if err != nil {
		h.Log.ErrorContext(ctx, "dropping malformed payment event", slog.String("error", err.Error()))
		return nil
	}
	out, err := h.UseCase.HandlePaymentStatusChanged(ctx, e)
	if errors.Is(err, kernel.ErrValidation) {
		h.Log.ErrorContext(ctx, "dropping invalid payment event", slog.String("event_id", e.EventID), slog.String("error", err.Error()))
		return nil
	}
	if err != nil {
		return err
	}
	attrs := []any{slog.String("event_id", e.EventID), slog.String("order_id", e.OrderID), slog.String("outcome", string(out))}
	switch out {
	case orderingport.EventAmountMismatch:
		h.Log.ErrorContext(ctx, "payment amount differs from order total; needs reconciliation", attrs...)
	case orderingport.EventConflictIgnored:
		h.Log.WarnContext(ctx, "payment event conflicts with final order status; needs reconciliation", attrs...)
	default:
		h.Log.InfoContext(ctx, "payment event handled", attrs...)
	}
	return nil
}
