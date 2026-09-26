// Package httpapi is the driving HTTP adapter (Fiber v2, ADR-0003). It translates
// HTTP to inbound port calls and domain errors to the contract in
// contracts/openapi.yaml. It contains no business rules.
//
// Fiber runs on fasthttp, which reuses request memory after the handler returns:
// never keep strings from c.Params/c.Get/c.Body beyond the request without
// copying them (utils.CopyString).
package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Authenticator turns a bearer token into a customer id.
type Authenticator interface {
	Authenticate(token string) (customerID string, err error)
}

// Module registers one feature's routes under the authenticated /v1 group.
// Each feature adds a handler type in this package that holds its inbound port
// and implements Module; cmd/api passes it to NewApp.
type Module interface {
	Register(v1 fiber.Router)
}

// PublicModule is an optional interface for modules that also expose routes
// outside the JWT-protected /v1 group, e.g. provider webhooks that authenticate
// by signature. Use it sparingly: every public route must authenticate itself.
type PublicModule interface {
	RegisterPublic(r fiber.Router)
}

// Deps are the collaborators of the HTTP adapter.
type Deps struct {
	Auth           Authenticator
	Log            *slog.Logger
	RequestTimeout time.Duration
	// Ready reports whether dependencies (e.g. the database) are reachable.
	Ready func(ctx context.Context) error
}

// NewApp builds the Fiber app with middleware, probes, and feature modules.
func NewApp(d Deps, modules ...Module) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "api",
		DisableStartupMessage: true,
		BodyLimit:             maxBodyBytes,
		ReadTimeout:           10 * time.Second,
		WriteTimeout:          d.RequestTimeout + 5*time.Second,
		IdleTimeout:           60 * time.Second,
		ErrorHandler:          errorHandler(d.Log),
	})
	app.Use(traceID(), accessLog(d.Log), recovery(d.Log), requestTimeout(d.RequestTimeout))

	app.Get("/healthz", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"status": "ok"}) })
	app.Get("/readyz", func(c *fiber.Ctx) error {
		if d.Ready != nil {
			if err := d.Ready(c.UserContext()); err != nil {
				d.Log.Warn("readiness check failed", slog.String("error", err.Error()), slog.String("trace_id", traceIDOf(c)))
				return errNotReady
			}
		}
		return c.JSON(fiber.Map{"status": "ready"})
	})

	for _, m := range modules {
		if pm, ok := m.(PublicModule); ok {
			pm.RegisterPublic(app)
		}
	}
	v1 := app.Group("/v1", authenticate(d.Auth)) // deny by default for everything under /v1
	for _, m := range modules {
		m.Register(v1)
	}

	// Catch-all: anything not matched above gets the contract's error schema.
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(localUnmatched, true)
		return errRouteNotFound
	})
	return app
}
