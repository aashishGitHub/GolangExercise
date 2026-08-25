# Concurrency in Go

## Overview

**Concurrency is about structure. Parallelism is about execution.**

> "Concurrency is not parallelism." — Rob Pike

- **Concurrency** — designing a program as independent tasks that *could* run in any order. This is a property of your **code**.
- **Parallelism** — actually running things at the same instant on multiple cores. This is a property of the **hardware**.

A concurrent Go program is still correct on a single core; it just will not be faster. This distinction matters because most Go concurrency bugs come from writing parallel-looking code that has no concurrency *structure* — no defined ownership, no defined lifetime.

Go gives you three building blocks:

| Primitive | Purpose |
|-----------|---------|
| **goroutine** | An independently executing function |
| **channel** | A typed conduit that also synchronizes |
| **select** | Wait on several channel operations at once |

---

## 🧵 Goroutines

A goroutine is a function scheduled by the **Go runtime**, not by the OS kernel.

```go
go doWork()           // starts a goroutine, returns immediately
go func() { ... }()   // anonymous variant
```

### Why they are cheap

| | OS thread | Goroutine |
|---|---|---|
| Initial stack | ~1–8 MB (fixed) | ~2 KB (grows on demand) |
| Created by | Kernel (syscall) | Go runtime (user space) |
| Context switch | Kernel trap, ~1–2 µs | Runtime, a few hundred ns |
| Practical ceiling | Thousands | Hundreds of thousands |

Stack sizes and switch costs are approximate and vary by platform and Go version — treat them as orders of magnitude, not measurements.

The stack starts small and **grows by copying** when it runs out. That is why a goroutine is cheap to create but why deep recursion in many goroutines can still surprise you.

### Two rules that catch most beginners

**1. Arguments are evaluated at `go` time, not at run time.**

```go
x := 1
go fmt.Println(x)  // captures 1 right now
x = 2              // does not change what gets printed
```

**2. When `main` returns, the process exits — every other goroutine is killed mid-flight.**

```go
func main() {
    go fmt.Println("may never print")
    // no synchronization -> the process may exit first
}
```

`time.Sleep` is **not** synchronization. Use `sync.WaitGroup` or a channel.

### Loop variables (changed in Go 1.22)

```go
for i := 0; i < 3; i++ {
    go func() { fmt.Println(i) }()
}
```

| Go version | Behaviour |
|------------|-----------|
| ≤ 1.21 | One shared `i`. Typically prints `3 3 3`. |
| ≥ 1.22 | A new `i` per iteration. Prints `0 1 2` in some order. |

Which one you get is decided by the **`go` directive in `go.mod`**, not by your installed toolchain. Passing the value as an argument (`go func(n int){...}(i)`) is correct on every version and is still the clearest thing to write.

### Panics are not contained

An unrecovered panic in **any** goroutine terminates the **whole process**. `recover()` only works in the goroutine that panicked — a parent cannot catch a child's panic.

```go
go func() {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("recovered: %v", r)
        }
    }()
    riskyWork()
}()
```

Any long-lived goroutine you spawn from library code needs its own recover.

---

## 📡 Channels

A channel is a typed, thread-safe queue that also carries a **happens-before** guarantee: everything the sender did before the send is visible to the receiver after the receive. That is what lets channels replace locks — the data *moves*, so only one goroutine touches it at a time.

```go
ch := make(chan int)      // unbuffered
ch := make(chan int, 10)  // buffered, capacity 10
```

### Unbuffered vs buffered

| | Unbuffered | Buffered |
|---|---|---|
| Send blocks until | A receiver is ready | The buffer has room |
| Semantics | Rendezvous / handoff | Queue |
| Use for | Synchronization, guaranteed delivery | Decoupling speeds, absorbing bursts |

An unbuffered channel gives you **backpressure for free**: a slow consumer automatically throttles the producer. A large buffer removes that safety and just delays the moment you run out of memory. **Buffer size is a tuning knob, never a correctness fix.**

