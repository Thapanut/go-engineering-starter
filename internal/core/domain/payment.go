package domain

import (
	"errors"
	"time"
)

// PaymentStatus is the lifecycle state of a payment.
type PaymentStatus string

// Payment statuses. SUCCESS and FAILED are terminal.
const (
	PaymentPending PaymentStatus = "PENDING"
	PaymentSuccess PaymentStatus = "SUCCESS"
	PaymentFailed  PaymentStatus = "FAILED"
)

// IsTerminal reports whether no further transition is allowed.
func (s PaymentStatus) IsTerminal() bool { return s == PaymentSuccess || s == PaymentFailed }

// Payment errors.
var (
	// ErrInvalidSignature means the webhook could not be authenticated.
	ErrInvalidSignature = errors.New("invalid webhook signature")
	// ErrPaymentMismatch means a notification does not match the stored payment (amount/currency).
	ErrPaymentMismatch = errors.New("notification does not match payment")
)

// Payment is a payment we asked the provider to collect. InvoiceNo is the
// transaction reference shared with the provider.
type Payment struct {
	ID           string
	InvoiceNo    string
	Amount       Money
	Status       PaymentStatus
	ProviderRef  string // provider's transaction reference (2C2P tranRef)
	ProviderCode string // provider's raw response code, kept for disputes
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PaymentNotification is a verified, provider-agnostic payment outcome.
type PaymentNotification struct {
	InvoiceNo    string
	ProviderRef  string
	Amount       Money
	Outcome      PaymentStatus // SUCCESS or FAILED
	ProviderCode string
}

// Validate checks that a decoded notification is complete (spec AC-09). The
// amount format is the adapter's job; a wrong amount fails Payment.Apply.
func (n PaymentNotification) Validate() error {
	switch {
	case n.InvoiceNo == "":
		return Invalid("invoiceNo is required")
	case n.ProviderRef == "":
		return Invalid("tranRef is required")
	case n.Amount.Currency == "":
		return Invalid("currencyCode is required")
	case n.Outcome != PaymentSuccess && n.Outcome != PaymentFailed:
		return Invalid("outcome must be SUCCESS or FAILED")
	}
	return nil
}

// WebhookOutcome says what applying a notification did.
type WebhookOutcome string

// Webhook outcomes. Only Processed changes state.
const (
	OutcomeProcessed       WebhookOutcome = "PROCESSED"
	OutcomeDuplicate       WebhookOutcome = "DUPLICATE"
	OutcomeConflictIgnored WebhookOutcome = "CONFLICT_IGNORED"
)

// PaymentStatusChanged is raised when a payment reaches a final status. It is
// published to other services through the transactional outbox.
type PaymentStatusChanged struct {
	EventID     string
	PaymentID   string
	InvoiceNo   string
	Status      PaymentStatus
	Amount      Money
	ProviderRef string
	OccurredAt  time.Time
}

// StatusChanged returns the event for the transition Apply just made.
func (p Payment) StatusChanged(eventID string) PaymentStatusChanged {
	return PaymentStatusChanged{
		EventID:     eventID,
		PaymentID:   p.ID,
		InvoiceNo:   p.InvoiceNo,
		Status:      p.Status,
		Amount:      p.Amount,
		ProviderRef: p.ProviderRef,
		OccurredAt:  p.UpdatedAt,
	}
}

// Apply transitions a PENDING payment according to n. Terminal payments never
// change: a repeat of the same outcome is a duplicate, and a different outcome is
// ignored for reconciliation (spec AC-01..AC-04, AC-08).
func (p *Payment) Apply(n PaymentNotification, now time.Time) (WebhookOutcome, error) {
	if n.Amount != p.Amount {
		return "", ErrPaymentMismatch
	}
	if p.Status.IsTerminal() {
		if p.Status == n.Outcome && p.ProviderRef == n.ProviderRef {
			return OutcomeDuplicate, nil
		}
		return OutcomeConflictIgnored, nil
	}
	p.Status = n.Outcome
	p.ProviderRef = n.ProviderRef
	p.ProviderCode = n.ProviderCode
	p.UpdatedAt = now
	return OutcomeProcessed, nil
}
