// Command dlqreplay lists or replays the ordering consumer's dead-lettered payment
// events (spec ordering-consumer-dlq). It is run by an Admin; in production with the
// Admin Kafka principal, the only one allowed to produce to the retry topic.
//
// By default it is a dry run: it prints one JSON audit line per DLQ message and
// changes nothing. With -dry-run=false it republishes each message, byte for byte,
// to payments.v1.status-changed.ordering.retry (read only by the ordering group) and
// commits its DLQ offset. With -event-id it replays that event only and commits no
// offset. A run covers only what was in the DLQ when it started, so a replayed
// message that is dead-lettered again waits for the next run. Ordering deduplicates
// on event_id, so replaying twice is harmless.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/Thapanut/go-engineering-starter/internal/adapter/inbound/paymentevents"
)

func main() {
	brokers := flag.String("brokers", os.Getenv("KAFKA_BROKERS"), "comma-separated bootstrap brokers (default KAFKA_BROKERS)")
	dryRun := flag.Bool("dry-run", true, "list only; publish and commit nothing")
	limit := flag.Int("limit", 100, "stop after this many matching messages (0 = no limit)")
	eventID := flag.String("event-id", "", "replay only this event id (commits no DLQ offset)")
	wait := flag.Duration("wait", 10*time.Second, "stop after this long without a new DLQ message")
	flag.Parse()

	list := strings.Split(*brokers, ",")
	if strings.TrimSpace(*brokers) == "" {
		fail("set -brokers or KAFKA_BROKERS")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ends, err := paymentevents.DLQEndOffsets(ctx, list[0])
	if err != nil {
		fail(err.Error())
	}
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     list,
		GroupID:     paymentevents.ReplayGroupID,
		Topic:       paymentevents.DLQTopic,
		MinBytes:    1,
		MaxBytes:    1 << 20,
		MaxWait:     500 * time.Millisecond,
		StartOffset: kafkago.FirstOffset,
	})
	w := paymentevents.NewWriter(list)
	defer func() { _ = r.Close(); _ = w.Close() }()

	res, err := paymentevents.Replay(ctx, r, w, paymentevents.ReplayOptions{
		DryRun: *dryRun, Limit: *limit, EventID: *eventID, Wait: *wait, Operator: operator(), EndOffsets: ends,
	}, os.Stdout)
	mode := "dry run: nothing published"
	if !*dryRun {
		mode = fmt.Sprintf("replayed %d to %s", res.Replayed, paymentevents.RetryTopic)
	}
	fmt.Fprintf(os.Stderr, "dlqreplay: read %d, matched %d, %s\n", res.Seen, res.Matched, mode)
	if err != nil {
		fail(err.Error())
	}
}

func operator() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "dlqreplay:", msg)
	os.Exit(1)
}