### The six axioms

Memorize these — most channel bugs are a violation of one:

| Operation | Result |
|-----------|--------|
| Send on `nil` channel | **Blocks forever** |
| Receive from `nil` channel | **Blocks forever** |
| Send on closed channel | **panic** |
| Receive from closed channel | Zero value immediately, `ok == false` |
| Close a closed channel | **panic** |
| Close a `nil` channel | **panic** |

### Closing

```go
v, ok := <-ch   // ok == false means the channel is closed and drained
for v := range ch { ... }  // ends when the channel is closed
```

**The sender closes. Never the receiver.** A receiver that closes will make the sender panic. If there are multiple senders, none of them can close safely — coordinate with a `sync.WaitGroup` and a dedicated closer goroutine:

```go
go func() {
    wg.Wait()      // every sender has finished
    close(results) // now it is safe
}()
```

Closing is a **broadcast**: one `close()` wakes every waiting receiver at once. That makes `chan struct{}` the idiomatic "signal" type — it carries no data and allocates nothing.

### Direction types

```go
func produce(out chan<- int)  // send-only: compiler rejects receives
func consume(in <-chan int)   // receive-only: compiler rejects sends and close
```

Put directions in your signatures. It documents ownership and lets the compiler enforce it.

---

## 🔀 select

```go
select {
case v := <-ch1:
    // ...
case ch2 <- x:
    // ...
case <-ctx.Done():
    return ctx.Err()
default:
    // runs only if NO other case is ready
}
```

- Blocks until **one** case is ready.
- If several are ready, it picks **uniformly at random** — this prevents one busy channel from starving the others.
- `default` makes the whole select **non-blocking**.

### Three idioms worth knowing

**Timeout**

```go
select {
case res := <-work:
    return res, nil
case <-time.After(2 * time.Second):
    return nil, errors.New("timeout")
}
```

In a hot loop prefer `time.NewTimer` + `defer timer.Stop()`. On Go versions before 1.23, a `time.After` timer stays alive until it fires, so a tight loop accumulates timers. Go 1.23 made unreferenced timers collectable — if this matters to your code path, verify the behaviour for the Go version you actually ship.

**Non-blocking send (load shedding)**

```go
select {
case queue <- job:
    // accepted
default:
    // queue full: drop it, or return 503 — do not block the producer
}
```

**`nil` channels as an off switch**

Because a `nil` channel blocks forever, setting a channel variable to `nil` **permanently disables** its select case. This is the standard way to merge streams that close at different times:

```go
for a != nil || b != nil {
    select {
    case v, ok := <-a:
        if !ok { a = nil; continue }  // disable this case
        use(v)
    case v, ok := <-b:
        if !ok { b = nil; continue }
        use(v)
    }
}
```

Without setting them to `nil`, a closed channel is *always* ready and the loop spins at 100% CPU.

---

## ⚙️ The scheduler (G-M-P)

Understanding the model explains most performance behaviour you will observe.

| Letter | Is | Count |
|--------|-----|-------|
| **G** | Goroutine | Thousands |
| **M** | Machine — an OS thread | Grows as needed |
| **P** | Processor — a scheduling context with a local run queue | `GOMAXPROCS` |

An **M** must hold a **P** to run Go code. `GOMAXPROCS` (default: number of logical CPUs) therefore caps how many goroutines run Go code **in parallel**.

Key behaviours:

- **Work stealing** — an idle P steals from another P's run queue, so load balances itself.
- **Blocking syscalls** — the M blocks, hands its P to another M, and the other goroutines keep running. This is why blocking I/O does not stall your program.
- **Network I/O** — handled by the runtime's netpoller. A goroutine blocked on a socket is parked, not holding a thread. This is why "one goroutine per connection" scales in Go and does not in thread-per-connection languages.
- **Asynchronous preemption** (Go 1.14+) — the runtime can interrupt a goroutine that never yields. Before that, a tight CPU loop with no function calls could hog a P indefinitely.

