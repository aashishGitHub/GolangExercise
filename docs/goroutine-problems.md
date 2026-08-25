# Goroutine Problems

## Overview

Go makes concurrency easy to *write*. It does not make it easy to get *right*. Four failure modes cause nearly every production incident:

| Problem | Symptom | Detectable by |
|---------|---------|---------------|
| **Goroutine leak** | Memory and goroutine count grow forever | `runtime.NumGoroutine()`, pprof, `goleak` |
| **Data race** | Corrupted values, impossible states, rare crashes | `-race` |
| **Deadlock** | Requests hang; total deadlock panics the process | Stack dump, pprof |
| **Race condition** | Logically wrong results under timing | ❌ **Nothing automatic** |

The last row is the dangerous one: a race *condition* can exist even when every individual memory access is correctly synchronized.

---

## 💧 Goroutine leaks

A goroutine that can never make progress and never returns.

**Nothing reclaims it.** The GC collects unreachable *memory*, but a blocked goroutine is always reachable from the scheduler — it, its stack, and everything it references live until the process dies.

> **The rule:** whoever starts a goroutine must know, *at that moment*, how it will be told to stop. If you cannot answer "what makes this return?", you have written a leak.

### Leak 1 — abandoned sender

```go
// ❌ LEAKS 2 goroutines every call
func first() string {
    ch := make(chan string)          // unbuffered
    for i := 0; i < 3; i++ {
        go func() { ch <- query() }()  // 2 of these block forever
    }
    return <-ch                       // only one value is ever read
}
```

**Fix:** buffer for *every possible sender*, so a send always succeeds whether or not anyone is still listening.

```go
ch := make(chan string, 3)   // ✅
```

### Leak 2 — worker with no stop signal

```go
// ❌ runs until the process exits
go func() {
    for range time.Tick(time.Second) { poll() }
}()
```

**Fix:** take a `context.Context`, and stop the ticker too.

```go
go func() {
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
}()
```

### Leak 3 — forgotten `cancel`

```go
// ❌ the parent holds this child until the deadline fires
ctx, cancel := context.WithTimeout(ctx, time.Hour)
_ = cancel
```

**Fix:** `defer cancel()` on the line after the call, always. `go vet`'s `lostcancel` check catches many of these.

> Watch the ordering: if the deferred `cancel()` cannot run until a goroutine finishes, and that goroutine is waiting on `ctx.Done()`, you have built a deadlock. The worker must be able to exit *either* way — work completing or context cancelled.

### Leak 4 — `range` over a channel nobody closes

```go
// ❌ consumer blocks forever after the last send
go func() { for v := range ch { use(v) } }()
go func() { for i := 0; i < 3; i++ { ch <- i } }()  // never closes
```

**Fix:** the sender closes. `defer close(ch)` in the producer.

### Detecting leaks

```go
// Cheapest possible check
before := runtime.NumGoroutine()
doWork()
time.Sleep(50 * time.Millisecond)   // let things settle
fmt.Println(runtime.NumGoroutine() - before)
```

