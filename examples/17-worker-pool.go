package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ============================================================================
// WORKER POOL — the most important concurrency pattern in server code
//
// WHY IT EXISTS: `for _, job := range jobs { go handle(job) }` is unbounded
// concurrency. 100k jobs means 100k goroutines, 100k in-flight DB queries and
// an OOM or a thundering herd on your downstream dependency. A worker pool
// caps concurrency at N, which turns an unbounded fan-out into a queue with a
// known, tunable depth.
//
// SIZING THE POOL:
//   CPU-bound work ..... N = runtime.GOMAXPROCS(0); more just adds context switches
//   I/O-bound work ..... N = what the DOWNSTREAM can take (its connection pool,
//                        its rate limit) — not what your machine can run
//
// STRUCTURE: one jobs channel (fan-out to N workers), one results channel
// (fan-in back to the collector), one WaitGroup to know when to close results.
// ============================================================================

type Job struct {
	ID      int
	Payload string
}

type Result struct {
	JobID    int
	WorkerID int
	Output   string
	Err      error
}

var errPoison = errors.New("payload rejected")

// process simulates a unit of work that can fail and honours cancellation.
func process(ctx context.Context, j Job) (string, error) {
	select {
	case <-ctx.Done():
		// Always return ctx.Err() so the caller can distinguish "cancelled"
		// from "genuinely failed" — they need different retry decisions.
		return "", ctx.Err()
	case <-time.After(10 * time.Millisecond): // pretend this is an RPC
	}

	if j.ID%7 == 0 {
		return "", fmt.Errorf("job %d: %w", j.ID, errPoison)
	}
	return fmt.Sprintf("processed(%s)", j.Payload), nil
}

// ----------------------------------------------------------------------------
// The pool
// ----------------------------------------------------------------------------

func workerPool(ctx context.Context, jobs <-chan Job, numWorkers int) <-chan Result {
	results := make(chan Result)

	var wg sync.WaitGroup
	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)

		go func(workerID int) {
			defer wg.Done()

			// Every worker ranges over the SAME jobs channel. The runtime hands
			// each job to exactly one worker, so this is a natural work queue
			// with automatic load balancing — a slow worker simply takes fewer
			// jobs. No dispatcher, no partitioning, no locks.
			for job := range jobs {
				output, err := process(ctx, job)

				select {
				case results <- Result{JobID: job.ID, WorkerID: workerID, Output: output, Err: err}:
				case <-ctx.Done():
					// The collector is gone. Without this case the worker would
					// block on the send forever — the leak from example 15.
					return
				}
			}
		}(w)
	}

	// A separate closer goroutine is required: results must be closed exactly
	// once, and only after EVERY worker has stopped sending. Closing inside a
	// worker would panic the others mid-send.
	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}

// generate feeds jobs and closes the channel so workers' `range` terminates.
func generate(ctx context.Context, n int) <-chan Job {
	jobs := make(chan Job)
	go func() {
		defer close(jobs) // producer owns the channel, producer closes it
		for i := 1; i <= n; i++ {
			select {
			case jobs <- Job{ID: i, Payload: fmt.Sprintf("item-%02d", i)}:
			case <-ctx.Done():
				return // stop producing the moment the consumer gives up
			}
		}
	}()
	return jobs
}

// ----------------------------------------------------------------------------
// 1. Normal run
// ----------------------------------------------------------------------------

