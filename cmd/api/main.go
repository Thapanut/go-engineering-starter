// Command api is the composition root: it reads config, picks adapters, wires them
// to the core, and manages the HTTP server lifecycle. No business logic lives here.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/inbound/httpapi"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/inbound/paymentevents"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/kafka"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/catalogclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/paymentclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/postgres"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	catalogport "github.com/Thapanut/go-engineering-starter/internal/core/catalog/port"
	catalogservice "github.com/Thapanut/go-engineering-starter/internal/core/catalog/service"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	orderingservice "github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
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

	// Wire features: each service gets its outbound adapters; its HTTP module goes to
	// NewApp (JWT-protected modules as arguments, signature-authenticated ones in Public).
	webhooks := service.NewWebhookService(
		twoc2p.NewVerifier(cfg.TwoC2PSecretKey, cfg.TwoC2PMerchantID), st.tx, system.Clock{}, system.UUIDGenerator{}, log)
	// UNCONFIRMED: StubGateway stands in for the 2C2P Payment Token API (spec payment-checkout).
	checkout := service.NewCheckoutService(st.tx, twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	catalog := catalogservice.NewCatalogService(st.products)
	// Ordering reaches catalog and payment only through its client adapters (ADR-0005).
	orders := orderingservice.NewOrderService(st.orders, catalogclient.Client{Catalog: catalog},
		paymentclient.Client{Payments: checkout}, system.Clock{}, system.UUIDGenerator{})
	// Ordering learns payment outcomes from payment.status-changed: from Kafka, or
	// in process with STORE=memory and no brokers.
	paymentEvents := paymentevents.Handler{UseCase: orderingservice.NewPaymentEventService(st.orders, system.Clock{}), Log: log}

	// The outbox relay and the event consumer run until shutdown; they must stop
	// before the store closes.
	var workers sync.WaitGroup
	pub, pubKind, closePub := newPublisher(cfg, log, paymentEvents)
	if pub != nil {
		relay := service.NewOutboxRelay(st.tx, pub, system.Clock{}, log, outboxBatchSize)
		workers.Go(func() { relay.Run(ctx, cfg.OutboxPollInterval) })
	}
	closeConsumer := func() {}
	if len(cfg.KafkaBrokers) > 0 {
		consumer := paymentevents.NewConsumer(cfg.KafkaBrokers, paymentEvents)
		workers.Go(func() { consumer.Run(ctx) })
		closeConsumer = func() {
			if err := consumer.Close(); err != nil {
				log.Warn("close kafka consumer", slog.String("error", err.Error()))
			}
		}
	}
	defer func() { stop(); workers.Wait(); closeConsumer(); closePub() }()

	public := []httpapi.PublicModule{httpapi.WebhookModule{UseCase: webhooks}}
	modules := []httpapi.Module{httpapi.CatalogModule{UseCase: catalog}, httpapi.OrderModule{UseCase: orders},
		httpapi.PaymentModule{UseCase: checkout}}
	if cfg.DemoUI {
		log.Warn("DEMO_UI_ENABLED: serving the payment demo page at /demo; not for production")
		demo := httpapi.DemoModule{Events: checkout, Publisher: pubKind}
		public, modules = append(public, demo), append(modules, demo)
	}
	app := httpapi.NewApp(httpapi.Deps{
		Auth:           auth.NewJWT(cfg.JWTSecret, cfg.JWTIssuer),
		Log:            log,
		RequestTimeout: cfg.RequestTimeout,
		Ready:          st.ready,
		Public:         public,
	}, modules...)

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

// outboxBatchSize bounds how many messages one relay transaction locks and publishes.
const outboxBatchSize = 100

// newPublisher selects where outbox messages go and names the choice for the demo
// page. Without KAFKA_BROKERS the Postgres relay is disabled and events wait safely
// in the outbox until Kafka is configured.
func newPublisher(cfg config.Config, log *slog.Logger, inProcess paymentevents.Handler) (port.MessagePublisher, string, func()) {
	switch {
	case len(cfg.KafkaBrokers) > 0:
		p := kafka.NewPublisher(cfg.KafkaBrokers)
		return p, httpapi.PublisherKafka, func() {
			if err := p.Close(); err != nil {
				log.Warn("close kafka publisher", slog.String("error", err.Error()))
			}
		}
	case cfg.Store == config.StoreMemory:
		log.Warn("KAFKA_BROKERS not set; publishing events in-process only (STORE=memory)")
		deliver := func(ctx context.Context, m port.OutboxMessage) error {
			if m.Topic != paymentevents.Topic {
				return nil
			}
			return inProcess.Handle(ctx, m.Payload)
		}
		return &memory.Publisher{Deliver: deliver}, httpapi.PublisherInProcess, func() {}
	default:
		log.Warn("KAFKA_BROKERS not set; outbox relay disabled, events stay in the outbox")
		return nil, httpapi.PublisherDisabled, func() {}
	}
}

// store holds each module's outbound adapters on one database (ADR-0005).
type store struct {
	tx       port.TxManager                // payment
	products catalogport.ProductRepository // catalog
	orders   orderingport.TxManager        // ordering
	ready    func(context.Context) error
	close    func()
}

// newStore selects the outbound adapter. Swapping storage never touches the core.
func newStore(ctx context.Context, cfg config.Config, log *slog.Logger) (store, error) {
	if cfg.Store == config.StoreMemory {
		log.Warn("using in-memory store; not for production")
		return store{tx: memory.NewStore(), products: memory.SampleProducts(), orders: memory.NewOrderStore(),
			ready: func(context.Context) error { return nil }, close: func() {}}, nil
	}
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, err := postgres.Open(openCtx, cfg.DatabaseDSN, log)
	if err != nil {
		return store{}, err
	}
	return store{
		tx:       postgres.NewTxManager(db),
		products: postgres.NewProductRepository(db),
		orders:   postgres.NewOrderingTxManager(db),
		ready:    func(ctx context.Context) error { return postgres.Ping(ctx, db) },
		close:    func() { postgres.Close(db) },
	}, nil
}
