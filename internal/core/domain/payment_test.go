package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

func pending() domain.Payment {
	return domain.Payment{ID: "p1", InvoiceNo: "INV1", Amount: thb(23087), Status: domain.PaymentPending}
}

func notification(outcome domain.PaymentStatus) domain.PaymentNotification {
	return domain.PaymentNotification{InvoiceNo: "INV1", ProviderRef: "2868821", Amount: thb(23087), Outcome: outcome, ProviderCode: "0000"}
}

func TestPaymentApplyTransitionsPending(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	for _, outcome := range []domain.PaymentStatus{domain.PaymentSuccess, domain.PaymentFailed} {
		p := pending()
		got, err := p.Apply(notification(outcome), now)
		if err != nil || got != domain.OutcomeProcessed || p.Status != outcome || p.ProviderRef != "2868821" || !p.UpdatedAt.Equal(now) {
			t.Fatalf("%s: outcome=%s err=%v payment=%+v", outcome, got, err, p)
		}
	}
}

func TestPaymentApplyIsIdempotentOnceTerminal(t *testing.T) {
	p := pending()
	if _, err := p.Apply(notification(domain.PaymentSuccess), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	before := p
	if got, _ := p.Apply(notification(domain.PaymentSuccess), time.Unix(2, 0)); got != domain.OutcomeDuplicate || p != before {
		t.Fatalf("repeat: outcome=%s, payment changed=%v", got, p != before)
	}
	if got, _ := p.Apply(notification(domain.PaymentFailed), time.Unix(3, 0)); got != domain.OutcomeConflictIgnored || p != before {
		t.Fatalf("conflict: outcome=%s, payment changed=%v", got, p != before)
	}
}

func TestPaymentApplyRejectsMismatch(t *testing.T) {
	for name, amount := range map[string]domain.Money{
		"amount":   thb(23086),
		"currency": {Amount: 23087, Currency: "USD"},
	} {
		p := pending()
		n := notification(domain.PaymentSuccess)
		n.Amount = amount
		if _, err := p.Apply(n, time.Now()); !errors.Is(err, domain.ErrPaymentMismatch) || p.Status != domain.PaymentPending {
			t.Fatalf("%s: err=%v status=%s", name, err, p.Status)
		}
	}
}

func TestPaymentNotificationValidate(t *testing.T) {
	ok := notification(domain.PaymentSuccess)
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*domain.PaymentNotification){
		"no invoice":      func(n *domain.PaymentNotification) { n.InvoiceNo = "" },
		"no provider ref": func(n *domain.PaymentNotification) { n.ProviderRef = "" },
		"no currency":     func(n *domain.PaymentNotification) { n.Amount.Currency = "" },
		"pending outcome": func(n *domain.PaymentNotification) { n.Outcome = domain.PaymentPending },
	} {
		n := ok
		mutate(&n)
		if err := n.Validate(); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestPaymentStatusChangedCarriesTheTransition(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	p := pending()
	if _, err := p.Apply(notification(domain.PaymentSuccess), now); err != nil {
		t.Fatal(err)
	}
	want := domain.PaymentStatusChanged{EventID: "evt-1", PaymentID: "p1", InvoiceNo: "INV1",
		Status: domain.PaymentSuccess, Amount: thb(23087), ProviderRef: "2868821", OccurredAt: now}
	if got := p.StatusChanged("evt-1"); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
