// Package httpapi is the driving HTTP adapter (Gin). It translates HTTP to inbound
// port calls and domain errors to the contract in contracts/openapi.yaml.
// It contains no business rules.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Authenticator turns a bearer token into a customer id.
type Authenticator interface {
	Authenticate(token string) (customerID string, err error)
}

// Module registers one feature's routes under the authenticated /v1 group.
// Each feature adds a handler type in this package that holds its inbound port
// and implements Module; cmd/api passes it to NewRouter.
type Module interface {
	Register(v1 *gin.RouterGroup)
}

// Deps are the collaborators of the HTTP adapter.
type Deps struct {
	Auth           Authenticator
	Log            *slog.Logger
	RequestTimeout time.Duration
	// Ready reports whether dependencies (e.g. the database) are reachable.
	Ready func(ctx context.Context) error
}

// NewRouter builds the Gin engine with middleware, probes, and feature modules.
func NewRouter(d Deps, modules ...Module) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(traceID(), accessLog(d.Log), recovery(d.Log), requestTimeout(d.RequestTimeout), limitBody(maxBodyBytes))
	r.NoRoute(func(c *gin.Context) { writeError(c, d.Log, errRouteNotFound) })

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/readyz", func(c *gin.Context) {
		if d.Ready != nil {
			if err := d.Ready(c.Request.Context()); err != nil {
				d.Log.Warn("readiness check failed", slog.String("error", err.Error()), slog.String("trace_id", traceIDOf(c)))
				writeError(c, d.Log, errNotReady)
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	v1 := r.Group("/v1", authenticate(d.Auth, d.Log)) // deny by default for everything under /v1
	for _, m := range modules {
		m.Register(v1)
	}
	return r
}
