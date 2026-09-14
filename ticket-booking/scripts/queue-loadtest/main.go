// scripts/queue-loadtest drives internal/waitingroom.Queue.Join directly
// with N concurrent distinct arrivals against the real running Redis —
// docs/plan.md Phase 8's "5,000 arrivals in 1s" verification. Deliberately
// NOT driven through HTTP+cognito-local: that would measure JWT
// verification throughput (already proven in Phase 1), not the queue
// mechanism itself. 5,000 distinct synthetic subs exercise exactly what
// the queue needs to handle — real concurrent ZADD NX + ZRANK traffic.
package main

import (
	"context"
	"flag"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"ticketing/internal/waitingroom"
)

func main() {
	addr := flag.String("addr", "localhost:6379", "redis address")
	eventID := flag.Int64("event", 1, "event id")
	n := flag.Int("n", 5000, "number of arrivals")
	concurrency := flag.Int("concurrency", 200, "concurrent goroutines")
	flag.Parse()

	rdb := redis.NewClient(&redis.Options{Addr: *addr})
	defer rdb.Close()
	q := waitingroom.New(rdb, "loadtest-secret")

	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted, queued, errs := 0, 0, 0

	start := time.Now()
	for i := 0; i < *n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			sub := fmt.Sprintf("loadtest-user-%d", i)
			result, err := q.Join(context.Background(), *eventID, sub)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				return
			}
			if result.Admitted {
				admitted++
			} else {
				queued++
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("%d arrivals in %s (%.0f/s) — admitted=%d queued=%d errors=%d\n",
		*n, elapsed, float64(*n)/elapsed.Seconds(), admitted, queued, errs)
}
