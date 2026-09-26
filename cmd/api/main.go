// Command api is the composition root: it reads config, picks adapters, wires them
// to the core, and manages the HTTP server lifecycle. No business logic lives here.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/inbound/httpapi"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/postgres"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/platform/auth"
	"github.com/Thapanut/go-engineering-starter/internal/platform/config"
	"github.com/Thapanut/go-engineering-starter/internal/platform/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := logger.New(os.Stdout, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := newStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.close()

	// Wire features here: build each service with st.tx (plus system.Clock{} and
	// system.UUIDGenerator{} as needed) and pass its HTTP module to NewRouter.
	_ = st.tx
	router := httpapi.NewRouter(httpapi.Deps{
		Auth:           auth.NewJWT(cfg.JWTSecret, cfg.JWTIssuer),
		Log:            log,
		RequestTimeout: cfg.RequestTimeout,
		Ready:          st.ready,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("server started", slog.Int("port", cfg.Port), slog.String("store", string(cfg.Store)))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}

type store struct {
	tx    port.TxManager
	ready func(context.Context) error
	close func()
}

// newStore selects the outbound adapter. Swapping storage never touches the core.
func newStore(ctx context.Context, cfg config.Config, log *slog.Logger) (store, error) {
	if cfg.Store == config.StoreMemory {
		log.Warn("using in-memory store; not for production")
		return store{tx: memory.NewStore(), ready: func(context.Context) error { return nil }, close: func() {}}, nil
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseDSN)
	if err != nil {
		return store{}, fmt.Errorf("postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return store{}, fmt.Errorf("postgres ping: %w", err)
	}
	return store{tx: postgres.NewTxManager(pool), ready: pool.Ping, close: pool.Close}, nil
}
