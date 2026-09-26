package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

const (
	headerTraceID   = "X-Trace-Id"
	localTraceID    = "traceID"
	localCustomerID = "customerID"
	localUnmatched  = "unmatched"
	maxBodyBytes    = 16 << 10 // 16 KiB
)

// Only accept well-formed incoming trace ids, so clients cannot inject into logs.
var traceIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func traceID() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := utils.CopyString(c.Get(headerTraceID)) // fasthttp reuses header memory
		if !traceIDRe.MatchString(id) {
			id = newTraceID()
		}
		c.Locals(localTraceID, id)
		c.Set(headerTraceID, id)
		return c.Next()
	}
}

func newTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// traceIDOf returns the request's trace id. Requests rejected by fasthttp before
// any middleware ran (e.g. body over BodyLimit) get one generated here.
func traceIDOf(c *fiber.Ctx) string {
	if id, ok := c.Locals(localTraceID).(string); ok {
		return id
	}
	id := newTraceID()
	c.Locals(localTraceID, id)
	c.Set(headerTraceID, id)
	return id
}

// customerIDOf returns the authenticated customer id set by the auth middleware.
func customerIDOf(c *fiber.Ctx) string {
	id, _ := c.Locals(localCustomerID).(string)
	return id
}

// accessLog writes one line per request with the route template, never the raw
// path, body, query, or headers, so ids and tokens stay out of logs.
func accessLog(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		if err := c.Next(); err != nil {
			// Render the error here so the logged status is the one the client gets.
			if herr := c.App().ErrorHandler(c, err); herr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}
		route := c.Route().Path
		if unmatched, _ := c.Locals(localUnmatched).(bool); unmatched {
			route = "unmatched"
		}
		log.LogAttrs(c.UserContext(), slog.LevelInfo, "http_request",
			slog.String("method", c.Method()),
			slog.String("route", route),
			slog.Int("status", c.Response().StatusCode()),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
			slog.String("trace_id", traceIDOf(c)),
		)
		return nil
	}
}

func recovery(log *slog.Logger) fiber.Handler {
	return func(c *fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", slog.String("panic", fmt.Sprint(r)), slog.String("trace_id", traceIDOf(c)))
				err = errInternal
			}
		}()
		return c.Next()
	}
}

// requestTimeout bounds downstream work: handlers must pass c.UserContext() to ports.
func requestTimeout(d time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.UserContext(), d)
		defer cancel()
		c.SetUserContext(ctx)
		return c.Next()
	}
}

func authenticate(a Authenticator) fiber.Handler {
	return func(c *fiber.Ctx) error {
		scheme, token, ok := strings.Cut(c.Get(fiber.HeaderAuthorization), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return errUnauthorized
		}
		customerID, err := a.Authenticate(token)
		if err != nil {
			return errUnauthorized
		}
		c.Locals(localCustomerID, customerID)
		return c.Next()
	}
}
