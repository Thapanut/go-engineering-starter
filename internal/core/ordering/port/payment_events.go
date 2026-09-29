package port

import (
	"context"

	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// PaymentStatusChanged is the payment module's event as ordering consumes it
// (contracts/asyncapi.yaml, payment.status-changed).
type PaymentStatusChanged struct {
	EventID   string
	OrderID   string // empty for payments not created by ordering
	Succeeded bool   // status SUCCESS; FAILED otherwise
	Amount    kernel.Money
}

// EventOutcome says what handling one payment event did.
type EventOutcome string

// Event outcomes. Only Applied changes an order; every outcome records the event.
const (
	EventApplied          EventOutcome = "APPLIED"
	EventAlreadyProcessed EventOutcome = "ALREADY_PROCESSED" // same event_id seen before
	EventDuplicate        EventOutcome = "DUPLICATE"         // order already has this outcome
	EventConflictIgnored  EventOutcome = "CONFLICT_IGNORED"  // order is final with the other outcome
	EventAmountMismatch   EventOutcome = "AMOUNT_MISMATCH"   // needs reconciliation
	EventUnknownOrder     EventOutcome = "UNKNOWN_ORDER"
)

// PaymentEventHandler applies payment events to orders (inbound port; driven by
// the Kafka consumer or, with STORE=memory, the in-process publisher).
type PaymentEventHandler interface {
	// HandlePaymentStatusChanged is idempotent per EventID. It returns an error
	// only when the event should be retried (e.g. the database is unavailable).
	HandlePaymentStatusChanged(ctx context.Context, e PaymentStatusChanged) (EventOutcome, error)
}
