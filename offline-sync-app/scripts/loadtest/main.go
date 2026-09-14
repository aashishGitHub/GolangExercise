// Command loadtest fires concurrent POST /api/sync requests (each a unique
// new location) and reports throughput + latency percentiles. Not part of
// the deployed app — a repeatable local NFR-measurement tool.
//
// Usage: go run ./scripts/loadtest -token "$(cat /tmp/idtoken.txt)" -n 500 -c 20
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

func main() {
	concurrency := flag.Int("c", 10, "concurrent workers")
	total := flag.Int("n", 500, "total requests")
	url := flag.String("url", "http://localhost:8080/api/sync", "target URL")
	token := flag.String("token", os.Getenv("LOADTEST_TOKEN"), "bearer token (or set LOADTEST_TOKEN)")
	flag.Parse()

	if *token == "" {
		fmt.Fprintln(os.Stderr, "missing -token or LOADTEST_TOKEN")
		os.Exit(1)
	}

	var wg sync.WaitGroup
	var okCount, errCount int64
	latencies := make([]time.Duration, *total)
	var idx int64

	client := &http.Client{Timeout: 10 * time.Second}
	sem := make(chan struct{}, *concurrency)

	start := time.Now()
	for i := 0; i < *total; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()

			now := time.Now().UTC().Format(time.RFC3339Nano)
			body, _ := json.Marshal(map[string]any{
				"locations": []map[string]any{{
					"id": uuid.New(), "name": fmt.Sprintf("loc-%d", i),
					"latitude": 1.0, "longitude": 2.0,
					"createdAt": now, "updatedAt": now,
				}},
			})

			req, _ := http.NewRequest(http.MethodPost, *url, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+*token)

			t0 := time.Now()
			resp, err := client.Do(req)
			elapsed := time.Since(t0)
			n := atomic.AddInt64(&idx, 1) - 1
			latencies[n] = elapsed

			if err != nil || resp.StatusCode != http.StatusOK {
				atomic.AddInt64(&errCount, 1)
			} else {
				atomic.AddInt64(&okCount, 1)
			}
			if resp != nil {
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	totalElapsed := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) time.Duration { return latencies[int(float64(len(latencies)-1)*p)] }

	fmt.Printf("requests=%d concurrency=%d elapsed=%s throughput=%.1f req/s\n",
		*total, *concurrency, totalElapsed, float64(*total)/totalElapsed.Seconds())
	fmt.Printf("ok=%d err=%d\n", okCount, errCount)
	fmt.Printf("p50=%s p95=%s p99=%s max=%s\n", pct(0.50), pct(0.95), pct(0.99), latencies[len(latencies)-1])
}
