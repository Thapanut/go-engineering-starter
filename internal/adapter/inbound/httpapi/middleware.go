package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	headerTraceID = "X-Trace-Id"
	ctxTraceID    = "traceID"
	ctxCustomerID = "customerID"
	maxBodyBytes  = 16 << 10 // 16 KiB (spec §6)
)

// Only accept well-formed incoming trace ids, so clients cannot inject into logs.
var traceIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func traceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerTraceID)
		if !traceIDRe.MatchString(id) {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		c.Set(ctxTraceID, id)
		c.Header(headerTraceID, id)
		c.Next()
	}
}

func traceIDOf(c *gin.Context) string { return c.GetString(ctxTraceID) }

func customerIDOf(c *gin.Context) string { return c.GetString(ctxCustomerID) }

// accessLog writes one line per request with the route template, never the raw
// path, body, query, or headers, so ids and tokens stay out of logs (spec AC-16).
func accessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		log.LogAttrs(c.Request.Context(), slog.LevelInfo, "http_request",
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", c.Writer.Status()),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
			slog.String("trace_id", traceIDOf(c)),
		)
	}
}

func recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("panic recovered", slog.Any("panic", r), slog.String("trace_id", traceIDOf(c)))
				writeError(c, log, errInternal)
			}
		}()
		c.Next()
	}
}

func requestTimeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func limitBody(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

func authenticate(a Authenticator, log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		scheme, token, ok := strings.Cut(c.GetHeader("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			writeError(c, log, errUnauthorized)
			return
		}
		customerID, err := a.Authenticate(token)
		if err != nil {
			writeError(c, log, errUnauthorized)
			return
		}
		c.Set(ctxCustomerID, customerID)
		c.Next()
	}
}