| Tool | Use |
|------|-----|
| **`go.uber.org/goleak`** | `defer goleak.VerifyNone(t)` — fails tests that leak. The single highest-value thing you can add. |
| **`runtime.NumGoroutine()`** | Export as a metric and alert on the *slope*, not the value |
| **`net/http/pprof`** | `/debug/pprof/goroutine?debug=2` dumps every goroutine's stack |
| **`SIGQUIT`** (Ctrl-`\`) | Dumps all goroutine stacks and exits |
| **`GOTRACEBACK=all`** | Include runtime frames in the dump |

Run [`15-goroutine-leaks.go`](../examples/15-goroutine-leaks.go) to see each leak measured, with its fix at +0 goroutines.

---

## 🏁 Data races

Two goroutines access the same memory concurrently, at least one writes, and there is no synchronization between them.

In Go this is **undefined behaviour** — not "you get one of the two values". The compiler may keep the variable in a register, reorder the access, or tear a multi-word value in half.

```go
counter++    // three operations: load, add, store
```

Two goroutines can both load `5`, both store `6`, and one increment vanishes.

### The four fixes

| Fix | When |
|-----|------|
| **Mutex** | Any critical section |
| **Atomic** | A single word — cheaper |
| **Channel** | Transfer ownership instead of sharing |
| **Don't share** | Shard per goroutine, combine at the end — fastest of all |

The last one is underrated. Give each worker its own slot, then sum after `wg.Wait()`:

```go
perWorker := make([]int, workers)
// ... each goroutine writes only perWorker[id] — distinct memory, no sync needed
wg.Wait()   // Wait establishes happens-before; reading now is safe
```

### The race detector

```bash
go test -race ./...              # in CI, always
go run -race main.go
go build -race -o app main.go
```

| Fact | Detail |
|------|--------|
| Cost | Roughly 2–20× CPU and 5–10× memory — CI and staging, not production |
| Exit code | A race-instrumented **binary** exits **66** after reporting. `go run` wraps it and exits 1, printing `exit status 66`. Either way, non-zero fails CI. |
| Coverage | Only reports races it **actually observes at runtime** |

> **A clean `-race` run is evidence, not proof.** It cannot find a race on a code path your test never exercised. Combine it with load testing and stress runs (`go test -race -count=100`).

---

## 🔄 Race conditions that `-race` cannot see

This is the trap that catches experienced engineers.

```go
// Both methods are individually thread-safe. This function is still broken.
func withdraw(a *Account, amount int) {
    if a.Balance() >= amount {   // lock acquired and RELEASED
        a.Withdraw(amount)       // re-acquired — the world moved on
    }
}
```

Between the check and the act, another goroutine can withdraw. The balance goes negative. **`-race` reports nothing**, because every individual access *was* properly locked.

This is **TOCTOU** (time-of-check to time-of-use).

**Fix — make check-and-act a single atomic operation.** Widen the critical section; do not compose thread-safe calls.

```go
func (a *Account) WithdrawIfAvailable(amount int) bool {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.balance < amount { return false }
    a.balance -= amount
    return true
}
```

> **The general lesson: composing two thread-safe operations does not produce a thread-safe operation.** Thread safety is not compositional. Design your API so callers cannot express the broken sequence.

[`14-race-conditions.go`](../examples/14-race-conditions.go) demonstrates both kinds side by side — the data race is flagged by `-race`, the TOCTOU is not.

---

## 🔗 Deadlocks

A deadlock requires all four **Coffman conditions** simultaneously:

1. **Mutual exclusion** — resources held exclusively
2. **Hold and wait** — a holder requests another resource
3. **No preemption** — you cannot take a lock away
4. **Circular wait** — A waits on B, B waits on A

Break **any one** and deadlock becomes impossible. Go's standard fixes break #4 (lock ordering) or #2 (`TryLock`/timeouts).

### What Go detects — and what it doesn't

```
fatal error: all goroutines are asleep - deadlock!
```

The runtime raises this **only when every goroutine is blocked**. A *partial* deadlock — two goroutines stuck while the rest of your server happily serves traffic — is completely invisible to the runtime. That is the kind you get in production.

### Deadlock 1 — inconsistent lock order (AB-BA)

```go
// ❌ transfer(a,b) and transfer(b,a) concurrently -> deadlock
func transfer(from, to *Account, n int) {
    from.mu.Lock(); defer from.mu.Unlock()
    to.mu.Lock();   defer to.mu.Unlock()
    ...
}
```

**Fix A — a total order.** Always lock the lower ID first, regardless of direction. No cycle can form.

```go
first, second := from, to
if first.id > second.id { first, second = second, first }
first.mu.Lock();  defer first.mu.Unlock()
second.mu.Lock(); defer second.mu.Unlock()
```

**Fix B — `TryLock` and back off.** For when a total order is impossible (locks discovered dynamically). Breaks hold-and-wait.

```go
for {
    from.mu.Lock()
    if to.mu.TryLock() {
        defer to.mu.Unlock()
        defer from.mu.Unlock()
        break
    }
    from.mu.Unlock()                    // release everything
    time.Sleep(randomBackoff())         // randomized, or you livelock
}
```

### Deadlock 2 — self-deadlock (non-reentrant mutexes)

Covered in [Synchronization](synchronization.md#non-reentrancy). Use the `xxxLocked` helper convention.

### Deadlock 3 — WaitGroup never reaches zero

Missing `Done()` on an early return. Always `defer wg.Done()` as the first statement.

### Deadlock 4 — channels

| Code | Result |
|------|--------|
| `ch <- v` on unbuffered with no receiver | Blocks forever |
| `<-ch` where nobody sends or closes | Blocks forever |
| `ch <- v` on a full buffer with no receiver | Blocks forever |
| Any operation on a `nil` channel | Blocks forever |

### Diagnosing a live deadlock

```bash
kill -QUIT <pid>                              # dump all goroutine stacks, exit
curl localhost:6060/debug/pprof/goroutine?debug=2
GOTRACEBACK=all ./app
```

Look for many goroutines parked in **`semacquire`** on the same lock, or in `chan send` / `chan receive`. The stack tells you which line.

[`16-deadlocks.go`](../examples/16-deadlocks.go) demonstrates every case above with a watchdog, so the program always terminates.

---

## 🐌 Livelock and starvation

**Livelock** — goroutines are actively running but make no progress. Classic cause: a `TryLock` retry loop where everyone retries in lockstep. **Fix:** randomized exponential backoff.

**Starvation** — a goroutine never gets the resource it needs.

Go's mutex mitigates this: since Go 1.9 `sync.Mutex` has a **starvation mode**. If a waiter has been queued for more than ~1 ms, the mutex switches from barging (fast, unfair) to strict FIFO handoff (slower, fair). You rarely have to think about it, but it explains why mutex throughput can drop under heavy contention.

`RWMutex` writer starvation is the one to watch: a continuous stream of readers can starve a writer. Go's implementation blocks *new* readers once a writer is waiting, which bounds this.

---

## 🧮 Unbounded concurrency

Not a bug in one goroutine — a bug in how many you make.

```go
// ❌ 100k rows = 100k goroutines = 100k concurrent DB queries
for _, row := range rows {
    go process(row)
}
```

This is a self-inflicted DoS on your own database. **Fix:** a worker pool or semaphore. See [Concurrency Patterns](concurrency-patterns.md).

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(10)
for _, row := range rows {
    g.Go(func() error { return process(ctx, row) })
}
err := g.Wait()
```

---

## ✅ Review checklist

Use this on any concurrent Go change:

- [ ] Every goroutine has a defined exit path — I can name what makes it return
- [ ] Every `WithCancel` / `WithTimeout` has `defer cancel()` on the next line
- [ ] Every blocking send/receive in a long-lived goroutine has a `ctx.Done()` case
- [ ] Result channels are buffered for **all** possible senders
- [ ] The **sender** closes channels; a `WaitGroup` + closer goroutine handles multiple senders
- [ ] `wg.Add` is before `go`; `defer wg.Done()` is the first statement inside
- [ ] Locks are always acquired in the same documented order
- [ ] No I/O, and no calls into unknown code, while holding a lock
- [ ] Check-and-act sequences are inside **one** critical section
- [ ] Concurrency is bounded — a pool or semaphore, not one goroutine per item
- [ ] Long-lived goroutines started by library code have their own `recover()`
- [ ] `go test -race` runs in CI
- [ ] `goleak` guards the test suite

---

## ▶️ Runnable examples

| Example | Covers |
|---------|--------|
| [`14-race-conditions.go`](../examples/14-race-conditions.go) | Lost updates, four fixes, TOCTOU that `-race` misses |
| [`15-goroutine-leaks.go`](../examples/15-goroutine-leaks.go) | Four leak patterns, each measured, each fixed |
| [`16-deadlocks.go`](../examples/16-deadlocks.go) | AB-BA, self-deadlock, WaitGroup, channels — all with watchdogs |

```bash
cd examples
go run 14-race-conditions.go        # see the wrong number
go run -race 14-race-conditions.go  # see exactly where
go run 15-goroutine-leaks.go
go run 16-deadlocks.go
```

---

## 🔗 Related

- [Concurrency](concurrency.md)
- [Synchronization](synchronization.md)
- [Concurrency Patterns](concurrency-patterns.md)
- [Concurrency Cheat Sheet](concurrency-cheatsheet.md)

## 📚 References

- [Go Blog — Introducing the Race Detector](https://go.dev/blog/race-detector)
- [Data Race Detector](https://go.dev/doc/articles/race_detector)
- [`go.uber.org/goleak`](https://pkg.go.dev/go.uber.org/goleak)
- [Diagnostics](https://go.dev/doc/diagnostics)