> ⚠️ **Container gotcha:** `GOMAXPROCS` defaults to the number of CPUs the *machine* reports, not your cgroup CPU limit. In Kubernetes a 1-core-limited pod on a 64-core node gets `GOMAXPROCS=64`, causing heavy throttling and latency spikes. Set it explicitly or use a library such as `go.uber.org/automaxprocs`. Go 1.25 reportedly made the runtime cgroup-aware — verify against the release notes for the version you deploy.

---

## 🧠 The Go memory model

The memory model defines when a write in one goroutine is **guaranteed visible** to a read in another. Without a happens-before relationship, the compiler and CPU may reorder, cache, or optimize away your access — so a data race is *undefined behaviour*, not "you get one of the two values".

The guarantees you actually use:

| Action | Guarantee |
|--------|-----------|
| `go f()` | Everything before the `go` statement happens-before `f` starts |
| Channel send | Happens-before the corresponding receive **completes** |
| Channel close | Happens-before a receive that returns the zero value |
| Unbuffered receive | Happens-before the corresponding send **completes** |
| `mu.Unlock()` | Happens-before a subsequent `mu.Lock()` returns |
| `once.Do(f)` | `f` returning happens-before any `Do` call returns |
| `wg.Wait()` returning | Everything before the matching `Done()` calls happens-before it |
| `sync/atomic` ops | Sequentially consistent (formalized in Go 1.19) |

**Practical rule:** if two goroutines touch the same variable and at least one writes, you need a channel, a mutex, or an atomic. There is no "it's just an int, it'll be fine".

---

## 🚦 Choosing your tool

| Situation | Use |
|-----------|-----|
| Passing ownership of data between goroutines | **Channel** |
| Guarding fields of a struct in place | **`sync.Mutex`** next to the fields |
| Read-mostly shared state | **`sync.RWMutex`** |
| A single counter or flag | **`sync/atomic`** |
| Wait for N goroutines | **`sync.WaitGroup`** |
| Initialize exactly once | **`sync.Once`** |
| Cancellation and deadlines | **`context.Context`** |

> "Don't communicate by sharing memory; share memory by communicating."

But do not over-apply it. A mutex around a shared counter is simpler and faster than a channel-based "counter server". Use channels for **handoff**, locks for **in-place state**.

---

## ▶️ Runnable examples

| Example | Covers |
|---------|--------|
| [`11-goroutines-basics.go`](../examples/11-goroutines-basics.go) | Lifecycle, WaitGroup, loop variables, panics |
| [`12-channels-select.go`](../examples/12-channels-select.go) | Buffering, closing, select, nil channels |
| [`13-sync-primitives.go`](../examples/13-sync-primitives.go) | Mutex, RWMutex, Once, atomic, Map, Cond, Pool |

```bash
cd examples
go run 11-goroutines-basics.go
```

---

## 🔗 Related

- [Synchronization](synchronization.md) — the `sync` and `sync/atomic` packages in depth
- [Goroutine Problems](goroutine-problems.md) — leaks, races, deadlocks and how to find them
- [Concurrency Patterns](concurrency-patterns.md) — worker pools, pipelines, distributed-systems patterns
- [Concurrency Cheat Sheet](concurrency-cheatsheet.md) — quick reference

## 📚 References

- [The Go Memory Model](https://go.dev/ref/mem)
- [Effective Go — Concurrency](https://go.dev/doc/effective_go#concurrency)
- [Go Blog — Share Memory By Communicating](https://go.dev/blog/codelab-share)
- [Go Blog — Go Concurrency Patterns: Pipelines](https://go.dev/blog/pipelines)
- [Rob Pike — Concurrency Is Not Parallelism](https://go.dev/blog/waza-talk)
