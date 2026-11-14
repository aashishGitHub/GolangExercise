# Day 2 — Concurrency, context, and the patterns you will be asked to write

> **New to Go?** Read [`day-2a-concurrency.md`](day-2a-concurrency.md) first. It covers the same concepts in the same order, with slower explanations and one example per idea.

**Goal:** be able to design, write, and debug bounded concurrent Go without hand-waving. By the end
you can implement a worker pool with cancellation, explain why every goroutine stops, and name the
race each ControlPlane mechanism protects.

**You should be able to answer:** GMP, buffered vs unbuffered, close rules, `select`, context
propagation, mutex vs channel, happens-before, leak patterns, `errgroup`, and why the consumer is
sequential.

---

## Timebox (120 min)

| Time | Block |
|---|---|
| 0:00–0:10 | Cold recall: draw G-M-P and the channel axioms |
| 0:10–0:55 | Mechanisms below |
| 0:55–1:25 | Active practice: worker pool + three broken snippets |
| 1:25–1:45 | Grill |
| 1:45–1:55 | Explain-back: "how I would build a bounded fan-out" |
| 1:55–2:00 | Log weak spots |

---

## 1. The scheduler in one page

Go runs goroutines on OS threads using the **G-M-P** model:

| Piece | Meaning |
|---|---|
| **G** | Goroutine: a small stack (starts ~2 KB) and instruction pointer |
| **M** | Machine: an OS thread |
| **P** | Processor: a logical scheduling context; `GOMAXPROCS` of them |

Each P has a local run queue. An M must hold a P to run Go code. When a goroutine blocks in a syscall,
the M can hand off its P so other goroutines keep running. Idle Ps steal work from busy Ps. Since Go
1.14, long-running goroutines can be preempted asynchronously.

Why it matters:

- Goroutines are cheap, but not free: each has a stack and scheduling cost.
- On Linux, Go 1.25 and newer set `GOMAXPROCS` from the cgroup CPU bandwidth limit by default, and
  update it when the limit changes. Setting `GOMAXPROCS` manually disables this. Older Go versions
  use the CPUs visible to the process, which can exceed the container limit and cause throttling;
  set `GOMAXPROCS` from the cgroup limit or use `automaxprocs` there.
- `runtime.NumGoroutine()` is a cheap leak signal in tests and metrics.
- `main` returning kills every goroutine. A goroutine has no parent-child relationship.

**Goroutine vs OS thread:** goroutines are multiplexed onto threads, have growable stacks, and are
scheduled by the runtime; threads are scheduled by the OS and are far more expensive to create and
context-switch.

---

## 2. Channels: semantics, ownership, close

```go
ch := make(chan int)     // unbuffered: send blocks until a receiver is ready
ch := make(chan int, 8)  // buffered: send blocks only when full
```

| Operation | Result |
|---|---|
| Send/receive on `nil` | Blocks forever |
| Send on closed | Panic |
| Receive on closed | Zero value, `ok == false` |
| Close a closed/nil channel | Panic |
| `for v := range ch` | Ends when the channel is closed and drained |

### Unbuffered vs buffered

- **Unbuffered** is a rendezvous: it synchronizes sender and receiver and gives natural backpressure.
- **Buffered** decouples speeds up to a bound. It is not a queue you can grow forever; a full buffer
  still blocks.
- A buffer does not fix a leak if the receiver never comes. It only delays the block.
- For load shedding, a non-blocking send with `default` is often better than an unbounded queue:

```go
select {
case queue <- job:
    // accepted
default:
    // full: return 503 or drop with a metric
}
```

### Ownership and close

The **sender** closes. A receiver closing is a bug because another sender may still send and panic.
If there are multiple senders, use a `WaitGroup` and one closer goroutine:

```go
go func() {
    wg.Wait()
    close(results)
}()
```

Close means "no more values", not "cancel". Use `context` for cancellation; use close for end-of-stream.
Do not close a channel to signal one receiver if other senders may still use it.

### Directional channels document intent

