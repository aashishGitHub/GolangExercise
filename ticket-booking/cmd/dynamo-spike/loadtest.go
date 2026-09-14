package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/google/uuid"
)

type loadTestConfig struct {
	seatCount  int
	workers    int
	duration   time.Duration
	contention string // uniform | hot
	multiSeat  int
}

type journeyResult struct {
	latencyMs float64
	conflict  bool
	err       bool
}

// runLoadTest reproduces Phase 10's scenario SHAPE (uniform vs hot
// contention, concurrent workers, measured p50/p95/p99 + conflict rate) —
// not the literal HTTP browse->queue->hold->order journey, since this
// spike is explicitly not wired into cmd/server (docs/plan.md Phase 12:
// "nothing from this phase wires into cmd/server"). It drives
// AcquireHoldSingle/AcquireHoldMulti directly, the same way
// scripts/queue-loadtest and this project's other direct-driver harnesses
// exercise a mechanism without going through HTTP+auth when doing so
// would test something else entirely.
func runLoadTest(ctx context.Context, client *dynamodb.Client, cfg loadTestConfig) {
	const eventID = "loadtest-event"
	log.Printf("dynamo-spike loadtest: seeding %d seats...", cfg.seatCount)
	if err := seedSeats(ctx, client, eventID, cfg.seatCount); err != nil {
		log.Fatalf("seed seats: %v", err)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	pickSeats := func() []int {
		if cfg.contention == "hot" {
			seats := make([]int, cfg.multiSeat)
			for i := range seats {
				seats[i] = i // the SAME fixed seats every attempt under hot contention
			}
			return seats
		}
		start := rng.Intn(cfg.seatCount - cfg.multiSeat)
		seats := make([]int, cfg.multiSeat)
		for i := range seats {
			seats[i] = start + i
		}
		return seats
	}

	results := make(chan journeyResult, 4096)
	deadline := time.Now().Add(cfg.duration)

	var wg sync.WaitGroup
	for w := 0; w < cfg.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				seats := pickSeats()
				holdID := uuid.New().String()
				start := time.Now()
				var err error
				if cfg.multiSeat == 1 {
					err = AcquireHoldSingle(ctx, client, eventID, seats[0], holdID, "loadtest", 5*time.Minute)
				} else {
					err = AcquireHoldMulti(ctx, client, eventID, seats, holdID, "loadtest", 5*time.Minute)
				}
				lat := float64(time.Since(start).Microseconds()) / 1000.0
				switch {
				case err == nil:
					results <- journeyResult{latencyMs: lat}
				case err == ErrConflict:
					results <- journeyResult{latencyMs: lat, conflict: true}
				default:
					results <- journeyResult{latencyMs: lat, err: true}
				}
			}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	var all []journeyResult
	for r := range results {
		all = append(all, r)
	}

	printLoadTestReport(cfg, all)
}

func printLoadTestReport(cfg loadTestConfig, all []journeyResult) {
	total := len(all)
	var conflicts, errs int
	lats := make([]float64, 0, total)
	for _, r := range all {
		if r.conflict {
			conflicts++
		}
		if r.err {
			errs++
		}
		lats = append(lats, r.latencyMs)
	}
	sort.Float64s(lats)
	pct := func(p float64) float64 {
		if len(lats) == 0 {
			return 0
		}
		idx := int(p * float64(len(lats)))
		if idx >= len(lats) {
			idx = len(lats) - 1
		}
		return lats[idx]
	}

	fmt.Println()
	fmt.Printf("=== dynamo-spike loadtest report (contention=%s, multiSeat=%d) — measured on dev hardware ===\n", cfg.contention, cfg.multiSeat)
	fmt.Printf("total attempts:  %d\n", total)
	fmt.Printf("p50/p95/p99 (ms): %.2f / %.2f / %.2f\n", pct(0.50), pct(0.95), pct(0.99))
	if total > 0 {
		fmt.Printf("conflict rate:   %.1f%% (%d/%d)\n", 100*float64(conflicts)/float64(total), conflicts, total)
		fmt.Printf("error rate:      %.1f%% (%d/%d)\n", 100*float64(errs)/float64(total), errs, total)
	}
}
