package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// RESILIENCE PATTERNS FOR DISTRIBUTED SYSTEMS
//
// Every pattern here exists to answer one question: what does this service do
// when the thing it depends on is slow, broken, or overloaded?
//
//   semaphore ......... never send more concurrent work than the peer can take
//   rate limiter ...... never exceed the peer's requests-per-second budget
//   retry + backoff ... recover from transient failures WITHOUT amplifying them
//   circuit breaker ... stop hammering a peer that is already down
//   singleflight ...... collapse duplicate concurrent work into one call
//   hedged request .... beat tail latency by racing a second replica
//
// The failure they collectively prevent is the RETRY STORM: a peer slows down,
// every caller retries, the extra load makes it slower, more retries pile on,
// and a partial outage becomes a total one.
//
// Randomness here is seeded deterministically so the output is reproducible.
// ============================================================================

// A fixed seed keeps this example's output stable across runs.
// In production use the global rand (auto-seeded since Go 1.20) or crypto/rand.
var rng = rand.New(rand.NewSource(42)) //nolint:gosec // deterministic demo output

// ----------------------------------------------------------------------------
// 1. SEMAPHORE — bound concurrency with a buffered channel
// ----------------------------------------------------------------------------

// A buffered channel IS a counting semaphore: capacity = permits.
// This is the whole implementation — no library needed.
type Semaphore chan struct{}

func NewSemaphore(n int) Semaphore { return make(Semaphore, n) }

// Acquire blocks for a permit, but gives up if the context dies first.
// Always take the ctx-aware path: an unbounded Acquire is a deadlock waiting
// to happen when the pool is saturated.
func (s Semaphore) Acquire(ctx context.Context) error {
	select {
	case s <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s Semaphore) Release() { <-s }

func demoSemaphore() {
	fmt.Println("1. Semaphore — cap concurrency at 3")

	sem := NewSemaphore(3)
	ctx := context.Background()

	var inFlight, maxInFlight atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			if err := sem.Acquire(ctx); err != nil {
				return
			}
			defer sem.Release()

			// Track the high-water mark to prove the bound actually holds.
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}

			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
		}(i)
	}
	wg.Wait()

	fmt.Printf("   12 tasks, peak concurrency = %d (never exceeded 3)\n", maxInFlight.Load())
	fmt.Println("   production alternative: golang.org/x/sync/semaphore (weighted),")
	fmt.Println("   or errgroup.Group.SetLimit(n) when you also need error handling")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. RATE LIMITER — token bucket
// ----------------------------------------------------------------------------

// Concurrency limits and rate limits are DIFFERENT controls: a semaphore caps
// how many run at once, a rate limiter caps how many start per second. A fast
// peer can be overwhelmed by rate even at low concurrency.
type RateLimiter struct {
	tokens chan struct{}
	stop   chan struct{}
}

func NewRateLimiter(perSecond int, burst int) *RateLimiter {
	rl := &RateLimiter{
		tokens: make(chan struct{}, burst),
		stop:   make(chan struct{}),
	}

	// Pre-fill the bucket so an idle client can burst immediately.
	for i := 0; i < burst; i++ {
		rl.tokens <- struct{}{}
	}

	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(perSecond))
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				select {
				case rl.tokens <- struct{}{}: // refill one token
				default: // bucket full: drop it, do not block the ticker
				}
			case <-rl.stop:
				return // without this the refiller goroutine leaks
			}
		}
	}()

	return rl
}

func (rl *RateLimiter) Wait(ctx context.Context) error {
	select {
	case <-rl.tokens:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (rl *RateLimiter) Close() { close(rl.stop) }

func demoRateLimiter() {
	fmt.Println("2. Rate limiter — 100/sec with a burst of 3")

	rl := NewRateLimiter(100, 3)
	defer rl.Close()

	ctx := context.Background()
	start := time.Now()

	for i := 1; i <= 6; i++ {
		if err := rl.Wait(ctx); err != nil {
			break
		}
		if i == 3 || i == 6 {
			fmt.Printf("   request %d at %v\n", i, time.Since(start).Round(5*time.Millisecond))
		}
	}

	fmt.Println("   first 3 were instant (burst), the rest paced at ~10ms apart")
	fmt.Println("   production alternative: golang.org/x/time/rate (rate.Limiter)")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. RETRY with exponential backoff and jitter
// ----------------------------------------------------------------------------

// errPermanent marks a failure that retrying cannot fix (400, 404, validation).
// Retrying these wastes budget and hides bugs.
var errPermanent = errors.New("permanent failure")

type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func retry(ctx context.Context, cfg RetryConfig, op func(attempt int) error) error {
	var lastErr error

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		lastErr = op(attempt)
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, errPermanent) {
			return fmt.Errorf("not retryable: %w", lastErr) // fail fast
		}
		if attempt == cfg.MaxAttempts {
			break
		}

		// EXPONENTIAL: base * 2^(attempt-1) — gives the peer room to recover.
		delay := cfg.BaseDelay * time.Duration(1<<(attempt-1))
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}

		// JITTER is not optional. Without it, N clients that failed together
		// retry together — a synchronised thundering herd that re-kills the
		// peer at every backoff boundary. Full jitter = random in [0, delay).
		jittered := time.Duration(rng.Int63n(int64(delay)))

		timer := time.NewTimer(jittered)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("retry aborted: %w", ctx.Err())
		}
	}

	return fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

