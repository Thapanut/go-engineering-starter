//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Thapanut/go-engineering-starter/internal/core/payment/port"
	"github.com/Thapanut/go-engineering-starter/internal/core/payment/service"
)

// Spec outbox-attempt-tracking on PostgreSQL.

type attemptRowModel struct {
	ID            string
	Status        string
	Attempts      int
	LastError     *string
	LastAttemptAt *time.Time
	PublishedAt   *time.Time
}

func attemptRows(t *testing.T, m *TxManager) map[string]attemptRowModel {
	t.Helper()
	var rows []attemptRowModel
	if err := m.db.Raw(`SELECT id, status, attempts, last_error, last_attempt_at, published_at FROM outbox`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]attemptRowModel{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func evt(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

// scriptedPublisher fails the ids in fail and accepts the rest.
type scriptedPublisher struct{ fail map[string]error }

func (p scriptedPublisher) Publish(_ context.Context, msgs []port.OutboxMessage) error {
	failed := map[string]error{}
	for _, m := range msgs {
		if err, ok := p.fail[m.ID]; ok {
			failed[m.ID] = err
		}
	}
	if len(failed) > 0 {
		return &port.PublishError{Failed: failed}
	}
	return nil
}

func TestIntegration_OutboxAttempts_PartialFailureAndParking(t *testing.T) {
	m, db := setup(t)
	applyMigrations(t, db)
	seedEvents(t, m, 3)
	tooLarge := fmt.Errorf("%w: kafka: message too large", port.ErrPermanentPublish)
	relay := service.NewOutboxRelay(m, scriptedPublisher{fail: map[string]error{evt(2): tooLarge}}, stepClock{}, discardLog(), 100)

	if n, err := relay.RelayOnce(context.Background()); n != 2 || err == nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rows := attemptRows(t, m)
	if rows[evt(1)].Status != "PUBLISHED" || rows[evt(1)].PublishedAt == nil || rows[evt(3)].Status != "PUBLISHED" {
		t.Fatalf("published rows = %+v", rows)
	}
	if r := rows[evt(2)]; r.Status != "PENDING" || r.Attempts != 1 || r.LastError == nil || r.LastAttemptAt == nil {
		t.Fatalf("failed row = %+v", r)
	}
	for range service.MaxPublishAttempts - 1 {
		_, _ = relay.RelayOnce(context.Background())
	}
	if r := attemptRows(t, m)[evt(2)]; r.Status != "FAILED" || r.Attempts != service.MaxPublishAttempts {
		t.Fatalf("parked row = %+v", r)
	}
	if n, err := relay.RelayOnce(context.Background()); n != 0 || err != nil {
		t.Fatalf("parked row claimed again: n=%d err=%v", n, err)
	}
	// AC-10: Admin re-queue, then it is published with the same id.
	if err := db.Exec(`UPDATE outbox SET status = 'PENDING', attempts = 0 WHERE id = ? AND status = 'FAILED'`, evt(2)).Error; err != nil {
		t.Fatal(err)
	}
	ok := service.NewOutboxRelay(m, scriptedPublisher{}, stepClock{}, discardLog(), 100)
	if n, err := ok.RelayOnce(context.Background()); n != 1 || err != nil || attemptRows(t, m)[evt(2)].Status != "PUBLISHED" {
		t.Fatalf("requeued: n=%d err=%v", n, err)
	}
}

func TestIntegration_OutboxAttempts_TransientNeverParks(t *testing.T) {
	m, db := setup(t)
	applyMigrations(t, db)
	seedEvents(t, m, 1)
	relay := service.NewOutboxRelay(m, scriptedPublisher{fail: map[string]error{evt(1): errors.New("NOT_ENOUGH_REPLICAS")}},
		stepClock{}, discardLog(), 100)
	for range service.MaxPublishAttempts + 2 {
		_, _ = relay.RelayOnce(context.Background())
	}
	if r := attemptRows(t, m)[evt(1)]; r.Status != "PENDING" || r.Attempts != service.MaxPublishAttempts+2 {
		t.Fatalf("row = %+v", r)
	}
}

// AC-11: 0008 backfills status from published_at, and down/up round-trips.
func TestIntegration_OutboxAttempts_MigrationBackfillsStatus(t *testing.T) {
	m, db := setup(t)
	applyMigrations(t, db)
	seedEvents(t, m, 2)
	if err := db.Exec(`UPDATE outbox SET status = 'PUBLISHED', published_at = now() WHERE id = ?`, evt(1)).Error; err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"0008_outbox_status_attempts.down.sql", "0008_outbox_status_attempts.up.sql"} {
		execFile(t, db, f)
	}
	rows := attemptRows(t, m)
	if rows[evt(1)].Status != "PUBLISHED" || rows[evt(2)].Status != "PENDING" || rows[evt(2)].Attempts != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

type stepClock struct{}

func (stepClock) Now() time.Time { return time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC) }

func discardLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

func execFile(t *testing.T, db *gorm.DB, f string) {
	t.Helper()
	sql, err := os.ReadFile("../../../../migrations/" + f)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(sql)).Error; err != nil {
		t.Fatalf("apply %s: %v", f, err)
	}
}