```go
func produce(out chan<- int)
func consume(in <-chan int)
```

Use them in function signatures. They prevent accidental receives/sends and make ownership obvious.

---

## 3. `select`: the control plane of a goroutine

```go
select {
case v := <-ch:
    use(v)
case ch <- x:
    // sent
case <-ctx.Done():
    return ctx.Err()
case <-timer.C:
    // timeout
default:
    // non-blocking path
}
```

Rules:

- If several cases are ready, one is chosen uniformly at random. Do not rely on priority.
- `default` makes the select non-blocking.
- A `nil` channel disables its case forever. This is useful for merging a variable number of inputs:

```go
for a != nil || b != nil {
    select {
    case v, ok := <-a:
        if !ok { a = nil; continue }
        use(v)
    case v, ok := <-b:
        if !ok { b = nil; continue }
        use(v)
    }
}
```

- `time.After` in a loop allocates a new timer each iteration and the old timer may live until it
  fires. Use `time.NewTimer`/`time.NewTicker` and `Stop` when appropriate.
- A select with no ready case and no default blocks. If every goroutine blocks and no timer/IO can
  wake one, the runtime reports "all goroutines are asleep - deadlock!" — but only for a total
  deadlock. Partial deadlocks are silent; use stack dumps.

---

## 4. `context`: cancellation, deadlines, values

`context.Context` carries cancellation, a deadline, and request-scoped values across API boundaries.

```go
ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
defer cancel()
```

Rules:

- First parameter, named `ctx`. Never store it in a struct.
- Always `defer cancel()` for `WithCancel`, `WithTimeout`, `WithDeadline`.
- Deadlines only shrink as they go down the call chain.
- Check `ctx.Done()` in loops and before expensive work; pass it to DB/broker/HTTP calls.
- `ctx.Err()` is `context.Canceled` or `context.DeadlineExceeded`.
- Use `errors.Is(err, context.Canceled)` to distinguish client disconnect from a real failure. Do not
  retry a canceled request.
- `context.WithValue` is for request metadata (request ID, auth subject), with an unexported key type.
  Not for dependencies or optional parameters.
- `context.WithCancelCause` (Go 1.20) lets you attach a cause; read it with `context.Cause(ctx)`.
- `context.WithoutCancel` keeps values but drops cancellation, useful for best-effort audit writes.
- `context.AfterFunc` runs a callback when the context is done.

**Server rule:** `r.Context()` is canceled when the client disconnects. If you launch background work
that must outlive the request, explicitly detach it with a new context and a bounded lifetime; do not
silently reuse the request context.

---

## 5. `sync` and `sync/atomic`

### Mutex vs channel

| Need | Use |
|---|---|
| Guard fields in place | `sync.Mutex` |
| Pass ownership of data / signal completion | channel |
| Read-mostly shared state | `sync.RWMutex` |
| One counter or flag | `sync/atomic` |
| Initialize exactly once | `sync.Once`/`OnceValue` |
| Wait for N goroutines | `sync.WaitGroup` |
| Wait + first error | `errgroup.Group` |

"Channels for handoff, locks for in-place state." Do not build a counter server out of channels.

### Rules that show up in interviews

- `sync.WaitGroup.Add` must happen **before** the `go` statement, not inside the goroutine; otherwise
  `Wait` can race with `Add`.
- Never copy a type containing a mutex/WaitGroup after first use. Pass a pointer. `go vet` catches some.
- `Mutex` is not reentrant. Calling `Lock` twice in the same goroutine deadlocks.
- `RWMutex` is not automatically faster; it helps read-heavy workloads, but it adds overhead, and a
  waiting writer blocks new readers, so measure it.
- `sync.Once` blocks all callers until the function returns. If it panics, it is considered done; a
  later call will not retry.
- `sync.Pool` is for temporary objects, not a cache with guarantees. Items can be dropped at GC.
- Atomics protect one word. Two related fields still need a mutex or an immutable snapshot swapped
  with `atomic.Pointer`.

---

