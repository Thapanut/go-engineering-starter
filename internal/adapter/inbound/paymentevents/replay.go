package paymentevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// ReplayGroupID is the consumer group the replay reads the DLQ in.
const ReplayGroupID = "ordering-dlq-replay"

// ReplayOptions control one replay run (spec ordering-consumer-dlq, cmd/dlqreplay).
type ReplayOptions struct {
	DryRun   bool          // list only; publish and commit nothing
	Limit    int           // stop after this many matching messages
	EventID  string        // only this event; never commits DLQ offsets
	Wait     time.Duration // stop after this long without a message
	Operator string        // written to the audit line
	// EndOffsets is the DLQ's end offset per partition when the run started. The
	// run stops at it, so a replayed message that is dead-lettered again during
	// the run is left for the next run instead of looping.
	EndOffsets map[int]int64
}

// ReplayResult counts what a run did.
type ReplayResult struct {
	Seen, Matched, Replayed int
}

// replayAudit is one JSON line per matching DLQ message (AC-11, AC-15). It carries
// ids and positions only, never payload values.
type replayAudit struct {
	Action            string `json:"action"` // "would-replay" or "replayed"
	Operator          string `json:"operator"`
	EventID           string `json:"event_id"`
	OriginalTopic     string `json:"original_topic"`
	OriginalPartition string `json:"original_partition"`
	OriginalOffset    string `json:"original_offset"`
	Reason            string `json:"reason"`
	DLQOffset         int64  `json:"dlq_offset"`
	TargetTopic       string `json:"target_topic,omitempty"`
	ReplayCount       int    `json:"replay_count"`
	At                string `json:"at"`
}

// Replay reads the DLQ from r and, unless DryRun, republishes each matching
// message to RetryTopic through w with byte-identical key and value, its
// original headers, and dlq-replay-count + 1. Without EventID it commits each
// DLQ offset after the retry write is acknowledged.
func Replay(ctx context.Context, r reader, w writer, opt ReplayOptions, audit io.Writer) (ReplayResult, error) {
	var res ReplayResult
	enc := json.NewEncoder(audit)
	for opt.Limit <= 0 || res.Matched < opt.Limit {
		fctx, cancel := context.WithTimeout(ctx, opt.Wait)
		m, err := r.FetchMessage(fctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return res, nil // caught up
		}
		if err != nil {
			return res, fmt.Errorf("read DLQ: %w", err)
		}
		if m.Offset >= opt.EndOffsets[m.Partition] {
			return res, nil // reached what was dead-lettered before this run; not committed
		}
		res.Seen++
		eventID := header(m, "event_id")
		if opt.EventID != "" && eventID != opt.EventID {
			continue
		}
		res.Matched++
		count, _ := strconv.Atoi(header(m, HeaderReplayCount))
		line := replayAudit{Action: "would-replay", Operator: opt.Operator, EventID: eventID,
			OriginalTopic: header(m, HeaderOriginalTopic), OriginalPartition: header(m, HeaderOriginalPartition),
			OriginalOffset: header(m, HeaderOriginalOffset), Reason: header(m, HeaderReason),
			DLQOffset: m.Offset, ReplayCount: count + 1}
		if !opt.DryRun {
			headers := append(withoutDLQHeaders(m.Headers),
				kafkago.Header{Key: HeaderReplayCount, Value: []byte(strconv.Itoa(count + 1))})
			if err := w.WriteMessages(ctx, kafkago.Message{Topic: RetryTopic, Key: m.Key, Value: m.Value, Headers: headers}); err != nil {
				return res, fmt.Errorf("write retry topic (event %s): %w", eventID, err)
			}
			if opt.EventID == "" {
				if err := r.CommitMessages(ctx, m); err != nil {
					return res, fmt.Errorf("commit DLQ offset %d: %w", m.Offset, err)
				}
			}
			res.Replayed++
			line.Action, line.TargetTopic = "replayed", RetryTopic
		}
		line.At = time.Now().UTC().Format(time.RFC3339Nano)
		if err := enc.Encode(line); err != nil {
			return res, fmt.Errorf("write audit line: %w", err)
		}
	}
	return res, nil
}

// DLQEndOffsets returns the DLQ's current end offset per partition, for
// ReplayOptions.EndOffsets.
func DLQEndOffsets(ctx context.Context, broker string) (map[int]int64, error) {
	conn, err := kafkago.DialContext(ctx, "tcp", broker)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", broker, err)
	}
	defer func() { _ = conn.Close() }()
	parts, err := conn.ReadPartitions(DLQTopic)
	if err != nil {
		return nil, fmt.Errorf("read DLQ partitions: %w", err)
	}
	ends := make(map[int]int64, len(parts))
	for _, p := range parts {
		lc, err := kafkago.DialLeader(ctx, "tcp", broker, DLQTopic, p.ID)
		if err != nil {
			return nil, fmt.Errorf("dial DLQ partition %d leader: %w", p.ID, err)
		}
		end, err := lc.ReadLastOffset()
		_ = lc.Close()
		if err != nil {
			return nil, fmt.Errorf("read DLQ partition %d end offset: %w", p.ID, err)
		}
		ends[p.ID] = end
	}
	return ends, nil
}
