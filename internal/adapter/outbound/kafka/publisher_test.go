package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
)

// Synthetic test data only.
func TestToKafkaMapsTopicKeyValueAndHeaders(t *testing.T) {
	msgs := []port.OutboxMessage{{ID: "evt-1", Topic: "payments.v1.status-changed", Key: "pay-1", Payload: []byte(`{"a":1}`)}}
	got := toKafka(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	m := got[0]
	if m.Topic != "payments.v1.status-changed" || string(m.Key) != "pay-1" || string(m.Value) != `{"a":1}` {
		t.Fatalf("message = %+v", m)
	}
	headers := map[string]string{}
	for _, h := range m.Headers {
		headers[h.Key] = string(h.Value)
	}
	if headers["content-type"] != "application/json" || headers["event_id"] != "evt-1" {
		t.Fatalf("headers = %v", headers)
	}
}

func TestPublishNothingDoesNotDial(t *testing.T) {
	p := NewPublisher([]string{"127.0.0.1:1"}) // nothing listens here
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Publish(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPublishReportsUnreachableBroker(t *testing.T) {
	p := NewPublisher([]string{"127.0.0.1:1"})
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := p.Publish(ctx, []port.OutboxMessage{{ID: "evt-1", Topic: "t", Key: "k", Payload: []byte("{}")}})
	if err == nil {
		t.Fatal("want an error when no broker is reachable")
	}
}
