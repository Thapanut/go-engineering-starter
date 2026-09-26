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
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/core/domain"
	"github.com/Thapanut/go-engineering-starter/internal/core/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/service"
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

	tx, closeStore, err := newStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()

	svc := service.NewTransferService(tx, system.Clock{}, system.UUIDGenerator{})
	router := httpapi.NewRouter(httpapi.Deps{
		Transfers:      svc,
		Accounts:       svc,
		Auth:           auth.NewJWT(cfg.JWTSecret, cfg.JWTIssuer),
		Log:            log,
		RequestTimeout: cfg.RequestTimeout,
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

// newStore selects the outbound adapter. Swapping storage never touches the core.
func newStore(ctx context.Context, cfg config.Config, log *slog.Logger) (port.TxManager, func(), error) {
	switch cfg.Store {
	case config.StoreMemory:
		log.Warn("using in-memory store with demo data; not for production")
		st := memory.NewStore()
		seedDemo(st)
		return st, func() {}, nil
	default:
		pool, err := pgxpool.New(ctx, cfg.DatabaseDSN)
		if err != nil {
			return nil, nil, fmt.Errorf("postgres pool: %w", err)
		}
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			pool.Close()
			return nil, nil, fmt.Errorf("postgres ping: %w", err)
		}
		return postgres.NewTxManager(pool), pool.Close, nil
	}
}

// seedDemo mirrors migrations/dev/seed.sql (synthetic data).
func seedDemo(st *memory.Store) {
	thb := func(v int64) domain.Money { return domain.Money{Amount: v, Currency: domain.THB} }
	for _, a := range []domain.Account{
		{ID: "a1111111-1111-4111-8111-111111111111", CustomerID: "demo-alice", Balance: thb(1_000_000), Status: domain.AccountActive},
		{ID: "a2222222-2222-4222-8222-222222222222", CustomerID: "demo-alice", Balance: thb(50_000), Status: domain.AccountActive},
		{ID: "b1111111-1111-4111-8111-111111111111", CustomerID: "demo-bob", Balance: thb(200_000), Status: domain.AccountActive},
		{ID: "b2222222-2222-4222-8222-222222222222", CustomerID: "demo-bob", Balance: thb(0), Status: domain.AccountFrozen},
	} {
		st.SeedAccount(a)
	}
}
