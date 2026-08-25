# Concurrency Cheat Sheet

Quick reference for goroutines, channels, synchronization and distributed-systems patterns in Go.

## 🧵 Goroutines

```go
go doWork()                       // fire and forget
go func(n int) { ... }(x)         // args evaluated NOW, not at run time
```

| Fact | Detail |
|------|--------|
| Initial stack | ~2 KB, grows by copying |
| `main` returns | Process exits — all goroutines killed |
| Panic in any goroutine | Kills the **whole process** |
| `recover()` | Only works in the goroutine that panicked |
| Loop variable | Per-iteration since Go 1.22 (controlled by `go.mod`) |
| Count them | `runtime.NumGoroutine()` |

---

## 📡 Channel axioms

| Operation | Result |
|-----------|--------|
| Send on `nil` | **Blocks forever** |
| Receive on `nil` | **Blocks forever** |
| Send on **closed** | **panic** |
| Receive on **closed** | Zero value, `ok == false` |
| Close a **closed** | **panic** |
| Close a `nil` | **panic** |

```go
ch := make(chan int)        // unbuffered — rendezvous, backpressure
ch := make(chan int, 10)    // buffered — decouples speeds

v, ok := <-ch               // ok == false -> closed and drained
for v := range ch { }       // ends on close
close(ch)                   // SENDER closes. Broadcasts to all receivers.

func send(out chan<- int)   // send-only
func recv(in <-chan int)    // receive-only
```

**Multiple senders?** Nobody can close safely — use a `WaitGroup` + closer goroutine:

```go
go func() { wg.Wait(); close(results) }()
```

---

## 🔀 select

```go
select {
case v := <-ch:        // receive
case ch <- x:          // send
case <-ctx.Done():     // cancellation
case <-timer.C:        // timeout
default:               // non-blocking
}
```

- Several ready → picks **uniformly at random**
- `default` → never blocks
- Set a channel to `nil` → **permanently disables** that case

```go
// Timeout
timer := time.NewTimer(2 * time.Second)
defer timer.Stop()
select {
case r := <-work:   return r, nil
case <-timer.C:     return nil, errTimeout
}

// Load shedding
select {
case queue <- job:  // accepted
default:            // full — drop or 503
}

// Merge until both close
for a != nil || b != nil {
    select {
    case v, ok := <-a: if !ok { a = nil; continue }; use(v)
    case v, ok := <-b: if !ok { b = nil; continue }; use(v)
    }
}
```

---

## 🔒 sync

```go
var mu sync.Mutex                 // zero value ready
mu.Lock(); defer mu.Unlock()
if mu.TryLock() { ... }           // Go 1.18+

var rw sync.RWMutex
rw.RLock(); defer rw.RUnlock()    // many readers
rw.Lock();  defer rw.Unlock()     // one writer

var wg sync.WaitGroup
wg.Add(1)                         // BEFORE `go`
go func() { defer wg.Done(); ... }()
wg.Wait()

var once sync.Once
once.Do(func() { ... })           // blocks all callers until f RETURNS
get := sync.OnceValue(func() T { ... })   // Go 1.21+
```

> ⚠️ **Never copy a sync type after first use.** Pass by pointer. `go vet` checks this.
> ⚠️ **Mutexes are NOT reentrant** — use the `xxxLocked` helper convention.

---

## ⚛️ sync/atomic

```go
var n atomic.Int64                // Go 1.19+ typed atomics — prefer these
n.Add(1)
n.Load()
n.Store(5)
n.CompareAndSwap(old, new)        // returns false if someone beat you

var flag atomic.Bool
var cfg  atomic.Pointer[Config]   // swap an immutable snapshot atomically
```

**Atomics protect exactly one word.** Two related fields still need a mutex.

---

## 🎯 context

```go
ctx, cancel := context.WithCancel(parent)
ctx, cancel := context.WithTimeout(parent, 2*time.Second)
ctx, cancel := context.WithDeadline(parent, t)
defer cancel()                    // ALWAYS, no exceptions

<-ctx.Done()                      // closed on cancel/timeout
ctx.Err()                         // Canceled | DeadlineExceeded
dl, ok := ctx.Deadline()
```

| Check | Meaning |
|-------|---------|
| `errors.Is(err, context.Canceled)` | Client left — **do not retry** |
| `errors.Is(err, context.DeadlineExceeded)` | Too slow — maybe retry elsewhere |

```go
// Go 1.20+ : why, not just that
ctx, cancel := context.WithCancelCause(parent)
cancel(errQuotaExceeded)
context.Cause(ctx)

// Go 1.21+
detached := context.WithoutCancel(ctx)      // keep values, drop cancellation
stop := context.AfterFunc(ctx, cleanup)     // callback instead of a goroutine
```

**Rules:** first parameter · named `ctx` · never in a struct · never `nil` (use `TODO()`) · deadlines only shrink · `WithValue` only for request metadata, with an unexported key type.

---

## 👷 Worker pool

```go
results := make(chan Result)
var wg sync.WaitGroup

for w := 0; w < n; w++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        for job := range jobs {           // all workers, one channel
            select {
            case results <- process(ctx, job):
            case <-ctx.Done(): return
            }
        }
    }()
}
go func() { wg.Wait(); close(results) }()  // dedicated closer
```

