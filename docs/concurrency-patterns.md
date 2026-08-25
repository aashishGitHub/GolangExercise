# Concurrency Patterns for Distributed Systems

## Overview

The primitives are the easy part. What separates a service that survives a bad day from one that amplifies it is **which patterns you compose and in what order**.

Every pattern here answers one question: *what does this service do when the thing it depends on is slow, broken, or overloaded?*

| Pattern | Problem it solves |
|---------|-------------------|
| [Worker pool](#-worker-pool) | Unbounded concurrency overwhelming a downstream |
| [Pipeline](#-pipeline) | Multi-stage processing with backpressure |
| [Fan-out / fan-in](#-fan-out--fan-in) | Parallelising a slow stage |
| [Context propagation](#-context-propagation) | Cancelling an entire request tree |
| [Semaphore](#1-semaphore--bound-concurrency) | Too many concurrent calls |
| [Rate limiter](#2-rate-limiter--token-bucket) | Too many calls per second |
| [Retry + backoff](#3-retry-with-exponential-backoff--jitter) | Transient failures |
| [Circuit breaker](#4-circuit-breaker) | Hammering a peer that is already down |
| [Singleflight](#5-singleflight) | Cache stampede / duplicate work |
| [Hedged requests](#6-hedged-requests) | Tail latency |
| [Graceful shutdown](#-graceful-shutdown) | Dropping in-flight work on deploy |

---

## 👷 Worker pool

**The most important pattern in server code.**

```go
for _, job := range jobs {
    go handle(job)      // ❌ unbounded
}
```

100k jobs means 100k goroutines and 100k in-flight queries. A worker pool caps concurrency at N, turning unbounded fan-out into a queue with a **known, tunable depth**.

```go
func workerPool(ctx context.Context, jobs <-chan Job, n int) <-chan Result {
    results := make(chan Result)
    var wg sync.WaitGroup

    for w := 0; w < n; w++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for job := range jobs {          // all workers share one channel
                r := process(ctx, job)
                select {
                case results <- r:
                case <-ctx.Done():
                    return                   // collector gone — do not block
                }
            }
        }(w)
    }

    go func() {                              // dedicated closer
        wg.Wait()
        close(results)
    }()
    return results
}
```

### Why every piece is there

| Piece | Reason |
|-------|--------|
| All workers `range` the **same** `jobs` channel | The runtime distributes work automatically — a slow worker simply takes fewer jobs. No dispatcher, no partitioning, no locks. |
| The **producer** closes `jobs` | Ends every worker's `range` |
| A **separate closer goroutine** closes `results` | It must close exactly once, only after every worker stopped. Closing from a worker would panic the others mid-send. |
| `ctx.Done()` on the send | Otherwise an abandoned collector leaks every worker |

### Sizing

| Work type | Pool size |
|-----------|-----------|
| CPU-bound | `runtime.GOMAXPROCS(0)` — more just adds context switches |
| I/O-bound | **What the downstream can take** — its connection pool, its rate limit |

Sizing to your own CPU count for I/O work is a common mistake; the constraint lives in the *other* system.

### Two things that bite

- **Results arrive in completion order, not submission order.** If you need input order, index by a job ID and reassemble: `ordered[r.JobID] = r.Output`. No coordination between workers required.
- **Always drain `results` to completion, even when aborting.** Breaking out of the loop early blocks every worker on its send.

In production, `errgroup` does the plumbing:

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(4)
for _, job := range jobs {
    g.Go(func() error { return process(ctx, job) })
}
err := g.Wait()      // first error, with ctx already cancelled for the rest
```

▶️ [`17-worker-pool.go`](../examples/17-worker-pool.go)

---

## 🔧 Pipeline

A chain of stages connected by channels. Each stage receives from inbound, does one thing, and sends on an outbound channel **it owns and closes**.

```go
func square(ctx context.Context, in <-chan int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out)              // this stage owns out
        for n := range in {           // ends when upstream closes
            select {
            case out <- n * n:
            case <-ctx.Done():
                return
            }
        }
    }()
    return out
}

// Composes like a shell pipe:
result := filter(ctx, square(ctx, generate(ctx, nums...)))
```

### Why this shape wins

- Every stage runs concurrently, so throughput is the **slowest stage**, not the sum of all stages.
- Unbuffered channels give **backpressure for free** — a slow consumer throttles the producer instead of queueing into an OOM.
- Each stage is an ordinary function you can unit test in isolation.

### Rules

1. Each stage **owns and closes** its output channel.
2. Closure **cascades**: `close(out)` ends the next stage's `range`, all the way down.
3. Every send is guarded by `ctx.Done()`.
4. If the consumer exits early, it **must** cancel — otherwise upstream stages stay parked forever holding their items.

▶️ [`18-pipeline-fanin-fanout.go`](../examples/18-pipeline-fanin-fanout.go)

---

## 🔱 Fan-out / fan-in

**Fan-out** — several goroutines read from one channel, parallelising a slow stage.
**Fan-in** — merge several channels back into one.

```go
// Fan-out: 4 stages reading the SAME source
workers := make([]<-chan int, 4)
for i := range workers {
    workers[i] = square(ctx, source)
}

// Fan-in
merged := merge(ctx, workers...)
```

```go
func merge(ctx context.Context, chans ...<-chan int) <-chan int {
    out := make(chan int)
    var wg sync.WaitGroup

    wg.Add(len(chans))
    for _, c := range chans {
        go func(c <-chan int) {
            defer wg.Done()
            for v := range c {
                select {
                case out <- v:
                case <-ctx.Done():
                    return
                }
            }
        }(c)
    }

    go func() { wg.Wait(); close(out) }()   // same closer idiom
    return out
}
```

Measured in the example: 8 items × 20 ms drops from **170 ms to 40 ms** with a fan-out of 4.

> **Fan-in destroys ordering.** Sort afterwards or carry an index.

---

## 🎯 Context propagation

`context.Context` answers one question at every level of the call stack: *should I still be doing this?*

When a client hangs up or a deadline passes, every branch of the request tree must stop — otherwise you keep burning CPU, connections and downstream quota on an answer nobody will read.

```go
func Handler(ctx context.Context) error {
    ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
    defer cancel()

    return callDownstream(ctx)
}
```

### Rules

| Rule | Why |
|------|-----|
| First parameter, named `ctx` | Universal Go convention |
| Never store in a struct field | Contexts are per-call, not per-object |
| Never pass `nil` — use `context.TODO()` | `nil` panics on use |
| **`defer cancel()` always** | Even when the deadline will fire anyway — it releases the timer |
| `select` on `ctx.Done()` in every blocking op | Otherwise cancellation cannot reach you |

### Deadlines only shrink

A child can shorten a deadline but never extend it. Asking for 5 s under a 50 ms parent gives you 50 ms. This is what stops a downstream call from outliving its caller.

### Distinguish the two errors

```go
errors.Is(err, context.Canceled)         // the CLIENT left — retrying is waste
errors.Is(err, context.DeadlineExceeded) // too slow — maybe retry another replica
```

### Modern additions

| API | Go | Use |
|-----|-----|-----|
| `WithCancelCause` / `Cause` | 1.20 | Attach *why* it was cancelled, for logs and metrics |
| `WithoutCancel` | 1.21 | Keep values, drop cancellation — for audit writes that must outlive the request (give it its own timeout) |
| `AfterFunc` | 1.21 | Run cleanup on cancellation without dedicating a goroutine to `<-ctx.Done()` |

### `WithValue` — use sparingly

Only for **request-scoped metadata that crosses API boundaries**: trace IDs, auth principals, locale. Never for optional parameters — those belong in the signature where the compiler checks them.

Always use an unexported key type, or another package can collide with your key:

```go
type ctxKey int
const requestIDKey ctxKey = iota
```

Lookup walks *up* the tree, so it is O(depth), not O(1).

▶️ [`19-context-patterns.go`](../examples/19-context-patterns.go)

---

## 🛡️ Resilience patterns

The failure these collectively prevent is the **retry storm**: a peer slows down, every caller retries, extra load makes it slower, more retries pile on, and a partial outage becomes a total one.

### 1. Semaphore — bound concurrency

A buffered channel **is** a counting semaphore. Capacity = permits.

```go
type Semaphore chan struct{}

func (s Semaphore) Acquire(ctx context.Context) error {
    select {
    case s <- struct{}{}:
        return nil
    case <-ctx.Done():
        return ctx.Err()      // never block unboundedly
    }
}

func (s Semaphore) Release() { <-s }
```

Production alternatives: `golang.org/x/sync/semaphore` (weighted), or `errgroup.SetLimit(n)`.

### 2. Rate limiter — token bucket

Concurrency limits and rate limits are **different controls**. A semaphore caps how many run *at once*; a rate limiter caps how many *start per second*. A fast peer can be overwhelmed by rate even at low concurrency.

```go
// Refill loop, with a stop channel so it doesn't leak
ticker := time.NewTicker(time.Second / time.Duration(perSecond))
defer ticker.Stop()
for {
    select {
    case <-ticker.C:
        select {
        case tokens <- struct{}{}:   // refill
        default:                     // bucket full — never block the ticker
        }
    case <-stop:
        return
    }
}
```

Production alternative: `golang.org/x/time/rate`.

### 3. Retry with exponential backoff + jitter

```go
delay := base * time.Duration(1<<(attempt-1))   // exponential
if delay > maxDelay { delay = maxDelay }
jittered := time.Duration(rand.Int63n(int64(delay)))   // full jitter
```

**Jitter is not optional.** Without it, N clients that failed together retry together — a synchronized thundering herd that re-kills the peer at every backoff boundary.

Two more essentials:

- **Never retry permanent errors** (400, 404, validation). Use `errors.Is` to classify. Retrying them wastes budget and hides bugs.
- **Give the whole retry loop an overall deadline**, or backoff outlives the request it belongs to.

### 4. Circuit breaker

```
CLOSED ──(N consecutive failures)──> OPEN
   ^                                   │
   │                            (cool-off elapses)
   │                                   v
   └──(M consecutive successes)── HALF-OPEN ──(1 failure)──> OPEN
```

The point is to **fail fast when the peer is known-down**. Without a breaker, every caller waits the full timeout on every request, so your goroutines and connections pile up and *your* service falls over too.

Implementation detail that matters: **run the call outside the lock.**

```go
cb.mu.Lock()
// decide whether to allow the call
cb.mu.Unlock()

err := fn()          // <-- no lock held across I/O

cb.mu.Lock()
// record the outcome
cb.mu.Unlock()
```

Holding a mutex across the network call would serialize every caller and defeat the entire purpose.

Production alternative: `github.com/sony/gobreaker`.

### 5. Singleflight

Solves **cache stampede**: a hot key expires, 1000 requests all miss at once, and 1000 identical queries hit the database. Singleflight lets **one** through and hands the same result to the other 999.

Measured in the example: **50 concurrent requests → 1 database query.**

> ⚠️ All callers share the **failure** too — one bad response fans out to everyone waiting.

Production alternative: `golang.org/x/sync/singleflight`.

### 6. Hedged requests

If p99 latency matters more than backend cost, send a second request to another replica after a short delay and take whichever answers first.

```go
results := make(chan answer, 2)    // BUFFERED for both — the loser must exit
go func() { results <- answer{call(ctx, 1), 1} }()

select {
case a := <-results:
    return a                        // primary was fast; no hedge sent
case <-timer.C:
    go func() { results <- answer{call(ctx, 2), 2} }()
    return <-results
}
```

Measured in the example: **200 ms → 50 ms.**

> **Budget it** (e.g. hedge only the slowest 5%), or you add 100% load to fix 1% of latency. Popularized by Google's *The Tail at Scale*.

### Composition order

```
breaker -> rate limit -> semaphore -> retry -> timeout -> call
```

Outermost first. Check the breaker before spending a rate-limit token; take the token before occupying a concurrency slot; retry inside all of them, with an overall deadline.

▶️ [`20-distributed-patterns.go`](../examples/20-distributed-patterns.go)

---

## 🛑 Graceful shutdown

In Kubernetes a pod termination looks like this:

1. The pod is removed from Service endpoints — traffic starts draining
2. **SIGTERM** is delivered
3. … `terminationGracePeriodSeconds` (default 30s) …
4. **SIGKILL** — no cleanup, no flush, no goodbye

Your job is to finish in step 3. A process that ignores SIGTERM drops every in-flight request **on every deploy**.

### The sequence — order matters

```go
ctx, stop := signal.NotifyContext(context.Background(),
    os.Interrupt, syscall.SIGTERM)
defer stop()

<-ctx.Done()          // SIGTERM arrived

shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
defer cancel()

srv.Shutdown(shutdownCtx)   // 1. stop accepting  2. drain in-flight
stopWorkers()               // 3. cancel background goroutines
db.Close()                  // 4. release resources
```

| Step | Detail |
|------|--------|
| 1. Stop accepting | Fail readiness first so the load balancer stops sending traffic |
| 2. Drain in-flight | **Bounded by a deadline** |
| 3. Stop background workers | *After* the drain — in-flight requests may still need them |
| 4. Release resources | DB pools, tracer flush, log sync — last |

### The hard-won details

- **Shutdown itself needs a timeout.** "Wait for everything to finish" turns one stuck request into a SIGKILL that skips steps 3 and 4. If the drain deadline is exceeded, log it and exit non-zero — it means requests *were* dropped, and your per-request timeouts are longer than your grace period.
- **Let a second signal hard-exit.** Call `stop()` to restore default disposition; operators expect an escape hatch when a graceful shutdown wedges.
- **Shut down independent components in parallel** — total time is the slowest, not the sum. Sequence only where there is a real dependency (drain the consumer *before* closing the DB it writes to).
- **Use `errors.Join`** to report every component failure, not just the first.
- **The gate and the counter must move together.** Checking "am I still accepting?" and incrementing an in-flight `WaitGroup` must happen under one mutex — otherwise `Add` can race with `Wait`. The race detector catches this one.

For `net/http` most of this is built in: `srv.Shutdown(ctx)` stops listeners and drains connections, returning `ctx.Err()` if the deadline passes first.

▶️ [`21-graceful-shutdown.go`](../examples/21-graceful-shutdown.go)

---

## ✅ Production checklist

- [ ] Concurrency is bounded everywhere — pool or semaphore, never one goroutine per item
- [ ] Pool size matches the **downstream** limit, not your CPU count
- [ ] Every retry has exponential backoff **with jitter** and an overall deadline
- [ ] Permanent errors are classified and never retried
- [ ] A circuit breaker guards every remote dependency
- [ ] Duplicate concurrent work is collapsed with singleflight where it is hot
- [ ] Every request carries a `context` with a deadline
- [ ] SIGTERM triggers a bounded, ordered graceful shutdown
- [ ] Goroutine count is a monitored metric
- [ ] `go test -race` and `goleak` run in CI

---

## ▶️ Runnable examples

```bash
cd examples
go run 17-worker-pool.go
go run 18-pipeline-fanin-fanout.go
go run 19-context-patterns.go
go run 20-distributed-patterns.go
go run 21-graceful-shutdown.go
```

---

## 🔗 Related

- [Concurrency](concurrency.md)
- [Synchronization](synchronization.md)
- [Goroutine Problems](goroutine-problems.md)
- [Concurrency Cheat Sheet](concurrency-cheatsheet.md)

## 📚 References

- [Go Blog — Go Concurrency Patterns: Pipelines and Cancellation](https://go.dev/blog/pipelines)
- [Go Blog — Context](https://go.dev/blog/context)
- [`golang.org/x/sync/errgroup`](https://pkg.go.dev/golang.org/x/sync/errgroup)
- [`golang.org/x/time/rate`](https://pkg.go.dev/golang.org/x/time/rate)
- Dean & Barroso, *The Tail at Scale*, CACM 2013 — the hedged-requests source
- Nygard, *Release It!* — circuit breaker and bulkhead patterns
