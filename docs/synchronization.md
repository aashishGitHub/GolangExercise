# Synchronization in Go

## Overview

Channels move data. **Synchronization primitives protect data that stays where it is.**

Choosing wrongly is the most common Go concurrency design mistake:

- A mutex wrapped around a queue is usually asking to be a **channel**.
- A channel wrapped around a counter is usually asking to be an **atomic**.

Every primitive in `sync` has a **usable zero value**. `var mu sync.Mutex` is ready to go — no constructor, no initialization.

> ⚠️ **None of these may be copied after first use.** They contain internal state; copying a `sync.Mutex` silently creates a *second, independent* lock that protects nothing. Always pass structs containing them **by pointer**. `go vet` catches most cases via its `copylocks` check.

---

## 🔒 sync.Mutex

Mutual exclusion. One goroutine in the critical section at a time.

```go
type SafeCounter struct {
    mu     sync.Mutex     // guards counts
    counts map[string]int
}

func (c *SafeCounter) Inc(key string) {
    c.mu.Lock()
    defer c.mu.Unlock()   // survives early returns AND panics
    c.counts[key]++
}
```

### The idiom that matters

Put the mutex **immediately above the fields it guards**, keep it unexported, and say what it protects in a comment. A mutex floating at the top of a 20-field struct protects nothing in particular, and nobody can tell what the lock discipline is.

### Rules

| Rule | Why |
|------|-----|
| `defer mu.Unlock()` right after `Lock()` | Guarantees release on every path, including panics |
| **Not reentrant** | Locking a mutex you already hold deadlocks instantly |
| Keep critical sections short | Long ones serialize your whole program |
| **Never do I/O under a lock** | A slow network call blocks every other goroutine |
| Never call unknown code under a lock | A callback may re-enter and deadlock |

### Non-reentrancy

Unlike Java's `synchronized`, Go mutexes are **not** reentrant:

```go
// ❌ DEADLOCKS
func (r *Registry) Add(key string) int {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.items[key] = 1
    return r.Count()   // Count() locks r.mu again -> deadlock
}
```

The fix is the **`xxxLocked` convention**: a public method takes the lock, a private helper assumes the caller already holds it.

```go
// ✅ CORRECT
func (r *Registry) Add(key string) int {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.items[key] = 1
    return r.countLocked()
}

// countLocked requires r.mu to be held by the caller.
func (r *Registry) countLocked() int { return len(r.items) }
```

### TryLock (Go 1.18+)

```go
if mu.TryLock() {
    defer mu.Unlock()
    // got it
} else {
    // busy — do something else instead of blocking
}
```

Useful for breaking deadlocks (see [Goroutine Problems](goroutine-problems.md)) and for "skip this tick if the previous one is still running". Do not build normal locking on it — a `TryLock` retry loop can **livelock**, where everyone retries in lockstep and nobody progresses. Randomize your backoff.

---

## 📖 sync.RWMutex

Many concurrent readers, or one exclusive writer.

```go
func (s *Store) Get(k string) string {
    s.mu.RLock()          // concurrent with other readers
    defer s.mu.RUnlock()
    return s.values[k]
}

func (s *Store) Set(k, v string) {
    s.mu.Lock()           // exclusive: blocks readers and writers
    defer s.mu.Unlock()
    s.values[k] = v
}
```

### When it actually helps

Only when **reads genuinely dominate** (roughly 10:1 or better) **and** the critical section is long enough for the extra bookkeeping to pay off. `RLock` is more expensive than `Lock`; for very short sections a plain `Mutex` often wins. Measure before switching.

### Traps

| Trap | Detail |
|------|--------|
| **Not reentrant either** | `RLock` while holding `Lock` deadlocks |
| **Recursive `RLock` can deadlock** | If a writer queues between your two `RLock`s, the second one blocks behind it and you deadlock |
| **No upgrading** | You cannot promote `RLock` to `Lock`. Release, re-acquire, and **re-check your assumptions** — the state changed while you held nothing |

---

## ⏳ sync.WaitGroup

Wait for a set of goroutines to finish.

```go
var wg sync.WaitGroup
for _, item := range items {
    wg.Add(1)                  // BEFORE `go`
    go func(it Item) {
        defer wg.Done()        // FIRST statement in the goroutine
        process(it)
    }(item)
}
wg.Wait()
```

