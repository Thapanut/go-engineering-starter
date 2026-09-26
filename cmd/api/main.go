// Command api is the composition root: it reads config, picks adapters, wires them
// to the core, and manages the HTTP server lifecycle. No business logic lives here.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/inbound/httpapi"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/postgres"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
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

	st, err := newStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.close()

	// Wire features: each service gets its outbound adapters; its HTTP module goes to NewApp.
	webhooks := service.NewWebhookService(
		twoc2p.NewValidator(cfg.TwoC2PSecretKey, cfg.TwoC2PMerchantID), st.tx, system.Clock{}, log)

	app := httpapi.NewApp(httpapi.Deps{
		Auth:           auth.NewJWT(cfg.JWTSecret, cfg.JWTIssuer),
		Log:            log,
		RequestTimeout: cfg.RequestTimeout,
		Ready:          st.ready,
	},
		httpapi.WebhookModule{UseCase: webhooks},
	)

	errCh := make(chan error, 1)
	go func() {
		log.Info("server started", slog.Int("port", cfg.Port), slog.String("store", string(cfg.Store)))
		errCh <- app.Listen(fmt.Sprintf(":%d", cfg.Port))
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
	case <-ctx.Done():
		log.Info("shutting down")
		if err := app.ShutdownWithTimeout(15 * time.Second); err != nil {
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
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := postgres.Open(openCtx, cfg.DatabaseDSN, log)
	if err != nil {
		return store{}, err
	}
	return store{
		tx:    postgres.NewTxManager(db),
		ready: func(ctx context.Context) error { return postgres.Ping(ctx, db) },
		close: func() { postgres.Close(db) },
	}, nil
}
