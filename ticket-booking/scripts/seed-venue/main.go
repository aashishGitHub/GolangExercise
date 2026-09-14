// seed-venue creates a synthetic venue in Postgres — the shared fixture
// backend and (later) frontend tests build against, per docs/plan.md
// MILESTONES Phase 2. Default layout: 10 sections x 30 rows x 100 seats =
// 30,000 seats, laid out on a simple grid (real venues aren't uniform —
// docs/plan.md's frontend risk list already flags this and calls for a
// deliberately lopsided fixture too, added when Phase 4 needs it).
//
// Usage: go run ./scripts/seed-venue [-name X] [-city Y] [-sections N] [-rows N] [-seats-per-row N]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticketing/internal/db"
)

func main() {
	var (
		name        = flag.String("name", "Interview Arena", "venue name")
		city        = flag.String("city", "Metropolis", "venue city")
		sections    = flag.Int("sections", 10, "number of sections")
		rows        = flag.Int("rows", 30, "rows per section")
		seatsPerRow = flag.Int("seats-per-row", 100, "seats per row")
		dsn         = flag.String("dsn", envOr("DATABASE_URL", "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable"), "postgres DSN")
	)
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	q := db.New(pool)

	venue, err := q.CreateVenue(ctx, db.CreateVenueParams{Name: *name, City: *city})
	if err != nil {
		log.Fatalf("create venue: %v", err)
	}

	total := 0
	for s := 0; s < *sections; s++ {
		tier, closed := tierFor(s, *sections)
		section, err := q.CreateSection(ctx, db.CreateSectionParams{
			VenueID: venue.VenueID, Name: sectionName(s), Tier: tier,
			DisplayOrder: int32(s), Closed: closed,
		})
		if err != nil {
			log.Fatalf("create section %d: %v", s, err)
		}

		sectionOriginX := int32(s%5) * 900
		sectionOriginY := int32(s/5) * 700

		for r := 0; r < *rows; r++ {
			row, err := q.CreateSeatRow(ctx, db.CreateSeatRowParams{
				SectionID: section.SectionID, Label: fmt.Sprintf("Row %d", r+1),
				DisplayOrder: int32(r),
			})
			if err != nil {
				log.Fatalf("create row: %v", err)
			}

			for seatN := 0; seatN < *seatsPerRow; seatN++ {
				x := sectionOriginX + int32(seatN)*8
				y := sectionOriginY + int32(r)*10
				if _, err := q.CreateSeat(ctx, db.CreateSeatParams{
					RowID: row.RowID, SeatLabel: fmt.Sprintf("%d", seatN+1),
					XCoord: x, YCoord: y,
				}); err != nil {
					log.Fatalf("create seat: %v", err)
				}
				total++
			}
		}
		log.Printf("section %d/%d (%s, tier=%s) done", s+1, *sections, sectionName(s), tier)
	}

	log.Printf("seeded venue_id=%d %q in %q: %d sections x %d rows x %d seats = %d total seats",
		venue.VenueID, *name, *city, *sections, *rows, *seatsPerRow, total)
}

func sectionName(idx int) string {
	return fmt.Sprintf("Section %c", 'A'+rune(idx))
}

// tierFor gives the first fifth of sections "floor" (priciest, and section
// index 0's tier is deliberately never closed), the middle three-fifths
// "lower", the rest "upper" — and marks exactly one section closed so
// event-publisher's sellable=false / closedSections path has something
// real to exercise.
func tierFor(idx, total int) (tier string, closed bool) {
	switch {
	case idx == total-1:
		return "upper", true // last section: obstructed-view / structurally closed
	case idx < total/5:
		return "floor", false
	case idx < total-total/5:
		return "lower", false
	default:
		return "upper", false
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