### Three ways to get it wrong

**1. `Add` inside the goroutine**

```go
go func() {
    wg.Add(1)     // ❌ races with Wait — Wait may return before this runs
    defer wg.Done()
}()
```

**2. Missing `Done` on an early return**

```go
go func() {
    if !valid { return }   // ❌ counter never reaches zero -> Wait blocks forever
    wg.Done()
}()
```

Always `defer wg.Done()` as the first line.

**3. `Add` concurrently with `Wait`**

An `Add` that raises the counter **from zero** must not run concurrently with `Wait`. If new work can arrive while you are draining, guard the "check whether we're still accepting, then `Add`" pair with a mutex so it is a single atomic step — see [`21-graceful-shutdown.go`](../examples/21-graceful-shutdown.go), which is exactly this situation. The race detector *does* catch this one.

> A `WaitGroup` may be reused, but only after the previous `Wait` has returned. Negative counters panic.
>
> Go 1.25 reportedly added a `WaitGroup.Go` helper that wraps `Add`/`Done` around a function. Verify availability in the current docs before relying on it.

---

## 1️⃣ sync.Once

Exactly-once initialization, safe under concurrency.

```go
var (
    once sync.Once
    conn *Connection
)

func Get() *Connection {
    once.Do(func() {
        conn = dial()
    })
    return conn
}
```

`Do` blocks every caller until the first invocation **returns**, so nobody can observe a half-built value.

> ⚠️ `Do` counts as "done" even if `f` **panics** — it will never run again. If initialization can fail and should be retried, `sync.Once` is the wrong tool.

**Go 1.21+ wrappers** remove the package-level variables:

```go
var getConfig = sync.OnceValue(func() Config { return loadConfig() })
var getPair   = sync.OnceValues(func() (Config, error) { return load() })
var setup     = sync.OnceFunc(func() { registerMetrics() })
```

---

## ⚛️ sync/atomic

Lock-free operations on a single word. The cheapest correct way to share a counter or a flag.

### Typed atomics (Go 1.19+) — prefer these

```go
var requests atomic.Int64
var degraded atomic.Bool
var config   atomic.Pointer[Config]

requests.Add(1)
n := requests.Load()
degraded.Store(true)
```

They are better than the older `atomic.AddInt64(&x, 1)` functions because the type makes non-atomic access to the field **impossible**, and they handle 64-bit alignment for you.

> The old function-based API has a real footgun: on 32-bit platforms, 64-bit atomics require the value to be 64-bit aligned, and a misaligned struct field panics at runtime. Typed atomics avoid this entirely.

### Compare-and-swap

`CompareAndSwap` is the building block of lock-free algorithms: *set to new **only if** it is still old*.

```go
for {
    old := counter.Load()
    if counter.CompareAndSwap(old, old+1) {
        break          // won the race
    }
    // someone else changed it — reload and retry
}
```

### The limit

**Atomics protect exactly one word.** Two related fields still need a mutex:

```go
// ❌ BROKEN — a reader can see the new width with the old height
var width, height atomic.Int64

// ✅ Either take a lock, or swap an immutable snapshot in one operation
var dims atomic.Pointer[Dimensions]
```

`atomic.Pointer[T]` swapping an immutable struct is a very effective pattern for hot-path config reloads: readers never block, and they always see a fully consistent snapshot.

---

## 🗺️ sync.Map

A concurrent map — but **not** a general-purpose replacement for `map` + `RWMutex`.

Use it only for its two documented workloads:

1. **Write-once, read-many** — a key is written once and then read repeatedly.
2. **Disjoint key sets** — goroutines mostly touch keys that other goroutines never touch.

For anything else, `map` + `RWMutex` is usually **faster** and is always more type-safe (`sync.Map` is untyped: keys and values are `any`, so you pay for boxing and lose compile-time checks).

```go
var m sync.Map

m.Store("k", 42)
v, ok := m.Load("k")

// LoadOrStore is atomic: exactly one caller sees loaded == false
actual, loaded := m.LoadOrStore("k", 99)

m.Range(func(k, v any) bool {
    return true    // return false to stop early
})
```

