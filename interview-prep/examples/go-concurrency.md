# Go concurrency — runnable patterns and interview drills

Reference for Day 2. Each section has a **shape you can write from memory**, the **failure mode** it
prevents, and the sentence to say in the interview.

> These are separate illustrative blocks, not one package. Imports are omitted; assume the standard
> library packages named in each block plus `golang.org/x/sync/errgroup` and
> `golang.org/x/sync/semaphore` where used. Compile them in a scratch module before you claim them in an
> interview.

---

## 1. Bounded worker pool — the default answer

**Prompt:** "Process N jobs with at most W workers, stop on the first error, and respect cancellation."

```go
type Job struct {
	ID string
}

func RunPool(
	parent context.Context,
	jobs []Job,
	workers int,
	process func(context.Context, Job) error,
) error {
	if workers < 1 {
		return fmt.Errorf("workers must be >= 1")
	}

	g, ctx := errgroup.WithContext(parent)
	ch := make(chan Job)

	// One producer owns the channel and closes it. The send selects on ctx so a
	// worker error cannot leave the producer blocked forever.
	g.Go(func() error {
		defer close(ch)
		for _, job := range jobs {
			select {
			case ch <- job:
			case <-ctx.Done():
				return nil // the worker's real error wins in g.Wait
			}
		}
		return nil
	})

	for i := 0; i < workers; i++ {
		g.Go(func() error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case job, ok := <-ch:
					if !ok {
						return nil
					}
					if err := process(ctx, job); err != nil {
						return fmt.Errorf("process %s: %w", job.ID, err)
					}
				}
			}
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}
	// g.Wait also cancels the derived ctx on success, so check the parent.
	return parent.Err()
}
```

Why this shape:

- **Bounded:** W goroutines, not one per job.
- **First error wins:** `errgroup.WithContext` cancels siblings when `process` returns an error.
- **No leak:** producer selects on `ctx.Done()`; workers select on both input and cancellation.
- **No masked error:** workers return `nil` when they observe cancellation, so the actual `process`
  error is the one `g.Wait` returns. Returning `ctx.Err()` from every worker can win the race and hide
  the root cause.

Say: *"I bound the goroutines, gave every blocking operation a cancellation path, and made the first
real error the returned error. The channel is owned and closed by the producer."*

**Follow-up — why not `SetLimit`?** `errgroup.SetLimit(W)` is fine when the work is a slice and you want
one goroutine per item with a cap. A fixed pool is better when workers should loop over a stream, when
you want to reuse the goroutines, or when ordering/admission needs its own policy.

---

## 2. Bounded concurrency with `SetLimit`

```go
func fetchAll(parent context.Context, urls []string) error {
	g, ctx := errgroup.WithContext(parent)
	g.SetLimit(8) // Go blocks when 8 goroutines are in flight.

	for _, url := range urls {
		url := url // only needed before Go 1.22; harmless and explicit.
		g.Go(func() error {
			return fetch(ctx, url)
		})
	}
	return g.Wait()
}
```

`SetLimit` bounds **goroutines**, not downstream connections. If each goroutine opens a DB connection,
eight goroutines can still exhaust a four-connection pool. Bound at the resource that actually hurts.

**Semaphore vs SetLimit:**

| Tool | Bounds | Use when |
|---|---|---|
| `SetLimit` | goroutines started by the group | one goroutine per item, simple fan-out |
| buffered channel | in-flight operations | you already have long-lived workers |
| `semaphore.Weighted` | weighted operations | mixed costs, `TryAcquire`, fair queuing |

---

## 3. Fan-out / fan-in — merge without leaking

```go
func merge[T any](ctx context.Context, chans ...<-chan T) <-chan T {
	out := make(chan T)
	var wg sync.WaitGroup

	for _, ch := range chans {
		ch := ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case v, ok := <-ch:
					if !ok {
						return
					}
					select {
					case out <- v:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out) // the merger owns out and closes it exactly once
	}()
	return out
}
```

Properties:

- Every input channel must eventually close or the merge waits forever.
- Inputs are consumed concurrently, so **ordering is lost**. If order matters, attach a sequence
  number and reorder downstream, or use a single consumer.
- Cancellation is cooperative: the caller must cancel `ctx` if it stops reading `out`, or the merge
  goroutines block on the send.

**Interview trap:** "Close the output in each input goroutine." No — multiple closers panic. One
coordinator waits for all inputs, then closes.

---

## 4. Pipeline — each stage owns its output

```go
func generate(ctx context.Context, nums ...int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for _, n := range nums {
			select {
			case out <- n:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case n, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- n * n:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func runPipeline(parent context.Context) []int {
	ctx, cancel := context.WithCancel(parent)
	defer cancel() // stops every stage if we return early

	var got []int
	for n := range square(ctx, generate(ctx, 1, 2, 3, 4)) {
		got = append(got, n)
	}
	return got // [1 4 9 16]
}
```

Rules:

1. The goroutine that creates a channel closes it.
2. Every send selects on `ctx.Done()`.
3. Every receive loop exits when the input closes.
4. The consumer cancels when it stops early.

**Do not** use `for v := range in` around a send without a cancellation select; a slow downstream can
wedge the whole pipeline.

---

## 5. Weighted semaphore

```go
import "golang.org/x/sync/semaphore"

type Fetcher struct {
	sem *semaphore.Weighted
}

func NewFetcher(n int64) *Fetcher {
	return &Fetcher{sem: semaphore.NewWeighted(n)}
}

func (f *Fetcher) Fetch(ctx context.Context, url string) error {
	if err := f.sem.Acquire(ctx, 1); err != nil {
		return err // ctx canceled or deadline exceeded while waiting
	}
	defer f.sem.Release(1)

	return fetch(ctx, url)
}
```

Use weights when operations have different costs:

```go
if err := sem.Acquire(ctx, int64(payloadSizeMB)); err != nil { ... }
```

`TryAcquire` is for load shedding: if the semaphore is full, return `429`/`503` instead of queueing.
Do not use it for correctness.

---

## 6. Retry — classify, bound, jitter, respect context

```go
func Retry(
	ctx context.Context,
	attempts int,
	base time.Duration,
	retryable func(error) bool,
	fn func(context.Context) error,
) error {
	if attempts < 1 {
		return fmt.Errorf("attempts must be >= 1")
	}

	delay := base
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		last = fn(ctx)
		if last == nil || !retryable(last) || ctx.Err() != nil {
			return last
		}
		if attempt == attempts {
			break
		}

		// Full-ish jitter: half the delay fixed, half random. Avoids lockstep
		// retries without collapsing to an immediate retry.
		half := delay / 2
		sleep := half + time.Duration(rand.Int64N(int64(half)+1))
		if err := sleepCtx(ctx, sleep); err != nil {
			return err
		}
		delay = min(delay*2, 5*time.Second)
	}
	return last
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
```

Classification is the important part, not the loop:

```go
// ControlPlane's consumer classifies domain validation/missing-row errors as
// permanent (poison) and everything else as transient.
func retryable(err error) bool {
	var derr *domain.Error
	if errors.As(err, &derr) {
		switch derr.Code {
		case domain.ErrValidation, domain.ErrTaskNotFound, domain.ErrTenantNotFound:
			return false
		}
	}
	return true
}
```

Never retry:

- validation errors, malformed payloads, unknown enum values;
- `context.Canceled`;
- a non-idempotent operation without an idempotency key.

Say: *"The retry policy is a classification problem first. I bound attempts at the caller, retry only
transient failures, add jitter, and stop on context cancellation. The broker-side delivery limit is the
second bound."*

---

## 7. SSE with client disconnect and heartbeats

```go
func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // tell nginx not to buffer

	events, unsubscribe := h.broker.Subscribe(r.Context())
	defer unsubscribe()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return // client went away; cleanup runs via defers
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
```

Why each piece:

- `r.Context()` is canceled when the client disconnects; a stream without it leaks a goroutine and a
  subscription per abandoned tab.
