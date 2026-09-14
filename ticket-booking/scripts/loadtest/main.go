// scripts/loadtest is docs/plan.md Phase 10's load harness: the full
// browse -> queue -> hold -> order -> ticket journey, driven concurrently
// over real HTTP against the real running stack, with configurable
// contention (uniform across the venue, or all racers targeting one hot
// seat) and a simple arrival ramp. Every number this project quotes about
// throughput or latency comes from a run of this binary — never estimated.
//
// Numbers here are LABELED "measured on dev hardware — a relative
// regression baseline, not a production capacity claim" (docs/plan.md's
// own instruction), matching the sibling project's convention: a laptop
// running Docker Desktop is not EC2, and this harness's job is to catch a
// regression between two runs, not to predict production capacity.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "server address")
	wsAddr := flag.String("ws-addr", "localhost:8080", "server address for the WS lag observer")
	cognitoEndpoint := flag.String("cognito-endpoint", "http://localhost:9229", "cognito-local endpoint")
	poolID := flag.String("pool-id", "", "cognito user pool id")
	clientID := flag.String("client-id", "", "cognito app client id")
	eventID := flag.Int64("event", 1, "event id")
	workers := flag.Int("workers", 20, "concurrent virtual users")
	users := flag.Int("users", 20, "size of the reused real-identity pool")
	duration := flag.Duration("duration", 20*time.Second, "how long to generate arrivals")
	rampUp := flag.Duration("ramp-up", 5*time.Second, "spread worker starts over this window")
	contention := flag.String("contention", "uniform", "uniform | hot")
	hotOrdinal := flag.Int("hot-ordinal", 5000, "seat ordinal every worker targets under -contention=hot")
	csvPath := flag.String("csv", "loadtest.csv", "output CSV path")
	dbURL := flag.String("db-url", "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable", "for the post-run invariant checks")
	flag.Parse()

	if *poolID == "" || *clientID == "" {
		log.Fatal("-pool-id and -client-id are required (see scripts/seed-cognito-local.sh output)")
	}

	ctx := context.Background()
	log.Printf("loadtest: provisioning %d real cognito-local users...", *users)
	tokens, err := provisionUsers(ctx, *cognitoEndpoint, *poolID, *clientID, *users)
	if err != nil {
		log.Fatalf("provision users: %v", err)
	}

	lagObserver := newWSLagObserver()
	wsStop := make(chan struct{})
	go lagObserver.run(*wsAddr, *eventID, tokens[0], wsStop)
	time.Sleep(300 * time.Millisecond) // let the WS connection + initial snapshot land before load starts

	runner := &journeyRunner{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: "http://" + *addr,
		eventID: *eventID,
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	switch *contention {
	case "hot":
		runner.pickSeat = func() int { return *hotOrdinal }
	default:
		runner.pickSeat = func() int { return rng.Intn(30000) }
	}
	runner.onHold = lagObserver.noteHoldRequested

	results := make(chan Result, 1024)
	var wg sync.WaitGroup
	deadline := time.Now().Add(*duration)

	var seq int64
	var seqMu sync.Mutex
	nextSeq := func() int {
		seqMu.Lock()
		defer seqMu.Unlock()
		seq++
		return int(seq)
	}

	log.Printf("loadtest: %d workers, %s duration, %s ramp-up, contention=%s, event=%d",
		*workers, *duration, *rampUp, *contention, *eventID)

	for w := 0; w < *workers; w++ {
		wg.Add(1)
		startDelay := time.Duration(w) * (*rampUp) / time.Duration(*workers)
		go func(workerIdx int, delay time.Duration) {
			defer wg.Done()
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			token := tokens[workerIdx%len(tokens)]
			for time.Now().Before(deadline) {
				res := runner.run(ctx, nextSeq(), token)
				results <- res
			}
		}(w, startDelay)
	}

	go func() { wg.Wait(); close(results) }()

	f, err := os.Create(*csvPath)
	if err != nil {
		log.Fatalf("create csv: %v", err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"seq", "startedAt", "outcome", "seatOrdinal", "holdMs", "orderMs", "totalMs"})

	var all []Result
	for res := range results {
		all = append(all, res)
		_ = w.Write([]string{
			strconv.Itoa(res.Seq), res.StartedAt.Format(time.RFC3339Nano), string(res.Outcome),
			strconv.Itoa(res.SeatOrdinal), fmt.Sprintf("%.2f", res.HoldMs),
			fmt.Sprintf("%.2f", res.OrderMs), fmt.Sprintf("%.2f", res.TotalMs),
		})
	}
	w.Flush()
	close(wsStop)
	time.Sleep(200 * time.Millisecond) // let the lag observer's last frame land

	printReport(all, lagObserver, *dbURL, *csvPath)
}

func printReport(all []Result, lag *wsLagObserver, dbURL, csvPath string) {
	total := len(all)
	var ticketed, seatTaken int
	holdLats := make([]float64, 0, total)
	for _, r := range all {
		switch r.Outcome {
		case OutcomeTicketed:
			ticketed++
		case OutcomeSeatTaken:
			seatTaken++
		}
		if r.HoldMs > 0 {
			holdLats = append(holdLats, r.HoldMs)
		}
	}
	sort.Float64s(holdLats)
	pct := func(p float64) float64 {
		if len(holdLats) == 0 {
			return 0
		}
		idx := int(p * float64(len(holdLats)))
		if idx >= len(holdLats) {
			idx = len(holdLats) - 1
		}
		return holdLats[idx]
	}

	fmt.Println()
	fmt.Println("=== scripts/loadtest report — measured on dev hardware; a relative regression baseline, not a production capacity claim ===")
	fmt.Printf("total journeys:        %d\n", total)
	fmt.Printf("hold p50/p95/p99 (ms): %.2f / %.2f / %.2f\n", pct(0.50), pct(0.95), pct(0.99))
	if total > 0 {
		fmt.Printf("conflict rate:         %.1f%% (%d/%d SEAT_TAKEN)\n", 100*float64(seatTaken)/float64(total), seatTaken, total)
		fmt.Printf("hold->purchase conv:   %.1f%% (%d/%d TICKETED)\n", 100*float64(ticketed)/float64(total), ticketed, total)
	}
	lp50, lp95, lp99 := lag.lagPercentiles()
	fmt.Printf("WS delta lag p50/p95/p99 (ms): %.2f / %.2f / %.2f (n=%d)\n", lp50, lp95, lp99, len(lag.lagsMs))

	runDBInvariants(dbURL)
	fmt.Printf("raw CSV: %s\n", csvPath)
}

func runDBInvariants(dbURL string) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Printf("loadtest: db invariants skipped (connect failed): %v", err)
		return
	}
	defer pool.Close()

	var compCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_saga_steps WHERE step='confirm' AND status='COMPENSATED'`,
	).Scan(&compCount); err == nil {
		fmt.Printf("saga compensation count: %d\n", compCount)
	}

	rows, err := pool.Query(ctx,
		`SELECT event_id, seat_id, count(*) FROM tickets WHERE revoked_at IS NULL GROUP BY 1,2 HAVING count(*) > 1`)
	if err != nil {
		log.Printf("loadtest: duplicate-ticket check failed: %v", err)
		return
	}
	defer rows.Close()
	dupes := 0
	for rows.Next() {
		dupes++
	}
	fmt.Printf("duplicate live tickets for one seat: %d rows (must be 0)\n", dupes)
}
