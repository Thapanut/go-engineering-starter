package httpapi

import (
	_ "embed" // demo page
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

//go:embed demo/index.html
var demoPage []byte

// demoCSP allows only the page's own inline script/style and calls back to this
// origin (the API and the webhook).
const demoCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; " +
	"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Where the outbox relay sends events, as reported to the demo page.
const (
	PublisherKafka     = "kafka"
	PublisherInProcess = "in-process"
	PublisherDisabled  = "disabled"
)

// DemoModule serves the local payment demo (spec payment-checkout AC-10, AC-11,
// AC-13). cmd/api registers it only when DEMO_UI_ENABLED=true, as a PublicModule
// (the static page) and as a Module (a JWT-protected debug view of a payment's
// outbox events). The page carries no secrets: `make demo` hands the dev JWT and
// 2C2P sandbox key to the browser in the URL fragment, which is never sent to the server.
type DemoModule struct {
	Events    port.PaymentEventsUseCase
	Publisher string // PublisherKafka, PublisherInProcess, or PublisherDisabled
}

var (
	_ PublicModule = DemoModule{}
	_ Module       = DemoModule{}
)

// RegisterPublic adds GET /demo and GET /demo/return (the 2C2P frontend return URL).
func (DemoModule) RegisterPublic(r fiber.Router) {
	r.Get("/demo", serveDemoPage)
	r.Get("/demo/return", serveDemoPage)
}

// Register adds GET /v1/demo/payments/:invoiceNo/events.
func (m DemoModule) Register(v1 fiber.Router) {
	v1.Get("/demo/payments/:invoiceNo/events", m.events)
}

func serveDemoPage(c *fiber.Ctx) error {
	c.Set(fiber.HeaderContentSecurityPolicy, demoCSP)
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderReferrerPolicy, "no-referrer")
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Type("html", "utf-8")
	return c.Send(demoPage)
}

type demoEvent struct {
	EventID     string          `json:"eventId"`
	Topic       string          `json:"topic"`
	Key         string          `json:"key"`
	CreatedAt   string          `json:"createdAt"`
	Status      string          `json:"status"`      // PENDING, PUBLISHED, or FAILED (parked)
	PublishedAt *string         `json:"publishedAt"` // null until the relay has published it
	Payload     json.RawMessage `json:"payload"`
}

type demoEventsResponse struct {
	Publisher string      `json:"publisher"`
	Events    []demoEvent `json:"events"`
}

func (m DemoModule) events(c *fiber.Ctx) error {
	recs, err := m.Events.ListPaymentEvents(c.UserContext(), customerIDOf(c), c.Params("invoiceNo"))
	if err != nil {
		return err
	}
	out := demoEventsResponse{Publisher: m.Publisher, Events: make([]demoEvent, len(recs))}
	for i, r := range recs {
		e := demoEvent{EventID: r.Message.ID, Topic: r.Message.Topic, Key: r.Message.Key, Status: string(r.Status),
			CreatedAt: r.Message.CreatedAt.UTC().Format(time.RFC3339Nano), Payload: r.Message.Payload}
		if !r.PublishedAt.IsZero() {
			at := r.PublishedAt.UTC().Format(time.RFC3339Nano)
			e.PublishedAt = &at
		}
		out.Events[i] = e
	}
	return c.JSON(out)
}