| Work | Size |
|------|------|
| CPU-bound | `runtime.GOMAXPROCS(0)` |
| I/O-bound | Whatever the **downstream** can take |

Always drain `results` fully, even when aborting.

---

## 🔧 Pipeline stage template

```go
func stage(ctx context.Context, in <-chan T) <-chan U {
    out := make(chan U)
    go func() {
        defer close(out)              // this stage OWNS out
        for v := range in {
            select {
            case out <- transform(v):
            case <-ctx.Done(): return
            }
        }
    }()
    return out
}
```

Fan-out: N stages read the same input. Fan-in: `merge()` with `WaitGroup` + closer. **Fan-in destroys ordering.**

---

## 🛡️ Resilience one-liners

```go
// Semaphore — a buffered channel IS a counting semaphore
sem := make(chan struct{}, 10)
select {
case sem <- struct{}{}:
    defer func() { <-sem }()
case <-ctx.Done():
    return ctx.Err()
}

// Backoff with full jitter
delay := base * time.Duration(1<<(attempt-1))
if delay > maxDelay { delay = maxDelay }
jittered := time.Duration(rand.Int63n(int64(delay)))
```

| Problem | Pattern |
|---------|---------|
| Too many at once | Semaphore / `errgroup.SetLimit(n)` |
| Too many per second | Rate limiter (`golang.org/x/time/rate`) |
| Transient failure | Retry + exponential backoff + **jitter** |
| Peer already down | Circuit breaker (fail fast) |
| Duplicate concurrent work | Singleflight |
| Bad p99 | Hedged requests (budget them) |

**Compose outermost first:** `breaker → rate limit → semaphore → retry → timeout → call`

---

## 🛑 Graceful shutdown

```go
ctx, stop := signal.NotifyContext(context.Background(),
    os.Interrupt, syscall.SIGTERM)
defer stop()
<-ctx.Done()

shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
defer cancel()

srv.Shutdown(shutdownCtx)   // 1. stop accepting  2. drain
stopWorkers()               // 3. background goroutines
db.Close()                  // 4. resources
```

1. Fail readiness → 2. drain in-flight (**with a deadline**) → 3. cancel workers → 4. close resources. Let a second signal hard-exit.

---

## 🐛 Debugging

```bash
go test -race ./...                  # ~2-20x slower; CI only
go test -race -count=100 ./...       # shake out rare races
kill -QUIT <pid>                     # dump all goroutine stacks
GOTRACEBACK=all ./app
curl localhost:6060/debug/pprof/goroutine?debug=2
```

```go
defer goleak.VerifyNone(t)           // go.uber.org/goleak
runtime.NumGoroutine()               // cheapest leak metric
```

| Symptom | Likely cause |
|---------|--------------|
| Goroutine count climbs forever | Leak — missing `ctx.Done()` or unclosed channel |
| `all goroutines are asleep - deadlock!` | **Total** deadlock (partial ones are silent) |
| Wrong counts under load | Data race — run `-race` |
| Correct locking but wrong results | **TOCTOU** — `-race` will **not** find it |
| Requests hang, no panic | Partial deadlock — get a stack dump |
| Latency spikes in containers | `GOMAXPROCS` ignoring the cgroup CPU limit |

---

## ⚠️ Top 10 mistakes

1. `wg.Add(1)` **inside** the goroutine → races with `Wait`
2. Forgetting `defer cancel()` → context and timer leak
3. Unbuffered result channel with abandoned senders → leak
4. Receiver closing a channel → sender panics
5. Not checking `ctx.Done()` in a blocking send → leak
6. `go handle(x)` per item → unbounded concurrency
7. Inconsistent lock ordering → AB-BA deadlock
8. Re-locking a mutex you already hold → instant self-deadlock
9. Composing two thread-safe calls and assuming the result is thread-safe → TOCTOU
10. Retrying without jitter → synchronized thundering herd

---

## 🚦 Which primitive?

| Need | Use |
|------|-----|
| Pass ownership of data | **channel** |
| Guard struct fields in place | **`sync.Mutex`** beside them |
| Read-mostly state | **`sync.RWMutex`** |
| One counter or flag | **`sync/atomic`** |
| Swap a whole snapshot | **`atomic.Pointer[T]`** |
| Init exactly once | **`sync.Once`** |
| Wait for N goroutines | **`sync.WaitGroup`** |
| …and collect the first error | **`errgroup.Group`** |
| Cap concurrency | **buffered channel** / `SetLimit` |
| Cancellation and deadlines | **`context.Context`** |

> **Channels for handoff. Locks for in-place state.** Do not build a "counter server" out of channels.

---

## 🔗 Related

- [Concurrency](concurrency.md) — full guide
- [Synchronization](synchronization.md) — `sync` and `sync/atomic` in depth
- [Goroutine Problems](goroutine-problems.md) — leaks, races, deadlocks
- [Concurrency Patterns](concurrency-patterns.md) — distributed-systems patterns
- [Runnable examples](../examples/) — files 11–21

---

**Remember:** the compiler cannot check concurrency. Every goroutine you start needs a defined way to stop. 🧵
