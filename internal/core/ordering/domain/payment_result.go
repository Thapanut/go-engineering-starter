package domain

import (
	"errors"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// ErrAmountMismatch means payment collected a different amount than the order total.
var ErrAmountMismatch = errors.New("payment amount does not match the order")

// PaymentResult is payment's final outcome for an order, as ordering understands it.
type PaymentResult struct {
	Succeeded bool
	Amount    kernel.Money
}

// ResultOutcome says what applying a payment result did.
type ResultOutcome string

// Outcomes of ApplyPayment. Only Applied changes the order.
const (
	ResultApplied         ResultOutcome = "APPLIED"
	ResultDuplicate       ResultOutcome = "DUPLICATE"
	ResultConflictIgnored ResultOutcome = "CONFLICT_IGNORED"
)

// ApplyPayment moves an order awaiting payment to PAID or PAYMENT_FAILED. Final
// orders never change: the same outcome again is a duplicate, a different one is
// ignored for reconciliation (spec order-flow-modules AC-06..AC-09).
func (o *Order) ApplyPayment(r PaymentResult, now time.Time) (ResultOutcome, error) {
	if r.Amount != o.Amount {
		return "", ErrAmountMismatch
	}
	target := PaymentFailed
	if r.Succeeded {
		target = Paid
	}
	if o.Status.IsFinal() {
		if o.Status == target {
			return ResultDuplicate, nil
		}
		return ResultConflictIgnored, nil
	}
	o.Status, o.UpdatedAt = target, now
	return ResultApplied, nil
}
