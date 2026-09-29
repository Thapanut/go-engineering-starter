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

// Topics and consumer group of this adapter (spec ordering-consumer-dlq).
const (
	Topic      = "payments.v1.status-changed"
	GroupID    = "ordering"
	DLQTopic   = "payments.v1.status-changed.ordering.dlq"   // poison messages, written by the consumer
	RetryTopic = "payments.v1.status-changed.ordering.retry" // replayed by an Admin (cmd/dlqreplay), read only by ordering
)

// ErrPoison marks a message that can never be applied: undecodable or invalid.
// The Kafka consumer dead-letters it; in-process delivery logs and skips it.
var ErrPoison = errors.New("poison payment event")

// poisonError carries a fixed, payload-free reason.
type poisonError struct{ reason string }

func (e poisonError) Error() string        { return "poison payment event: " + e.reason }
func (e poisonError) Is(target error) bool { return target == ErrPoison }

// PoisonReason returns the payload-free reason of a poison error, or "".
func PoisonReason(err error) string {
	var p poisonError
	if errors.As(err, &p) {
		return p.reason
	}
	return ""
}

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

// Handle applies one message. It returns an error wrapping ErrPoison when the
// message can never be applied (undecodable, or rejected by validation), and any
// other error when it should be retried. Logs and reasons carry ids only; the
// validation messages are fixed texts, never payload values.
func (h Handler) Handle(ctx context.Context, payload []byte) error {
	e, err := decode(payload)
	if err != nil {
		h.Log.ErrorContext(ctx, "poison payment event: cannot decode", slog.String("error", err.Error()))
		return poisonError{reason: err.Error()}
	}
	out, err := h.UseCase.HandlePaymentStatusChanged(ctx, e)
	if errors.Is(err, kernel.ErrValidation) {
		h.Log.ErrorContext(ctx, "poison payment event: invalid", slog.String("event_id", e.EventID), slog.String("error", err.Error()))
		return poisonError{reason: err.Error()}
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

// DeliverInProcess is Handle for in-process delivery (STORE=memory, no Kafka):
// there is no DLQ, so a poison event is logged by Handle and skipped (AC-16).
func (h Handler) DeliverInProcess(ctx context.Context, payload []byte) error {
	if err := h.Handle(ctx, payload); !errors.Is(err, ErrPoison) {
		return err
	}
	return nil
}