func demoPool() {
	fmt.Println("1. Bounded pool: 20 jobs, 4 workers")

	ctx := context.Background()
	start := time.Now()

	jobs := generate(ctx, 20)
	results := workerPool(ctx, jobs, 4)

	// Results arrive in COMPLETION order, not submission order. If you need
	// input order back, collect then sort by an index you carried along.
	perWorker := map[int]int{}
	var succeeded, failed int
	for r := range results {
		if r.Err != nil {
			failed++
			continue
		}
		succeeded++
		perWorker[r.WorkerID]++
	}

	elapsed := time.Since(start)

	workerIDs := make([]int, 0, len(perWorker))
	for id := range perWorker {
		workerIDs = append(workerIDs, id)
	}
	sort.Ints(workerIDs)

	fmt.Printf("   %d succeeded, %d failed in %v\n", succeeded, failed, elapsed.Round(time.Millisecond))
	fmt.Print("   jobs per worker:")
	for _, id := range workerIDs {
		fmt.Printf(" w%d=%d", id, perWorker[id])
	}
	fmt.Println()
	fmt.Printf("   serial would take ~%v; 4 workers gave ~4x\n", 20*10*time.Millisecond)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. Cancelling mid-flight
// ----------------------------------------------------------------------------

func demoCancellation() {
	fmt.Println("2. Cancelling the pool mid-flight")

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	jobs := generate(ctx, 1000) // far more work than we will allow to finish
	results := workerPool(ctx, jobs, 4)

	var completed, cancelled int
	for r := range results { // loop ends when results closes = pool fully drained
		switch {
		case r.Err == nil:
			completed++
		case errors.Is(r.Err, context.DeadlineExceeded):
			cancelled++
		}
	}

	fmt.Printf("   completed %d, cancelled %d before the 35ms deadline\n", completed, cancelled)
	fmt.Println("   the range loop exited cleanly: producer, workers and closer all returned")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. Fail fast on the first error
// ----------------------------------------------------------------------------

func demoFailFast() {
	fmt.Println("3. Fail-fast: abort the batch on the first error")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobs := generate(ctx, 50)
	results := workerPool(ctx, jobs, 4)

	var firstErr error
	var processed int
	for r := range results {
		if r.Err != nil && firstErr == nil {
			firstErr = r.Err
			cancel() // tell producer AND every worker to wind down
			continue // keep draining: never abandon a channel you own
		}
		if r.Err == nil {
			processed++
		}
	}

	fmt.Printf("   stopped after %d successes\n", processed)
	fmt.Printf("   first error: %v (errors.Is poison = %t)\n", firstErr, errors.Is(firstErr, errPoison))
	fmt.Println("   note: we kept draining `results` after cancel — leaving early")
	fmt.Println("   would block every worker on its send")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Preserving input order
// ----------------------------------------------------------------------------

func demoOrdered() {
	fmt.Println("4. Restoring input order from unordered results")

	ctx := context.Background()
	const n = 10

	jobs := generate(ctx, n)
	results := workerPool(ctx, jobs, 4)

	// Index by JobID and the ordering problem disappears — no coordination
	// between workers required, which is what keeps the pool fast.
	ordered := make([]string, n+1)
	for r := range results {
		if r.Err != nil {
			ordered[r.JobID] = "ERROR"
			continue
		}
		ordered[r.JobID] = r.Output
	}

	for i := 1; i <= 3; i++ {
		fmt.Printf("   [%d] %s\n", i, ordered[i])
	}
	fmt.Printf("   ... [%d] %s\n", n, ordered[n])
	fmt.Println()
}

func main() {
	fmt.Println("=== Worker Pool ===")
	fmt.Println()

	demoPool()
	demoCancellation()
	demoFailFast()
	demoOrdered()

	fmt.Println("Worker pool checklist:")
	fmt.Println("  - the PRODUCER closes jobs; a closer goroutine closes results")
	fmt.Println("  - wg.Wait() then close(results) — never close from a worker")
	fmt.Println("  - guard every send with a ctx.Done() case")
	fmt.Println("  - always drain results to completion, even when aborting")
	fmt.Println("  - size the pool to the DOWNSTREAM limit, not to your CPU count")
	fmt.Println("  - in real code, golang.org/x/sync/errgroup does the plumbing:")
	fmt.Println("      g, ctx := errgroup.WithContext(ctx); g.SetLimit(4)")
}