- Heartbeats keep proxies and load balancers from closing an idle connection. They also detect a dead
  client on the next write.
- `WriteTimeout` must be `0` or longer than the stream; otherwise the server cuts the response.
- SSE is one-way. Do not pretend it replaces bidirectional WebSockets.
- Backpressure: a slow client fills its buffer. Bound the per-client queue and drop/disconnect on
  overflow; do not let one browser tab grow memory without limit.

---

## 8. Graceful shutdown — ordered, deadline-bound

```go
// cm, logger, handler, and the store/outbox packages are wired elsewhere.
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A second signal should kill the process immediately instead of waiting for
	// the graceful path. stop restores the default signal behavior.
	go func() {
		<-ctx.Done()
		stop()
	}()

	pool, err := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()

	workCtx, cancelWork := context.WithCancel(context.Background())
	var bg errgroup.Group

	relay := outbox.NewRelay(pool, cm, logger)
	bg.Go(func() error { relay.Run(workCtx); return nil })

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			cancelWork()
			_ = bg.Wait()
			return err
		}
	case <-ctx.Done():
	}

	// 1. Stop accepting and drain in-flight requests.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", "error", err)
	}

	// 2. Stop background workers, wait for them, then close dependencies (defers).
	cancelWork()
	_ = bg.Wait()
	return nil
}
```

Order matters:

1. **Readiness fails** (or the orchestrator removes the instance) so no new traffic arrives.
2. **`srv.Shutdown`** stops accepting and waits for active handlers until the deadline.
3. **Cancel background work** (relay/consumer) and wait.
4. **Close the pool/broker** after in-flight work is done.

Do not close the DB before handlers finish; do not wait forever for a stuck handler or a broker
reconnect. A second signal is the escape hatch.

---

## 9. Leak fixes — recognize these on sight

### 9.1 `time.After` in a hot loop

```go
// BAD: allocates a timer per iteration; timers live until they fire.
for {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Second):
		poll()
	}
}
```

```go
// GOOD: one ticker for the loop.
ticker := time.NewTicker(time.Second)
defer ticker.Stop()
for {
	select {
	case <-ctx.Done():
		return
	case <-ticker.C:
		poll()
	}
}
```

`time.After` in a bounded retry or a one-shot timeout is fine. In a long-lived loop, use a ticker or a
reused timer.

### 9.2 Blocked send with no receiver

```go
// BAD: if the consumer returns, this goroutine blocks forever.
go func() {
	for _, v := range values {
		out <- v
	}
	close(out)
}()
```

```go
// GOOD: every send has an exit.
go func() {
	defer close(out)
	for _, v := range values {
		select {
		case out <- v:
		case <-ctx.Done():
			return
		}
	}
}()
```

### 9.3 Missing `cancel`

```go
// BAD: the child context/timer lives until the deadline even after return.
ctx, _ := context.WithTimeout(parent, time.Minute)
```

```go
// GOOD
ctx, cancel := context.WithTimeout(parent, time.Minute)
defer cancel()
```

### 9.4 `WaitGroup.Add` inside the goroutine

```go
// BAD: Wait can return before Add runs.
for _, v := range values {
	go func() {
		wg.Add(1)
		defer wg.Done()
		work(v)
	}()
}
wg.Wait()
```

```go
// GOOD: Add before starting.
for _, v := range values {
	wg.Add(1)
	go func() {
		defer wg.Done()
		work(v)
	}()
}
wg.Wait()
```

### 9.5 Goroutine per request, unbounded

```go
// BAD: 10k requests -> 10k goroutines all hitting the DB.
go audit.Log(r.Context(), event)
```

```go
// GOOD: bounded queue + workers, or synchronous if the write must be durable.
select {
case auditCh <- event:
default:
	// queue full: drop with a metric, or block with a request deadline
}
```

For an audit log that must not be lost, do not use a fire-and-forget goroutine; write it in the request
transaction or use an outbox.

### 9.6 `for range ch` with no close

A receiver ranging over a channel waits forever if the sender never closes. The sender owns the close.
If there are multiple senders, use a `WaitGroup` and one closer goroutine.

