// cmd/reminder-scheduler is the local-dev ticker loop sending T-24h/T-2h
// reminder one-shots (docs/plan.md Phase 9). This is a FAKE provider —
// "sending" a reminder means logging it and recording reminders_sent, the
// same honesty-over-completeness pattern internal/payment.FakeProvider
// established: there is no real email/SMS integration, and the interface
// boundary is exactly where a real one would plug in.
//
// moto-server is control-plane only (Phase 7's documented emulator gap:
// "creates schedules but never fires them") — a real deployment would use
// EventBridge Scheduler's per-ticket one-shot schedules, not a poll loop.
// This ticker is the honest local stand-in, matching cmd/hold-reaper's own
// "active release is UX-only, a ticker suffices locally" reasoning.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/config"
	"ticketing/internal/db"
)

const (
	reminderT24h = 24 * time.Hour
	reminderT2h  = 2 * time.Hour
	pollInterval = 30 * time.Second
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	log.Printf("reminder-scheduler: polling every %s", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for range ticker.C {
		runTick(ctx, q, "T-24h", reminderT24h)
		runTick(ctx, q, "T-2h", reminderT2h)
	}
}

// runTick finds tickets whose event starts within [now, now+lead) that
// haven't had this specific reminder kind sent yet, "sends" it (logs), and
// records reminders_sent — the dedup that makes a restarted scheduler, or
// two overlapping ticks, safe.
func runTick(ctx context.Context, q db.Querier, kind string, lead time.Duration) {
	now := time.Now()
	rows, err := q.ListTicketsDueForReminder(ctx, db.ListTicketsDueForReminderParams{
		WindowLow:  pgtype.Timestamptz{Time: now, Valid: true},
		WindowHigh: pgtype.Timestamptz{Time: now.Add(lead), Valid: true},
		Kind:       kind,
	})
	if err != nil {
		log.Printf("reminder-scheduler: list due for %s: %v", kind, err)
		return
	}
	for _, r := range rows {
		log.Printf("reminder-scheduler: [FAKE SEND] %s reminder for ticket %s (event %d starts %s)",
			kind, r.TicketID, r.EventID, r.StartsAt.Time.Format(time.RFC3339))
		if err := q.MarkReminderSent(ctx, db.MarkReminderSentParams{TicketID: r.TicketID, Kind: kind}); err != nil {
			log.Printf("reminder-scheduler: mark sent failed for %s (will retry, may double-log): %v", r.TicketID, err)
		}
	}
}
