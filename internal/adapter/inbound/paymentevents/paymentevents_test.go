package paymentevents

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/memory"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/catalogclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/ordering/paymentclient"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/system"
	"github.com/Thapanut/go-engineering-starter/internal/adapter/outbound/twoc2p"
	catalogservice "github.com/Thapanut/go-engineering-starter/internal/core/catalog/service"
	ordering "github.com/Thapanut/go-engineering-starter/internal/core/ordering/domain"
	orderingport "github.com/Thapanut/go-engineering-starter/internal/core/ordering/port"
	orderingservice "github.com/Thapanut/go-engineering-starter/internal/core/ordering/service"
	paymentport "github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	paymentservice "github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
	"github.com/Thapanut/go-engineering-starter/internal/kernel"
)

// Synthetic test data only.
const validEvent = `{"event_id":"7f0c2b1e-5d4a-4e8b-9a61-3c2d1e0f9a8b","payment_id":"p1","order_id":"o1",` +
	`"invoice_no":"INV1","status":"SUCCESS","amount":"1190.00","amount_minor":119000,"currency":"THB",` +
	`"provider_ref":"tr","occurred_at":"2026-09-29T10:00:00Z"}`

type recordingUseCase struct {
	mu   sync.Mutex
	got  []orderingport.PaymentStatusChanged
	errs []error // returned in order, then nil
}

func (r *recordingUseCase) HandlePaymentStatusChanged(_ context.Context, e orderingport.PaymentStatusChanged) (orderingport.EventOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, e)
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		return "", err
	}
	return orderingport.EventApplied, nil
}

func newHandler(uc orderingport.PaymentEventHandler) (Handler, *bytes.Buffer) {
	var logs bytes.Buffer
	return Handler{UseCase: uc, Log: slog.New(slog.NewJSONHandler(&logs, nil))}, &logs
}

func TestHandleDecodesTheContractPayload(t *testing.T) {
	uc := &recordingUseCase{}
	h, logs := newHandler(uc)
	if err := h.Handle(context.Background(), []byte(validEvent)); err != nil {
		t.Fatal(err)
	}
	want := orderingport.PaymentStatusChanged{EventID: "7f0c2b1e-5d4a-4e8b-9a61-3c2d1e0f9a8b", OrderID: "o1", Succeeded: true,
		Amount: kernel.Money{Amount: 119000, Currency: kernel.THB}}
	if len(uc.got) != 1 || uc.got[0] != want {
		t.Fatalf("got %+v", uc.got)
	}
	if strings.Contains(logs.String(), "INV1") {
		t.Fatalf("invoice number logged: %s", logs)
	}
}

func TestHandleSkipsMalformedMessages(t *testing.T) {
	uc := &recordingUseCase{}
	h, logs := newHandler(uc)
	for _, bad := range []string{`not json`, `{"status":"SUCCESS","amount_minor":1,"currency":"THB"}`,
		strings.Replace(validEvent, `"SUCCESS"`, `"PENDING"`, 1), strings.Replace(validEvent, `"amount_minor":119000,`, ``, 1)} {
		if err := h.Handle(context.Background(), []byte(bad)); err != nil {
			t.Fatalf("%s: err = %v, want nil (skip)", bad, err)
		}
	}
	if len(uc.got) != 0 || !strings.Contains(logs.String(), "dropping malformed payment event") {
		t.Fatalf("got %+v, logs %s", uc.got, logs)
	}
}

func TestHandleReturnsRetryableErrors(t *testing.T) {
	boom := errors.New("db down")
	h, _ := newHandler(&recordingUseCase{errs: []error{boom}})
	if err := h.Handle(context.Background(), []byte(validEvent)); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// fakeReader serves msgs once, then blocks until ctx is cancelled.
type fakeReader struct {
	mu        sync.Mutex
	msgs      []kafkago.Message
	committed []kafkago.Message
}

func (f *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	f.mu.Lock()
	if len(f.msgs) > 0 {
		m := f.msgs[0]
		f.msgs = f.msgs[1:]
		f.mu.Unlock()
		return m, nil
	}
	f.mu.Unlock()
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (f *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.committed = append(f.committed, msgs...)
	return nil
}

func (f *fakeReader) Close() error { return nil }

func TestOrderFlowAC12_OffsetCommittedOnlyAfterSuccess(t *testing.T) {
	uc := &recordingUseCase{errs: []error{errors.New("db down"), errors.New("still down")}}
	h, _ := newHandler(uc)
	r := &fakeReader{msgs: []kafkago.Message{{Offset: 7, Value: []byte(validEvent)}}}
	c := newConsumer(r, h, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.committed)
		r.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if len(r.committed) != 1 || r.committed[0].Offset != 7 || len(uc.got) != 3 {
		t.Fatalf("committed=%v attempts=%d", r.committed, len(uc.got))
	}
}

// AC-11: with STORE=memory the in-process publisher carries payment's event to
// ordering, so a webhook turns the order PAID without Kafka.
func TestOrderFlowAC11_InProcessDeliveryConfirmsTheOrder(t *testing.T) {
	ctx := context.Background()
	payStore, orderStore := memory.NewStore(), memory.NewOrderStore()
	checkout := paymentservice.NewCheckoutService(payStore, twoc2p.StubGateway{}, system.Clock{}, system.UUIDGenerator{})
	orders := orderingservice.NewOrderService(orderStore,
		catalogclient.Client{Catalog: catalogservice.NewCatalogService(memory.SampleProducts())},
		paymentclient.Client{Payments: checkout}, system.Clock{}, system.UUIDGenerator{})
	h, _ := newHandler(orderingservice.NewPaymentEventService(orderStore, system.Clock{}))
	pub := &memory.Publisher{Deliver: func(ctx context.Context, m paymentport.OutboxMessage) error { return h.Handle(ctx, m.Payload) }}
	relay := paymentservice.NewOutboxRelay(payStore, pub, system.Clock{}, h.Log, 10)

	placed, err := orders.PlaceOrder(ctx, orderingport.PlaceOrderCommand{CustomerID: "cust",
		Items: []ordering.Item{{ProductID: "CERAMIC-MUG", Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("test-2c2p-secret-key-0123456789abcdef")
	webhooks := paymentservice.NewWebhookService(twoc2p.NewVerifier(secret, "JT04"), payStore, system.Clock{}, system.UUIDGenerator{}, h.Log)
	body, _ := twoc2p.Sign(secret, []byte(`{"merchantID":"JT04","invoiceNo":"`+placed.Payment.InvoiceNo+
		`","amount":"290.00","currencyCode":"THB","tranRef":"tr-1","respCode":"0000"}`))
	if _, err := webhooks.HandlePaymentNotification(ctx, body); err != nil {
		t.Fatal(err)
	}
	if o, _ := orders.GetOrder(ctx, "cust", placed.Order.ID); o.Status != ordering.AwaitingPayment {
		t.Fatalf("order changed before the event was relayed: %s", o.Status)
	}
	if n, err := relay.RelayOnce(ctx); err != nil || n != 1 {
		t.Fatalf("relayed %d, err %v", n, err)
	}
	if o, _ := orders.GetOrder(ctx, "cust", placed.Order.ID); o.Status != ordering.Paid {
		t.Fatalf("order status = %s, want PAID", o.Status)
	}
}
