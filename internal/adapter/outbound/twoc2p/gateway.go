package twoc2p

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// StubGateway implements port.PaymentGateway without calling 2C2P. It stands in
// for the Payment Token API until that integration is specified (spec
// payment-checkout, open questions). Its checkout URL uses the reserved .invalid
// TLD so it can never be mistaken for, or resolve to, a real payment page.
type StubGateway struct{}

var _ port.PaymentGateway = StubGateway{}

// CreateSession returns a random stub token and a checkout URL built from it.
func (StubGateway) CreateSession(ctx context.Context, _ domain.Payment) (port.PaymentSession, error) {
	if err := ctx.Err(); err != nil {
		return port.PaymentSession{}, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return port.PaymentSession{}, fmt.Errorf("stub payment token: %w", err)
	}
	token := "stub_" + hex.EncodeToString(b[:])
	return port.PaymentSession{Token: token, CheckoutURL: "https://checkout.2c2p.invalid/payment/" + token}, nil
}
