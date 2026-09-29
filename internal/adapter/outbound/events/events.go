// Package events encodes domain events into the messages described in
// contracts/asyncapi.yaml. Every outbox adapter uses it, so the payload on the wire
// does not depend on which store wrote it.
package events

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// TopicPaymentStatusChanged carries payment.status-changed events, keyed by payment id.
const TopicPaymentStatusChanged = "payments.v1.status-changed"

// paymentStatusChangedV1 is the PaymentStatusChanged message payload (asyncapi.yaml).
type paymentStatusChangedV1 struct {
	EventID     string    `json:"event_id"`
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	InvoiceNo   string    `json:"invoice_no"`
	Status      string    `json:"status"`
	Amount      string    `json:"amount"`       // major units as a decimal string, e.g. "1000.00"
	AmountMinor int64     `json:"amount_minor"` // minor units, e.g. 100000
	Currency    string    `json:"currency"`
	ProviderRef string    `json:"provider_ref"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// PaymentStatusChanged encodes e as an outbox message.
func PaymentStatusChanged(e domain.PaymentStatusChanged) (port.OutboxMessage, error) {
	occurred := e.OccurredAt.UTC()
	payload, err := json.Marshal(paymentStatusChangedV1{
		EventID:     e.EventID,
		PaymentID:   e.PaymentID,
		OrderID:     e.OrderID,
		InvoiceNo:   e.InvoiceNo,
		Status:      string(e.Status),
		Amount:      e.Amount.Decimal(),
		AmountMinor: e.Amount.Amount,
		Currency:    string(e.Amount.Currency),
		ProviderRef: e.ProviderRef,
		OccurredAt:  occurred,
	})
	if err != nil {
		return port.OutboxMessage{}, fmt.Errorf("encode payment.status-changed: %w", err)
	}
	return port.OutboxMessage{
		ID:        e.EventID,
		Topic:     TopicPaymentStatusChanged,
		Key:       e.PaymentID,
		Payload:   payload,
		CreatedAt: occurred,
	}, nil
}