## 6. The memory model, races, and what `-race` proves

A **data race** is two goroutines accessing the same memory concurrently, at least one writing, with
no synchronization. It is undefined behavior in Go, even if it "works" on your machine.

**Happens-before** edges you can rely on:

- A send on a channel happens before the corresponding receive completes.
- A receive from a closed channel happens after the close.
- `mu.Unlock()` happens before a later `mu.Lock()`.
- `once.Do(f)` happens before any `once.Do` returns.
- An atomic operation synchronizes with atomics on the same variable.
- `go f()` happens before `f` starts, but there is no guarantee about when it finishes.

A **race condition** is a correctness bug where timing changes the result, even if there is no data
race. Example: two goroutines both read `balance`, both see 100, both write 90 — each access is
locked, but the read-modify-write is not atomic. A mutex around the whole operation fixes it; a mutex
around each individual read/write does not.

`go test -race` detects unsynchronized memory accesses that actually occur during the test. It does
not prove the absence of races, and it does not detect logical race conditions or TOCTOU bugs.

---

## 7. Patterns to be able to write from memory

### Bounded worker pool with `errgroup`

```go
import "golang.org/x/sync/errgroup"

func runWorkers(ctx context.Context, jobs <-chan Job, workers int) error {
    g, ctx := errgroup.WithContext(ctx)

    for i := 0; i < workers; i++ {
        g.Go(func() error {
            for {
                select {
                case <-ctx.Done():
                    return ctx.Err()
                case job, ok := <-jobs:
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
    return g.Wait()
}
```

The producer must also select on `ctx.Done()` when sending, or it can block forever after a worker
error cancels the context. `errgroup` returns the first non-nil error; with `WithContext`, it cancels
the derived context when that happens.

### Bounded concurrency without a worker pool

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(8) // at most 8 goroutines in flight

for _, item := range items {
    item := item
    g.Go(func() error {
        return fetch(ctx, item)
    })
}
return g.Wait()
```

`SetLimit` blocks `Go` when the limit is reached. Check `g.TryGo` if you want non-blocking admission.
Do not launch one goroutine per item without a limit; that is how a spike becomes an outage.

### Fan-out / fan-in

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
        close(out)
    }()
    return out
}
```

Fan-in destroys ordering. If order matters, carry a sequence number and reorder downstream, or use a
single consumer.

### Pipeline stage

```go
func stage(ctx context.Context, in <-chan int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out) // this stage owns out
        for v := range in {
            select {
            case out <- v * 2:
            case <-ctx.Done():
                return
            }
        }
    }()
    return out
}
```

Each stage owns its output channel and closes it. Cancellation must be checked on both the input and
the output send.

### Semaphore and rate limiter

```go
sem := make(chan struct{}, 10)
select {
case sem <- struct{}{}:
    defer func() { <-sem }()
case <-ctx.Done():
    return ctx.Err()
}
```

A buffered channel is a counting semaphore. For per-second limits, use
`golang.org/x/time/rate`:

```go
limiter := rate.NewLimiter(rate.Every(100*time.Millisecond), 5)
if err := limiter.Wait(ctx); err != nil {
    return err
}
```

### Retry with exponential backoff + jitter

```go
func retry(ctx context.Context, attempts int, fn func() error) error {
    var err error
    delay := 100 * time.Millisecond

    for i := 0; i < attempts; i++ {
        if err = fn(); err == nil || !isTransient(err) {
            return err
        }
        if i == attempts-1 {
            break
        }
        jitter := time.Duration(rand.Int64N(int64(delay)))
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(delay/2 + jitter):
        }
        delay = min(delay*2, 5*time.Second)
    }
    return err
}
```

Retry only transient errors, bound the attempts, respect the context, and add jitter so clients do not
retry in lockstep.

### Graceful shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

srv := &http.Server{Addr: ":8080", Handler: handler}
go func() {
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    _ = srv.Shutdown(shutdownCtx)
}()

