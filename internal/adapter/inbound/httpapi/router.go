// Package httpapi is the driving HTTP adapter (Gin). It translates HTTP to inbound
// port calls and domain errors to the contract in contracts/openapi.yaml.
// It contains no business rules.
package httpapi

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Thapanut/go-engineering-starter/internal/core/port"
)

// Authenticator turns a bearer token into a customer id.
type Authenticator interface {
	Authenticate(token string) (customerID string, err error)
}

// Deps are the collaborators of the HTTP adapter.
type Deps struct {
	Transfers      port.TransferUseCase
	Accounts       port.AccountQuery
	Auth           Authenticator
	Log            *slog.Logger
	RequestTimeout time.Duration
}

// NewRouter builds the Gin engine with middleware and routes.
func NewRouter(d Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.HandleMethodNotAllowed = false
	r.Use(traceID(), accessLog(d.Log), recovery(d.Log), requestTimeout(d.RequestTimeout), limitBody(maxBodyBytes))
	r.NoRoute(func(c *gin.Context) { writeError(c, d.Log, errRouteNotFound) })

	h := &handlers{transfers: d.Transfers, accounts: d.Accounts, log: d.Log}
	r.GET("/healthz", h.health)

	v1 := r.Group("/v1", authenticate(d.Auth, d.Log)) // deny by default for everything under /v1
	v1.GET("/accounts/:accountId", h.getAccount)
	v1.POST("/transfers", h.createTransfer)
	v1.GET("/transfers/:transferId", h.getTransfer)
	return r
}
