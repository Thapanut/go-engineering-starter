package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
)

// Synthetic test data only.
func TestPaymentStatusChangedMatchesContract(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*3600)
	msg, err := PaymentStatusChanged(domain.PaymentStatusChanged{
		EventID: "7f0c2b1e-5d4a-4e8b-9a61-3c2d1e0f9a8b", PaymentID: "0e6a4f6e-6a1f-4f5e-9d6e-2b7f3c1a9d01",
		OrderID: "5b1d7c2e-8f3a-4c6b-9e0d-1a2b3c4d5e6f", InvoiceNo: "INV-0001", Status: domain.PaymentSuccess, Amount: domain.Money{Amount: 23087, Currency: domain.THB},
		ProviderRef: "2868821", OccurredAt: time.Date(2026, 9, 27, 17, 0, 0, 0, bangkok),
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != "7f0c2b1e-5d4a-4e8b-9a61-3c2d1e0f9a8b" || msg.Topic != "payments.v1.status-changed" ||
		msg.Key != "0e6a4f6e-6a1f-4f5e-9d6e-2b7f3c1a9d01" || !msg.CreatedAt.Equal(time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("envelope = %+v", msg)
	}

	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"event_id": "7f0c2b1e-5d4a-4e8b-9a61-3c2d1e0f9a8b", "payment_id": "0e6a4f6e-6a1f-4f5e-9d6e-2b7f3c1a9d01",
		"order_id":   "5b1d7c2e-8f3a-4c6b-9e0d-1a2b3c4d5e6f",
		"invoice_no": "INV-0001", "status": "SUCCESS", "amount": "230.87", "amount_minor": float64(23087), "currency": "THB",
		"provider_ref": "2868821", "occurred_at": "2026-09-27T10:00:00Z",
	}
	if len(got) != len(want) {
		t.Fatalf("payload fields = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}
