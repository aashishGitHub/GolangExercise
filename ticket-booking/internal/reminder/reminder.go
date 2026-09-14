// Package reminder is the T-24h/T-2h reminder one-shot sender (docs/
// plan.md Phase 9), extracted out of cmd/reminder-scheduler (Phase 11) so
// the local ticker and cmd/reminder-scheduler-lambda's Scheduler-invoked
// single shot call the exact same code. A FAKE provider — "sending" means
// logging + recording reminders_sent, the same honesty-over-completeness
// pattern internal/payment.FakeProvider established.
package reminder

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ticketing/internal/db"
)

const (
	T24h = 24 * time.Hour
	T2h  = 2 * time.Hour
)

// RunOnce sends (logs + dedups) every reminder of `kind` due within `lead`
// of now, returning how many it sent.
func RunOnce(ctx context.Context, q db.Querier, kind string, lead time.Duration) (int, error) {
	now := time.Now()
	rows, err := q.ListTicketsDueForReminder(ctx, db.ListTicketsDueForReminderParams{
		WindowLow:  pgtype.Timestamptz{Time: now, Valid: true},
		WindowHigh: pgtype.Timestamptz{Time: now.Add(lead), Valid: true},
		Kind:       kind,
	})
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, r := range rows {
		log.Printf("reminder: [FAKE SEND] %s reminder for ticket %s (event %d starts %s)",
			kind, r.TicketID, r.EventID, r.StartsAt.Time.Format(time.RFC3339))
		if err := q.MarkReminderSent(ctx, db.MarkReminderSentParams{TicketID: r.TicketID, Kind: kind}); err != nil {
			log.Printf("reminder: mark sent failed for %s (will retry, may double-log): %v", r.TicketID, err)
			continue
		}
		sent++
	}
	return sent, nil
}
