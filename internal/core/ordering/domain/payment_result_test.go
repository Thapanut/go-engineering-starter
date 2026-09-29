package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
)

func awaiting() domain.Order {
	return domain.Order{ID: "o1", Status: domain.AwaitingPayment, Amount: thb(119000)}
}

func TestOrderFlowAC06_ApplyPaymentMovesToFinalStatus(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	for succeeded, want := range map[bool]domain.Status{true: domain.Paid, false: domain.PaymentFailed} {
		o := awaiting()
		got, err := o.ApplyPayment(domain.PaymentResult{Succeeded: succeeded, Amount: thb(119000)}, now)
		if err != nil || got != domain.ResultApplied || o.Status != want || !o.UpdatedAt.Equal(now) {
			t.Fatalf("succeeded=%v: outcome=%s err=%v order=%+v", succeeded, got, err, o)
		}
	}
}

func TestOrderFlowAC08_FinalOrdersNeverChange(t *testing.T) {
	o := awaiting()
	if _, err := o.ApplyPayment(domain.PaymentResult{Succeeded: true, Amount: thb(119000)}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	before := o
	if got, _ := o.ApplyPayment(domain.PaymentResult{Succeeded: true, Amount: thb(119000)}, time.Unix(2, 0)); got != domain.ResultDuplicate {
		t.Fatalf("same outcome = %s", got)
	}
	if got, _ := o.ApplyPayment(domain.PaymentResult{Succeeded: false, Amount: thb(119000)}, time.Unix(3, 0)); got != domain.ResultConflictIgnored {
		t.Fatalf("other outcome = %s", got)
	}
	if o.Status != before.Status || !o.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("final order changed: %+v", o)
	}
}

func TestOrderFlowAC09_AmountMismatchIsRejected(t *testing.T) {
	o := awaiting()
	if _, err := o.ApplyPayment(domain.PaymentResult{Succeeded: true, Amount: thb(1)}, time.Now()); !errors.Is(err, domain.ErrAmountMismatch) || o.Status != domain.AwaitingPayment {
		t.Fatalf("err = %v, status = %s", err, o.Status)
	}
}
