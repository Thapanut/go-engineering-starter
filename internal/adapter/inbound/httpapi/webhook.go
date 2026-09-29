package httpapi

import (
	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// WebhookModule exposes payment-provider webhooks. The routes are public because
// the provider authenticates with a signature, which the use case verifies.
type WebhookModule struct {
	UseCase port.WebhookUseCase
}

var _ PublicModule = WebhookModule{}

// RegisterPublic adds POST /webhooks/2c2p.
func (m WebhookModule) RegisterPublic(r fiber.Router) {
	r.Post("/webhooks/2c2p", m.receive2c2p)
}

type webhookAck struct {
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
}

// receive2c2p acknowledges quickly: the use case does one locked read and at most
// one update. c.Body() is only valid during this call; the use case does not keep it.
func (m WebhookModule) receive2c2p(c *fiber.Ctx) error {
	res, err := m.UseCase.HandlePaymentNotification(c.UserContext(), c.Body())
	if err != nil {
		return err
	}
	return c.JSON(webhookAck{Status: "OK", Outcome: string(res.Outcome)})
}