> `Range` does **not** take a consistent snapshot — entries added or removed during iteration may or may not be visited.

---

## 🔔 sync.Cond

Wait for a **condition** to become true, without busy-polling.

```go
cond := sync.NewCond(&mu)

// Consumer
mu.Lock()
for len(queue) == 0 {   // ALWAYS a for loop, never an if
    cond.Wait()         // atomically unlocks mu, sleeps, re-locks on wake
}
item := queue[0]
queue = queue[1:]
mu.Unlock()

// Producer
mu.Lock()
queue = append(queue, item)
mu.Unlock()
cond.Signal()    // wake one waiter; Broadcast() wakes all
```

**Why `for` and not `if`:** `Wait` can return without a matching `Signal` (spurious wakeup), and even with a real signal another consumer may have taken the item before you re-acquired the lock. Re-check the predicate every time.

In most Go code a **buffered channel replaces `Cond` entirely**. Reach for `Cond` only when waiters need a predicate a channel cannot express.

---

## ♻️ sync.Pool

Reuse allocations to reduce GC pressure.

```go
var bufPool = sync.Pool{
    New: func() any { return new(bytes.Buffer) },
}

b := bufPool.Get().(*bytes.Buffer)
b.Reset()              // CRITICAL: it carries the previous user's data
defer bufPool.Put(b)
```

| Rule | Why |
|------|-----|
| **Always reset on Get** | Pooled objects are dirty by definition — this is a real source of data leaks between requests |
| **Never a cache** | The GC may drop pooled items at any time; there are no retention semantics |
| **Measure first** | It is a throughput optimization; it complicates code and often changes nothing |
| Do not pool large or variable-size objects | You can pin the largest buffer you ever saw, forever |

---

## 🧭 Decision table

| You need to... | Use |
|----------------|-----|
| Pass ownership of data between goroutines | **channel** |
| Guard several related fields | **`sync.Mutex`** beside them |
| Read-mostly shared state, long critical sections | **`sync.RWMutex`** |
| One counter or flag | **`sync/atomic`** |
| Swap a whole config/snapshot atomically | **`atomic.Pointer[T]`** |
| Initialize exactly once | **`sync.Once`** / `OnceValue` |
| Wait for N goroutines | **`sync.WaitGroup`** |
| Wait for N goroutines *and collect the first error* | **`errgroup.Group`** |
| Cap concurrency at N | **buffered channel** or `errgroup.SetLimit(n)` |
| Wait for an arbitrary predicate | **`sync.Cond`** (rare) |
| Reduce allocation churn on a hot path | **`sync.Pool`** (measure first) |

---

## 📦 golang.org/x/sync

Not in the standard library, but effectively standard in production Go. All are real, actively maintained packages — verify current APIs in their docs.

| Package | Purpose |
|---------|---------|
| `errgroup` | WaitGroup + first-error propagation + context cancellation + `SetLimit` |
| `semaphore` | Weighted semaphore (permits of differing cost) |
| `singleflight` | Collapse duplicate concurrent calls into one |

`errgroup` is the one you will reach for constantly:

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(10)                        // bounded concurrency

for _, url := range urls {
    g.Go(func() error {
        return fetch(ctx, url)        // first error cancels ctx for everyone
    })
}
if err := g.Wait(); err != nil { ... }
```

---

## ▶️ Runnable example

[`13-sync-primitives.go`](../examples/13-sync-primitives.go) demonstrates all seven primitives with measured output.

```bash
cd examples
go run 13-sync-primitives.go
```

---

## 🔗 Related

- [Concurrency](concurrency.md) — goroutines, channels, select, the scheduler
- [Goroutine Problems](goroutine-problems.md) — what goes wrong and how to find it
- [Concurrency Patterns](concurrency-patterns.md) — composing these into real systems
- [Concurrency Cheat Sheet](concurrency-cheatsheet.md)

## 📚 References

- [`sync` package](https://pkg.go.dev/sync)
- [`sync/atomic` package](https://pkg.go.dev/sync/atomic)
- [`golang.org/x/sync/errgroup`](https://pkg.go.dev/golang.org/x/sync/errgroup)
- [The Go Memory Model](https://go.dev/ref/mem)