func demoRetry() {
	fmt.Println("3. Retry with exponential backoff + full jitter")

	ctx := context.Background()
	cfg := RetryConfig{MaxAttempts: 5, BaseDelay: 10 * time.Millisecond, MaxDelay: time.Second}

	// Case A: succeeds on the 3rd attempt
	start := time.Now()
	err := retry(ctx, cfg, func(attempt int) error {
		if attempt < 3 {
			return errors.New("connection refused")
		}
		return nil
	})
	fmt.Printf("   transient x2 then success: err=%v (took %v)\n",
		err, time.Since(start).Round(5*time.Millisecond))

	// Case B: permanent error, must NOT retry
	attempts := 0
	err = retry(ctx, cfg, func(attempt int) error {
		attempts++
		return fmt.Errorf("400 bad request: %w", errPermanent)
	})
	fmt.Printf("   permanent error: tried %d time(s) — %v\n", attempts, err)

	// Case C: exhausts the budget
	attempts = 0
	err = retry(ctx, cfg, func(attempt int) error {
		attempts++
		return errors.New("service unavailable")
	})
	fmt.Printf("   always failing: tried %d time(s) — %v\n", attempts, err)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. CIRCUIT BREAKER
// ----------------------------------------------------------------------------

// States: CLOSED (normal) -> OPEN (fail fast) -> HALF-OPEN (probe) -> CLOSED
//
// The point is to fail FAST when the peer is known-down. Without a breaker,
// every caller waits the full timeout on every request, so your own
// goroutines and connections pile up and YOUR service falls over too.
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	default:
		return "HALF-OPEN"
	}
}

type CircuitBreaker struct {
	mu           sync.Mutex
	state        State
	failures     int
	successes    int
	maxFailures  int
	resetTimeout time.Duration
	openedAt     time.Time
}

func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:        StateClosed,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
	}
}

var errCircuitOpen = errors.New("circuit breaker is open")

func (cb *CircuitBreaker) Call(fn func() error) error {
	// Phase 1: decide whether we are allowed to call at all.
	cb.mu.Lock()
	if cb.state == StateOpen {
		if time.Since(cb.openedAt) < cb.resetTimeout {
			cb.mu.Unlock()
			return errCircuitOpen // fail fast, no network call, no timeout wait
		}
		// Cool-off elapsed: allow a probe through.
		cb.state = StateHalfOpen
		cb.successes = 0
	}
	cb.mu.Unlock()

	// Phase 2: run the call OUTSIDE the lock. Holding a mutex across I/O
	// would serialise every caller and defeat the whole purpose.
	err := fn()

	// Phase 3: record the outcome.
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		if cb.state == StateHalfOpen || cb.failures >= cb.maxFailures {
			cb.state = StateOpen // a failed probe re-opens immediately
			cb.openedAt = time.Now()
		}
		return err
	}

	switch cb.state {
	case StateHalfOpen:
		cb.successes++
		if cb.successes >= 2 { // require sustained health before trusting again
			cb.state = StateClosed
			cb.failures = 0
		}
	case StateClosed:
		cb.failures = 0 // consecutive-failure counter, not cumulative
	}
	return nil
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func demoCircuitBreaker() {
	fmt.Println("4. Circuit breaker — fail fast instead of piling up")

	cb := NewCircuitBreaker(3, 60*time.Millisecond)
	var downstreamCalls atomic.Int64

	failing := func() error {
		downstreamCalls.Add(1)
		return errors.New("connection timeout")
	}
	healthy := func() error {
		downstreamCalls.Add(1)
		return nil
	}

	fmt.Printf("   initial state: %s\n", cb.State())

	// Trip it: 3 consecutive failures
	for i := 1; i <= 3; i++ {
		_ = cb.Call(failing)
	}
	fmt.Printf("   after 3 failures: %s\n", cb.State())

	// While open, calls are rejected without touching the network
	before := downstreamCalls.Load()
	rejected := 0
	for i := 0; i < 100; i++ {
		if errors.Is(cb.Call(failing), errCircuitOpen) {
			rejected++
		}
	}
	fmt.Printf("   next 100 calls: %d rejected instantly, %d reached downstream\n",
		rejected, downstreamCalls.Load()-before)

	// After the cool-off, a probe is allowed through
	time.Sleep(70 * time.Millisecond)
	_ = cb.Call(healthy)
	fmt.Printf("   after cool-off + 1 success: %s\n", cb.State())
	_ = cb.Call(healthy)
	fmt.Printf("   after 2nd success: %s\n", cb.State())
	fmt.Println("   production alternative: github.com/sony/gobreaker")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 5. SINGLEFLIGHT — collapse duplicate concurrent calls
// ----------------------------------------------------------------------------

// Solves CACHE STAMPEDE: a hot key expires, 1000 requests all miss at once,
// and 1000 identical queries hit the database. Singleflight lets ONE through
// and hands the same result to the other 999.
type call struct {
	wg  sync.WaitGroup
	val string
	err error
}

type Group struct {
	mu sync.Mutex
	m  map[string]*call
}

func (g *Group) Do(key string, fn func() (string, error)) (string, bool, error) {
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[string]*call)
	}

	// Someone is already computing this key: wait for THEIR result.
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, true, c.err // shared = true
	}

	// We are the leader. Register BEFORE unlocking so nobody else starts.
	c := new(call)
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done() // release every waiter

	g.mu.Lock()
	delete(g.m, key) // next caller after this point starts a fresh call
	g.mu.Unlock()

	return c.val, false, c.err
}

