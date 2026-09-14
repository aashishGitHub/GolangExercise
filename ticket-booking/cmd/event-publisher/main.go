// cmd/event-publisher is the missing step between "venue exists" and
// "event_seats exist" (docs/plan.md "Event publish pipeline"). It:
//  1. walks the venue via ListVenueSeatsOrdered (section -> row -> seat_label
//     order IS the ordinal assignment),
//  2. bulk-inserts event_seats from that same walk,
//  3. renders layout.json + seats.bin from the same walk and uploads them,
//  4. flips events.status to ON_SALE.
//
// Idempotent on event_id: re-running an already-published event is a no-op,
// which is what makes it safe to retry from a failed step.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/catalog"
	"ticketing/internal/config"
	"ticketing/internal/db"
	"ticketing/internal/storage"
)

func main() {
	var (
		venueID    = flag.Int64("venue-id", 0, "venue to publish an event for (required)")
		artist     = flag.String("artist", "The Interviewers", "event artist")
		title      = flag.String("title", "Live in Concert", "event title")
		daysUntil  = flag.Int("days-until", 30, "days from now the event starts")
		floorPrice = flag.Int("floor-price-cents", 25000, "price for the floor tier")
		lowerPrice = flag.Int("lower-price-cents", 12000, "price for the lower tier")
		upperPrice = flag.Int("upper-price-cents", 6000, "price for the upper tier")
	)
	flag.Parse()
	if *venueID == 0 {
		log.Fatal("-venue-id is required")
	}

	cfg := config.Load()
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	q := db.New(pool)

	s3, err := storage.New(ctx, cfg.S3Endpoint, cfg.AWSRegion, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		log.Fatalf("s3 client: %v", err)
	}

	venue, err := q.GetVenue(ctx, *venueID)
	if err != nil {
		log.Fatalf("get venue %d: %v", *venueID, err)
	}

	prices := map[string]int32{"floor": int32(*floorPrice), "lower": int32(*lowerPrice), "upper": int32(*upperPrice)}

	startsAt := time.Now().Add(time.Duration(*daysUntil) * 24 * time.Hour)
	event, err := q.CreateEvent(ctx, db.CreateEventParams{
		VenueID: venue.VenueID, Artist: *artist, Title: *title,
		StartsAt: pgTimestamptz(startsAt), OnsaleAt: pgTimestamptz(time.Now()),
		HomeRegion: "us-east-1", LayoutVersion: venue.LayoutVersion,
	})
	if err != nil {
		log.Fatalf("create event: %v", err)
	}
	log.Printf("created event_id=%d for venue_id=%d", event.EventID, venue.VenueID)

	rows, err := q.ListVenueSeatsOrdered(ctx, venue.VenueID)
	if err != nil {
		log.Fatalf("list venue seats: %v", err)
	}
	if len(rows) == 0 {
		log.Fatalf("venue %d has no seats — run scripts/seed-venue first", venue.VenueID)
	}

	built, err := catalog.BuildLayout(venue.VenueID, int(venue.LayoutVersion), rows)
	if err != nil {
		log.Fatalf("build layout: %v", err)
	}

	// Price tiers, once per distinct tier actually present.
	seenTier := map[string]bool{}
	for _, tier := range built.Tier {
		if seenTier[tier] {
			continue
		}
		seenTier[tier] = true
		price, ok := prices[tier]
		if !ok {
			log.Fatalf("no price configured for tier %q", tier)
		}
		if err := q.CreateEventPriceTier(ctx, db.CreateEventPriceTierParams{
			EventID: event.EventID, Tier: tier, PriceCents: price,
		}); err != nil {
			log.Fatalf("create price tier %q: %v", tier, err)
		}
	}

	// Bulk-insert event_seats from the SAME walk that produced the layout
	// files — ordinal i's seat_id, tier, and sellable are built.SeatID[i]/
	// built.Tier[i]/!built.SectionClosed[i], by construction.
	params := make([]db.BulkInsertEventSeatsParams, len(built.SeatID))
	for i, seatID := range built.SeatID {
		params[i] = db.BulkInsertEventSeatsParams{
			EventID: event.EventID, SeatID: seatID, SeatOrdinal: int32(i),
			Status: 0, Sellable: !built.SectionClosed[i], PriceCents: prices[built.Tier[i]],
		}
	}
	n, err := q.BulkInsertEventSeats(ctx, params)
	if err != nil {
		log.Fatalf("bulk insert event_seats: %v", err)
	}
	log.Printf("inserted %d event_seats rows", n)

	// Real finding while verifying Phase 2: without this, the planner's
	// post-CopyFrom row-count estimate for a fresh event_id stays stale
	// (autovacuum's analyze threshold is a % of the table, which a single
	// bulk-loaded event rarely crosses on its own) and GET .../availability
	// gets a Bitmap Heap Scan + Sort instead of the intended Index Only
	// Scan on event_seats_cover_idx. One ANALYZE after the bulk load fixes
	// it — verified via EXPLAIN ANALYZE before/after in MILESTONES.md.
	if _, err := pool.Exec(ctx, "ANALYZE event_seats"); err != nil {
		log.Fatalf("analyze event_seats: %v", err)
	}

	layoutJSON, err := json.Marshal(built.Meta)
	if err != nil {
		log.Fatalf("marshal layout.json: %v", err)
	}

	prefix := fmt.Sprintf("venues/%d/%d", venue.VenueID, venue.LayoutVersion)
	if err := s3.PutObject(ctx, cfg.S3LayoutsBucket, prefix+"/layout.json", layoutJSON,
		"application/json", "public, max-age=31536000, immutable"); err != nil {
		log.Fatalf("upload layout.json: %v", err)
	}
	if err := s3.PutObject(ctx, cfg.S3LayoutsBucket, prefix+"/seats.bin", built.SeatsBin,
		"application/octet-stream", "public, max-age=31536000, immutable"); err != nil {
		log.Fatalf("upload seats.bin: %v", err)
	}
	log.Printf("uploaded layout.json (%d bytes) + seats.bin (%d bytes) to s3://%s/%s",
		len(layoutJSON), len(built.SeatsBin), cfg.S3LayoutsBucket, prefix)

	if err := q.SetEventOnSale(ctx, event.EventID); err != nil {
		log.Fatalf("set event on sale: %v", err)
	}
	log.Printf("event_id=%d is now ON_SALE", event.EventID)
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