if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    return err
}
```

Order: fail readiness, stop accepting, drain in-flight with a deadline, cancel background workers,
close DB/broker. Let a second signal hard-exit.

### SSE with client disconnect

```go
func stream(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "streaming unsupported", http.StatusInternalServerError)
        return
    }

    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-r.Context().Done():
            return
        case <-ticker.C:
            fmt.Fprint(w, "event: heartbeat\ndata: {}\n\n")
            flusher.Flush()
        }
    }
}
```

SSE is one-way server push over HTTP. Watch for proxy buffering, send heartbeats, disable compression
for the stream, and handle auth on the initial request. For bidirectional, use WebSocket.

---

## 8. Leaks and failure modes

| Symptom | Likely cause | Fix |
|---|---|---|
| Goroutine count climbs forever | Blocked send/receive, missing `ctx.Done()` | Give every goroutine a stop path; buffer or cancel |
| Request hangs, no panic | Partial deadlock | Stack dump, context timeout, lock ordering |
| Wrong counts under load | Data race | `-race`, mutex/atomic, redesign |
| Correct locks, wrong result | TOCTOU / logical race | Lock the whole read-modify-write or use a DB conditional update |
| Memory grows | `time.After` in loop, retained slices, unbounded queue | Ticker, copy/clear, bounded queue |
| Latency spikes in containers | `GOMAXPROCS` exceeds the cgroup CPU limit (older Go) | Go 1.25+ handles it; on older versions set it or use `automaxprocs` |
| Panic kills service | Panic in an unrecovered goroutine | Recover at the boundary, fix the bug |

**Every goroutine needs a defined way to stop.** Ask: who closes the channel, who cancels the context,
and what happens if the downstream never reads?

---

## 9. Active practice (30 min)

### Drill 1 — worker pool (15 min)

Implement `runWorkers` above in a scratch file. Add:

- A producer that sends 100 jobs and stops on `ctx.Done()`.
- Three workers.
- A `process` that sleeps 10 ms and fails on job 42.
- `errgroup.WithContext` so the first error cancels the rest.
- A test that runs with `-race` and asserts the pool returns within 2 seconds.

Then answer: if the producer uses `jobs <- job` without a `select`, what happens after job 42 fails?

### Drill 2 — fix the leaks (10 min)

Each snippet is broken. Name the bug and fix it without changing the intended behavior.

```go
// A: first result, but the other goroutines never stop
func first(urls []string) string {
    ch := make(chan string)
    for _, u := range urls {
        go func() { ch <- fetch(u) }()
    }
    return <-ch
}
```

```go
// B: total deadlock
func main() {
    ch := make(chan int)
    ch <- 1
    fmt.Println(<-ch)
}
```

```go
// C: data race
func main() {
    var n int
    var wg sync.WaitGroup
    for i := 0; i < 1000; i++ {
        wg.Add(1)
        go func() { defer wg.Done(); n++ }()
    }
    wg.Wait()
    fmt.Println(n)
}
```

```go
// D: timer churn
for {
    select {
    case <-time.After(time.Second):
        doWork()
    }
}
```

### Drill 3 — explain the ControlPlane choices (5 min)

Answer aloud:

1. Why does the worker use `Qos(1)` and process deliveries sequentially?
2. Why is the consumer's `for` loop sequential rather than one goroutine per delivery?
3. Why does the outbox relay use a buffered `wake` channel with a non-blocking send?
4. Why is Go concurrency not the mechanism that prevents two tenants with the same slug?

---

## 10. Grill

<details>
<summary><strong>Q1. Goroutine vs OS thread?</strong></summary>

Goroutines are cheap, growable-stack units scheduled by the Go runtime onto OS threads (M:N). Threads
are OS-scheduled and expensive. Goroutines start around 2 KB and can grow; a thread has a fixed, larger
stack.

</details>

<details>
<summary><strong>Q2. What is G-M-P?</strong></summary>

G is a goroutine, M is an OS thread, P is a logical processor holding a run queue. An M must hold a P
to execute Go code; blocked syscalls can hand off the P; idle Ps steal work. `GOMAXPROCS` controls the
number of Ps.

</details>

<details>
<summary><strong>Q3. Unbuffered vs buffered channel?</strong></summary>

Unbuffered is a rendezvous: send blocks until a receiver is ready. Buffered decouples sender and
receiver up to capacity; send blocks only when full. Buffering changes timing, not the need for a
receiver or a stop path.

</details>

<details>
<summary><strong>Q4. Who closes a channel, and what happens on send/receive after close?</strong></summary>

The sender closes. Send on closed panics; receive returns the zero value with `ok == false`; close of a
closed or nil channel panics. Multiple senders need a `WaitGroup` and one closer.

</details>

<details>
<summary><strong>Q5. What does a nil channel do in a select?</strong></summary>

It blocks forever, so its case is effectively disabled. Setting a channel variable to nil is a common
way to stop selecting on an input after it closes.

</details>

<details>
<summary><strong>Q6. If two select cases are ready, which runs?</strong></summary>

One is chosen uniformly at random. There is no priority. If you need priority, nest selects or use a
separate state machine.

</details>

<details>
<summary><strong>Q7. Why `defer cancel()` if the context is about to be discarded?</strong></summary>

`cancel` releases the timer and the parent's reference to the child context. Without it, the context
and its timer can live until the deadline, leaking memory and goroutines. `go vet` flags a missing
cancel in some cases.

</details>

<details>
<summary><strong>Q8. Mutex or channel?</strong></summary>

Mutex for guarding in-place state; channel for handing off ownership or signaling. Channels are not a
universal replacement for locks, and a mutex is not a message queue. Use the simplest primitive that
makes the invariant obvious.

</details>

<details>
<summary><strong>Q9. What is happens-before, and why do you care?</strong></summary>

It is the partial order of memory operations the Go memory model guarantees. If A happens before B, B
observes A's writes. Channel send/receive, mutex unlock/lock, `Once`, and atomics create edges. Without
an edge, concurrent access is a data race.

</details>

<details>
<summary><strong>Q10. Data race vs race condition?</strong></summary>

A data race is unsynchronized concurrent access with at least one write; it is undefined behavior. A
race condition is a timing-dependent correctness bug that can exist even with locking (e.g. a
read-modify-write split across two locked calls). `-race` finds the former, not the latter.

</details>

<details>
<summary><strong>Q11. Why must `WaitGroup.Add` happen before `go`?</strong></summary>

`Add` and `Wait` must not race. If `Add` is inside the goroutine, `Wait` can return before the goroutine
increments the counter. Call `Add` in the parent before starting the goroutine.

</details>

<details>
<summary><strong>Q12. What does `errgroup.WithContext` do?</strong></summary>

It returns a group and a derived context. The first non-nil error from a `Go` function cancels the
derived context, and `Wait` returns that first error. It does not stop goroutines automatically; each
goroutine must observe the context.

</details>

<details>
<summary><strong>Q13. How do you bound concurrency?</strong></summary>

Use a fixed worker pool, `errgroup.SetLimit`, or a buffered-channel semaphore. Never spawn one
goroutine per item without a limit. Size CPU-bound work near `GOMAXPROCS`; size I/O-bound work to what
the downstream can handle.

</details>

<details>
<summary><strong>Q14. How do you find a goroutine leak?</strong></summary>

Watch `runtime.NumGoroutine()` or a goroutine metric; take a stack dump with `SIGQUIT` or
`pprof/goroutine?debug=2`; use `goleak` in tests. Common causes are blocked sends/receives, missing
cancellation, and tickers/timers that are never stopped.

</details>

<details>
<summary><strong>Q15. Why is `time.After` in a loop a problem?</strong></summary>

Each call creates a timer. In a long-running loop, timers and their channels can accumulate until they
fire, causing memory/GC pressure. Use a `time.Ticker` or `time.NewTimer` with `Stop`/`Reset`.

</details>

<details>
<summary><strong>Q16. What happens if a goroutine panics and nobody recovers?</strong></summary>

The whole process crashes. `recover` only works in a deferred function in the panicking goroutine. Use
a recover boundary in HTTP middleware and message handlers; do not swallow panics silently.

</details>

<details>
<summary><strong>Q17. Why does the ControlPlane consumer process deliveries sequentially?</strong></summary>

The consumer wants at most one unacked delivery per consume loop and relies on the broker's prefetch
and delivery limit for backpressure. Sequential processing keeps ordering and failure classification
simple; the DB transaction is the concurrency boundary, not a goroutine per message. Parallel consumers
would need per-tenant ordering and more careful inbox/lock behavior.

</details>

<details>
<summary><strong>Q18. Why does the worker use prefetch 1?</strong></summary>

Tasks are long-running and sequential. Prefetch 1 means at most one unacked task is in flight, so a
crash causes exactly one redelivery, and the worker does not hoard work it cannot process. It is a
deliberate throughput-for-simplicity tradeoff.

</details>

<details>
<summary><strong>Q19. What is the biggest concurrency mistake in a Go web service?</strong></summary>

Unbounded goroutines per request or per item, with no context cancellation and no downstream limit.
It looks fast in a demo and collapses under load. Bound it, propagate context, and measure queue
depth/latency.

</details>

<details>
<summary><strong>Q20. Why is Go concurrency not the fix for the slug race?</strong></summary>

The race is across processes/replicas and survives any in-process mutex. The database unique index is
the arbiter: two concurrent INSERTs, one commits, the other gets `23505`. Go concurrency only decides
which goroutine sends the INSERT; Postgres decides who wins.

</details>

---

## 11. ControlPlane tie-in

| Mechanism | File | Senior point |
|---|---|---|
| Signal context + graceful shutdown | [`cmd/controlplane/main.go`](../../Avid/ControlPlane/cmd/controlplane/main.go) | One cancellation tree stops HTTP, relay, and consumer; 10 s drain. |
| Reconnect loop with backoff | [`internal/messaging/connection.go`](../../Avid/ControlPlane/internal/messaging/connection.go) | `NotifyClose`, redial, re-declare idempotent topology. |
| Sequential consumer | [`internal/messaging/consumer.go`](../../Avid/ControlPlane/internal/messaging/consumer.go) | Ack after handler transaction; poison vs transient; bounded retry. |
| Non-blocking wake | [`internal/outbox/relay.go`](../../Avid/ControlPlane/internal/outbox/relay.go) | Buffered `wake` of 1; poll is the safety net; no goroutine leak. |
| Prefetch 1 | [`internal/worker/worker.go`](../../Avid/ControlPlane/internal/worker/worker.go) | One unacked task; crash = one redelivery. |
| Worker pool / DB locks | [`internal/service/concurrency_test.go`](../../Avid/ControlPlane/internal/service/concurrency_test.go) | 50 goroutines prove the DB arbiter; not a Go mutex. |

**Say this in the interview:** "I use goroutines for I/O concurrency and cancellation, but I do not use
them as a correctness mechanism. The slug race and the version race are settled by Postgres constraints
and conditional updates. Go's job is to issue those statements with bounded concurrency and to stop
cleanly."

---

## 12. End-of-day checkpoint

- [ ] G-M-P and why `GOMAXPROCS` matters in containers.
- [ ] Channel axioms, close ownership, nil channel.
- [ ] `select` randomness, default, nil-disable, timer leaks.
- [ ] Context propagation and `defer cancel()`.
- [ ] Mutex vs channel; `WaitGroup.Add` before `go`.
- [ ] Happens-before; data race vs race condition.
- [ ] Bounded worker pool, `errgroup.SetLimit`, semaphore, retry with jitter.
- [ ] Every goroutine has a stop path; how to find a leak.

**If you only remember five things:** bound concurrency, propagate context, sender closes, `Add` before
`go`, `-race` is evidence not proof.

**Next:** [`day-3-http-db.md`](day-3-http-db.md).