---

## 10. Race test — what to run and what it proves

```go
func TestConcurrentCounter(t *testing.T) {
	var counter atomic.Int64
	var wg sync.WaitGroup

	const goroutines = 100
	const increments = 1_000

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				counter.Add(1)
			}
		}()
	}
	wg.Wait()

	if got, want := counter.Load(), int64(goroutines*increments); got != want {
		t.Fatalf("counter = %d, want %d", got, want)
	}
}
```

Run it under the detector:

```bash
go test -race ./... -run TestConcurrentCounter -count=100
```

What `-race` proves: it detected unsynchronized reads/writes that **executed during this run**. It does
not prove the absence of races, and it cannot detect a logical race condition built from correctly
locked operations (for example, read version, then write with a stale value in a second transaction).

Use `sync.WaitGroup` or channels for test synchronization, not `time.Sleep`. A sleep-based test is
slow and still flaky on a loaded CI machine.

### Deterministic cancellation test

```go
var errBoom = errors.New("boom")

func TestRunPoolStopsOnError(t *testing.T) {
	jobs := make([]Job, 100)
	for i := range jobs {
		jobs[i] = Job{ID: fmt.Sprint(i)}
	}

	var started atomic.Int64
	err := RunPool(context.Background(), jobs, 4, func(ctx context.Context, j Job) error {
		if started.Add(1) == 3 {
			return errBoom
		}
		<-ctx.Done() // simulate a long operation that observes cancellation
		return nil
	})

	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want errBoom", err)
	}
}
```

This proves the error propagates **and** siblings stop. It does not prove every job ran; that is the
point of cancellation.

---

## 11. Interview prompts — 30-second skeletons

1. **"Write a worker pool."** Clarify: bounded workers, error policy, ordering, cancellation. Write
   `RunPool`; mention a buffered jobs channel if the producer should not block.
2. **"Why not a goroutine per request?"** Unbounded memory/DB connections, no backpressure, thundering
   herd on recovery. Bound at the downstream resource.
3. **"Mutex or channel?"** Locks for in-place state; channels for ownership transfer/signaling. A
   counter is a mutex/atomic, not a goroutine and a channel.
4. **"What is a data race?"** Unsynchronized concurrent access, at least one write; undefined behavior.
   `-race` finds it; a mutex or channel creates the happens-before edge.
5. **"Find the leak."** Look for a send/receive with no cancellation, a missing `cancel`, a ticker not
   stopped, `time.After` in a loop, or `WaitGroup.Add` in the wrong place.
6. **"How do you stop this on SIGTERM?"** Signal context → fail readiness → `Shutdown` with deadline →
   cancel workers → close dependencies → second signal hard-exits.
7. **"How do you test concurrency?"** Deterministic synchronization, `-race`, high `-count`, targeted
   invariants (exactly one winner, exactly one increment), no sleeps.
8. **"Why does the DB own the race in ControlPlane?"** The race is across processes; a Go mutex is
   invisible to another replica. The unique index and conditional UPDATE are atomic in Postgres; Go
   only sends the statements with bounded concurrency.
9. **"What is backpressure?"** A bounded queue or semaphore that makes the producer wait or fail fast
   instead of accumulating unbounded work. Name the policy: block, drop, or reject.
10. **"Channel closed twice?"** Panic. One owner closes; multiple senders coordinate with a WaitGroup
    and a single closer.

---

## 12. End-of-day checkpoint

- [ ] Write `RunPool` from memory, including the producer close and cancellation paths.
- [ ] Explain why workers return `nil` on cancellation in the pool.
- [ ] Write a pipeline stage and say who closes the output.
- [ ] Write a retry with context, jitter, and an explicit retryable classifier.
- [ ] Explain the SSE cleanup path and the `WriteTimeout` trap.
- [ ] List five goroutine leak patterns without notes.
- [ ] Run one concurrency test with `-race -count=100`.

**If you only remember three things:** every goroutine needs a stop path, every channel needs one
owner, and the race detector is evidence — not proof.