func demoSingleflight() {
	fmt.Println("5. Singleflight — collapse a cache stampede")

	var g Group
	var dbQueries atomic.Int64

	loadUser := func() (string, error) {
		dbQueries.Add(1)
		time.Sleep(30 * time.Millisecond) // a slow database read
		return "user:42", nil
	}

	var wg sync.WaitGroup
	var shared atomic.Int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, wasShared, _ := g.Do("user:42", loadUser)
			if wasShared {
				shared.Add(1)
			}
		}()
	}
	wg.Wait()

	fmt.Printf("   50 concurrent requests -> %d database quer(y/ies)\n", dbQueries.Load())
	fmt.Printf("   %d callers reused the in-flight result\n", shared.Load())
	fmt.Println("   caution: all 50 share one FAILURE too — one bad response fans out")
	fmt.Println("   production alternative: golang.org/x/sync/singleflight")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 6. HEDGED REQUESTS — trade cost for tail latency
// ----------------------------------------------------------------------------

// If p99 latency matters more than backend cost, send a second request to
// another replica after a short delay and take whichever answers first.
// Google's "The Tail at Scale" popularised this; budget it (e.g. hedge only
// 5% of requests) or you add 100% load to fix 1% of latency.
func hedgedCall(ctx context.Context, hedgeAfter time.Duration, call func(ctx context.Context, replica int) string) (string, int) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // cancels the loser as soon as we have a winner

	type answer struct {
		val     string
		replica int
	}
	// BUFFERED for both replicas: the loser must be able to send and exit
	// even though nobody reads it. Unbuffered here would leak a goroutine.
	results := make(chan answer, 2)

	go func() { results <- answer{call(ctx, 1), 1} }()

	timer := time.NewTimer(hedgeAfter)
	defer timer.Stop()

	select {
	case a := <-results:
		return a.val, a.replica // primary was fast; no hedge sent
	case <-timer.C:
		go func() { results <- answer{call(ctx, 2), 2} }() // fire the hedge
		a := <-results
		return a.val, a.replica
	}
}

func demoHedging() {
	fmt.Println("6. Hedged requests — cut the tail")

	ctx := context.Background()

	// Replica 1 is having a bad day; replica 2 is healthy.
	call := func(ctx context.Context, replica int) string {
		d := 200 * time.Millisecond
		if replica == 2 {
			d = 20 * time.Millisecond
		}
		select {
		case <-time.After(d):
			return fmt.Sprintf("answer from replica-%d", replica)
		case <-ctx.Done():
			return "cancelled"
		}
	}

	start := time.Now()
	val, replica := hedgedCall(ctx, 30*time.Millisecond, call)

	fmt.Printf("   %s (replica %d) in %v\n", val, replica,
		time.Since(start).Round(10*time.Millisecond))
	fmt.Println("   without hedging this request would have taken 200ms")
	fmt.Println()
}

func main() {
	fmt.Println("=== Distributed Systems Patterns ===")
	fmt.Println()

	demoSemaphore()
	demoRateLimiter()
	demoRetry()
	demoCircuitBreaker()
	demoSingleflight()
	demoHedging()

	fmt.Println("Which control solves which problem:")
	fmt.Println("  too many at once ........... semaphore / errgroup SetLimit")
	fmt.Println("  too many per second ........ rate limiter (token bucket)")
	fmt.Println("  transient failure .......... retry + exponential backoff + JITTER")
	fmt.Println("  peer is already down ....... circuit breaker (fail fast)")
	fmt.Println("  duplicate concurrent work .. singleflight")
	fmt.Println("  bad p99 latency ............ hedged requests (budgeted)")
	fmt.Println()
	fmt.Println("Compose them in this order, outermost first:")
	fmt.Println("  breaker -> rate limit -> semaphore -> retry -> timeout -> call")
	fmt.Println("  and give the RETRY an overall deadline, or backoff outlives the request")
}
