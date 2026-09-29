package twoc2p

import (
	"context"
	"regexp"
	"testing"

	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
)

func TestStubGatewayIssuesUniqueStubSessions(t *testing.T) {
	tokenRe := regexp.MustCompile(`^stub_[0-9a-f]{32}$`)
	a, err := StubGateway{}.CreateSession(context.Background(), domain.Payment{InvoiceNo: "INV1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := StubGateway{}.CreateSession(context.Background(), domain.Payment{InvoiceNo: "INV1"})
	if !tokenRe.MatchString(a.Token) || a.Token == b.Token || a.CheckoutURL != "https://checkout.2c2p.invalid/payment/"+a.Token {
		t.Fatalf("sessions = %+v, %+v", a, b)
	}
}

func TestStubGatewayHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (StubGateway{}).CreateSession(ctx, domain.Payment{}); err == nil {
		t.Fatal("want error for a cancelled context")
	}
}
