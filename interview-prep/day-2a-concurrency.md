# Day 2a — Concurrency for a first-time Go reader

**Audience:** You can build software in another language, but goroutines, channels, and Go's memory model are new. This is the on-ramp to [`day-2-concurrency.md`](day-2-concurrency.md), not a replacement for it.

**Read time:** ~55 minutes total, about 2–3 minutes per lesson. Split it into two or three sittings if that is what fits.

**In scope:** The same Day 2 concepts, in the same order: goroutines, G-M-P, `GOMAXPROCS`, channels, close ownership, `select`, context, mutexes, the memory model, worker pools, fan-in, retries, graceful shutdown, SSE, leaks, and the ControlPlane tie-in.

**Out of scope:** The HTTP API and Postgres material (Day 3), and deep messaging/outbox mechanics (Day 4). The SSE and graceful-shutdown lessons touch HTTP only where Day 2 needs it. This file also skips deep runtime and scheduler internals.

**Source:** [`day-2-concurrency.md`](day-2-concurrency.md).

**Lesson IDs:** L1–L21, in source order. The source's grill keeps its Q1–Q20 labels; the map below links each grill question to the lesson that prepares it.

**Flow:** Each lesson is one short explanation: what is happening, one concrete example, why it matters, what to focus on and ignore, three Q&As, and one line to remember. Code blocks are short sketches, not complete programs.

> **Schedule note:** After the Day 1a remediation, this is the Sun 15 Nov Day 2 core. If the day is short, do the 20-minute minimum pass below; do not double up on Day 3. The 19/20 Nov interview dates do not move.

---

## How to use this file

Read one lesson at a time. After each lesson, close the file and say the **Remember** line aloud. If you can say it in your own words, open the matching section of Day 2 and read the denser version. If you cannot, reread the example and move on; do not spend more than 10 minutes on one lesson.

Do not read this file and Day 2 cover to cover in the same sitting. This file gives you the shape; Day 2 gives you the interview-grade detail. Use the Day 2 drills after L14, L16, and L20.

**Suggested 60-minute pass:** 5 minutes on the map and the five things below, 50 minutes on the lessons, 5 minutes on the recall sheet. The second hour is the worker-pool drill and the grill.

**Minimum pass (20 minutes):** L2, L4, L5, L8, L11, L13, and L14. These are the five things plus the channel and scheduler basics they depend on.

**If you only remember five things:** bound concurrency, propagate context, the sender closes, `Add` before `go`, and `-race` is evidence, not proof.

---

## Question map

| ID | Lesson | Day 2 section | Grill |
|---|---|---|---|
| L1 | Goroutines vs OS threads | §1 Scheduler | Q1 |
| L2 | G-M-P | §1 Scheduler | Q2 |
| L3 | `GOMAXPROCS` and lifetime | §1 Scheduler | Q2, Q19 |
| L4 | Buffered vs unbuffered channels | §2 Channels | Q3 |
| L5 | Channel axioms and close | §2 Channels | Q4, Q5 |
| L6 | Directional channels and load shedding | §2 Channels | Q3 |
| L7 | `select` | §3 `select` | Q5, Q6, Q15 |
| L8 | Context basics | §4 `context` | Q7 |
| L9 | Context values and the server rule | §4 `context` | Q7 |
| L10 | Mutex vs channel | §5 `sync` | Q8 |
| L11 | `sync` rules | §5 `sync` | Q11 |
| L12 | Data race vs race condition | §6 Memory model | Q9, Q10 |
| L13 | `-race` is evidence | §6 Memory model | Q10 |
| L14 | Bounded worker pool | §7 Patterns | Q12, Q13 |
| L15 | `SetLimit` and semaphores | §7 Patterns | Q13 |
| L16 | Fan-out, fan-in, pipelines | §7 Patterns | Q19 |
| L17 | Retry with backoff and jitter | §7 Patterns | Q19 |
| L18 | Graceful shutdown | §7 Patterns | Q19 |
| L19 | SSE and client disconnect | §7 Patterns | Q19 |
| L20 | Leaks, deadlocks, panics | §8 Failure modes | Q14, Q15, Q16 |
| L21 | ControlPlane tie-in | §11 Tie-in | Q17, Q18, Q20 |

**The one idea behind Day 2.** Concurrency is not a correctness mechanism. Goroutines let I/O overlap and let cancellation travel, but they do not make a shared invariant safe. Every goroutine needs a stop path, every shared state needs a synchronization edge, and across processes the database — not a Go mutex — is the final arbiter.

---

## L1. Goroutines are cheap threads managed by the runtime

A **goroutine** is a function running concurrently with the rest of the program. You start one with `go f()`. The Go runtime schedules goroutines onto operating-system threads, so you write concurrent code without managing threads yourself. A goroutine starts with a small stack (about 2 KB) that can grow; an OS thread has a fixed, larger stack and is scheduled by the kernel.

```go
go func(msg string) {
    fmt.Println(msg)
}("hello")
```

Creating a goroutine is cheap, but it is not free: each one has a stack and scheduling cost. More importantly, `main` returning kills every goroutine. There is no parent-child relationship and no automatic wait. If you need to wait, use a `WaitGroup` or an `errgroup`.

**Why it matters.** Goroutines make it easy to launch work, which is exactly why unbounded launches become outages. The language gives you concurrency; it does not give you a bound, a lifetime, or a stop path.

**Focus on:** `go f()`; runtime-scheduled; growable stack; `main` kills all goroutines.

**Ignore for now:** stack-growth mechanics, the M:N scheduler internals, and thread-pool tuning.

**Q: Is a goroutine an OS thread?** No; it is a runtime-scheduled unit multiplexed onto OS threads.

**Q: Does `main` wait for running goroutines?** No; returning from `main` exits the process.

**Q: Is creating a goroutine free?** No; it has stack and scheduling cost, and unbounded creation is a common outage.

**Remember:** goroutines are cheap threads managed by the Go runtime, not the OS.

---

## L2. G-M-P — the scheduler in one picture

Go's scheduler has three pieces. A **G** is a goroutine. An **M** is an OS thread. A **P** is a logical processor with a local run queue. An M must hold a P to run Go code, and `GOMAXPROCS` sets how many Ps exist.

| Piece | Meaning |
|---|---|
| **G** | Goroutine: a small stack and instruction pointer |
| **M** | Machine: an OS thread |
| **P** | Processor: a scheduling context with a run queue |

When a goroutine blocks in a system call, the M can hand its P to another M so other goroutines keep running. When a P runs out of work, it steals from another P's queue. Since Go 1.14, long-running goroutines can be preempted asynchronously.

**Why it matters.** G-M-P explains why thousands of goroutines can run on a handful of threads, why a blocking syscall does not freeze the whole program, and what `GOMAXPROCS` actually controls. It is also a standard interview question.

**Focus on:** G, M, P; P's run queue; work stealing; `GOMAXPROCS` sets the number of Ps.

**Ignore for now:** the work-stealing algorithm, `sysmon`, and preemption internals.

**Q: What does P do?** It holds a run queue and is required for an M to execute Go code.

**Q: What happens when a goroutine blocks in a syscall?** The M can hand off its P so another thread runs Go code.

**Q: What does `GOMAXPROCS` control?** The number of Ps, not the number of goroutines.

**Remember:** G runs on M, M needs P, and `GOMAXPROCS` sets the number of Ps.

---

## L3. `GOMAXPROCS` in containers, and the lifetime of a goroutine

`GOMAXPROCS` sets the number of Ps the scheduler can use. Before Go 1.25, it defaulted to the number of logical CPUs visible to the process, not the container's CPU limit. A container limited to 2 CPUs on a 16-CPU host could still start with 16 Ps, causing throttling and latency spikes.

Go 1.25 changed the default on Linux: the runtime reads the cgroup CPU bandwidth limit and uses the lower value, then updates `GOMAXPROCS` when the limit changes. Setting `GOMAXPROCS` manually disables that automatic behavior. On older Go versions, or when you override it, set it from the cgroup limit or use `automaxprocs`.

```go
fmt.Println(runtime.GOMAXPROCS(0))   // current P count
fmt.Println(runtime.NumGoroutine())  // current goroutine count
```

`runtime.NumGoroutine()` is a cheap leak signal, but it is a snapshot, not proof. And `main` returning kills every goroutine; there is no parent-child wait.

**Why it matters.** A service that looks fine on a laptop can throttle in Kubernetes if the scheduler uses more Ps than the CPU quota. The same service can leak goroutines if `main` exits or a blocked goroutine never gets a stop path.

**Focus on:** the Go 1.25 cgroup-aware default; manual override; `NumGoroutine`; `main` kills goroutines.

**Ignore for now:** cgroup v1/v2 details, `GODEBUG` settings, and `automaxprocs` internals.

**Q: Does `GOMAXPROCS` read the container limit?** On Linux with Go 1.25 or newer, yes by default; older versions do not, and a manual setting disables it.

**Q: What happens if `main` returns?** The process exits and all goroutines die.

**Q: How do you get a cheap leak signal?** `runtime.NumGoroutine()`.

**Remember:** Go 1.25 reads the cgroup limit on Linux; older versions need help, and `main` never waits.

---

## L4. Channels are typed pipes: unbuffered is a handshake, buffered is a waiting room

A channel is a typed pipe that lets goroutines send and receive values. An **unbuffered** channel is a rendezvous: a send blocks until a receiver is ready, and a receive blocks until a sender is ready. A **buffered** channel decouples the two speeds up to its capacity; a send blocks only when the buffer is full, and a receive blocks only when it is empty.

```go
ch := make(chan int, 2)
ch <- 1
ch <- 2
// ch <- 3 blocks until a receiver drains a slot
```

A buffer is not an unbounded queue. It delays blocking; it does not remove the need for a receiver or a stop path. If nobody ever receives, a buffered channel still leaks the sender once the buffer fills.

**Why it matters.** Unbuffered channels give natural backpressure and synchronization. Buffered channels improve throughput when producer and consumer speeds differ, but they can hide a slow consumer until the buffer fills. The buffer size is a design decision, not a fix for a missing consumer.

**Focus on:** rendezvous vs buffer; send/receive blocking; a buffer is bounded.

**Ignore for now:** channel internals, `sudog`, and scheduler wakeup mechanics.

**Q: When does an unbuffered send complete?** When a receiver is ready.

**Q: Does a buffer make a channel unbounded?** No; a send blocks when the buffer is full.

**Q: Does a buffer fix a missing receiver?** No; it only delays the block.

**Remember:** unbuffered is a handshake; buffered is a bounded waiting room.

---

## L5. Channel axioms and close ownership

Five rules cover most channel bugs. Send or receive on a `nil` channel blocks forever. Send on a closed channel panics. Receive on a closed channel returns the zero value with `ok == false` after the buffered values are drained. Closing a closed or nil channel panics. `for v := range ch` ends when the channel is closed and drained.

```go
ch := make(chan int)
go func() {
    defer close(ch)
    ch <- 1
}()
for v := range ch {
    fmt.Println(v)
}
```

The **sender** closes. A receiver closing is a bug because another sender may still send and panic. With multiple senders, use a `WaitGroup` and one closer goroutine. Close means "no more values", not "cancel"; use `context` for cancellation. Directional types, `chan<- int` and `<-chan int`, document intent and prevent accidental sends or receives.

**Why it matters.** Closing the wrong side or sending after close causes panics that are hard to reproduce. `range` only ends when the sender closes, so a missing close becomes a blocked receiver and a leaked goroutine.

**Focus on:** the sender closes; close means end-of-stream; nil blocks; send on closed panics; receive on closed returns zero/false.

**Ignore for now:** close synchronization internals and channel-direction conversion details.

**Q: Who closes a channel?** The sender, or one closer after all senders finish.

**Q: What does `v, ok := <-ch` return after close?** The zero value with `ok == false`, after buffered values are drained.

**Q: Can you close a channel to cancel a receiver?** No; close signals end-of-stream, and other senders may panic. Use context for cancellation.

**Remember:** the sender closes; close means no more values, not cancellation.

---

## L6. Directional channels and load shedding

A function signature can restrict a channel's direction: `chan<- int` is send-only and `<-chan int` is receive-only. This is documentation the compiler enforces. It makes ownership obvious and prevents a function from doing something it should not.

For overload, a non-blocking send with `select` and `default` is often better than an unbounded queue:

```go
select {
case queue <- job:
    // accepted
default:
    // full: return 503 or drop with a metric
}
```

**Why it matters.** An unbounded queue hides overload: memory and latency grow until the process fails. A bounded queue with an explicit rejection path protects the service and gives you a metric. Directional channels make it harder to close or send from the wrong side.

**Focus on:** `chan<-` and `<-chan`; non-blocking send; bounded queue plus 503 or drop.

**Ignore for now:** rate-limiter algorithms, queueing theory, and broker prefetch tuning.

**Q: What does `chan<- int` mean?** A send-only channel.

**Q: What does `default` do in a select?** It makes the select non-blocking and runs when no case is ready.

**Q: Why not use an unbounded queue?** It hides overload and grows memory and latency until failure.

**Remember:** direction documents ownership; a full queue should reject, not grow forever.

---

## L7. `select` is the control plane of a goroutine

`select` waits on several channel operations at once. If several cases are ready, Go chooses one uniformly at random; there is no priority. A `default` case makes the select non-blocking. A `nil` channel disables its case forever, which is useful when merging a changing set of inputs.

```go
select {
case v := <-ch:
    fmt.Println(v)
case <-ctx.Done():
    return ctx.Err()
case <-time.After(time.Second):
    return errors.New("timeout")
}
```

`time.After` in a loop creates a new timer every iteration, and the old timer can live until it fires. Use `time.NewTimer` or `time.Ticker` and stop it. A select with no ready case and no default blocks; if every goroutine blocks, the runtime reports a total deadlock, but partial deadlocks are silent.

**Why it matters.** `select` is how one goroutine waits on input, cancellation, and time without spawning a goroutine per concern. The random choice is deliberate: it prevents starvation and accidental priority. Timer leaks and nil-channel mistakes are common production bugs.

**Focus on:** random ready case; `default` non-blocking; `nil` disables; context/timeout; timer cleanup.

**Ignore for now:** the runtime select implementation, timer wheels, and the deadlock detector internals.

**Q: If two cases are ready, which runs?** One at random; there is no priority.

**Q: What does a nil channel do in a select?** It disables that case forever.

**Q: Why is `time.After` in a loop a problem?** It creates a timer each iteration; use a ticker or `NewTimer` and stop it.

**Remember:** `select` waits on whatever can proceed; randomness is deliberate.

---

## L8. `context` carries cancellation and deadlines

`context.Context` carries cancellation, a deadline, and request-scoped values across API boundaries. The usual pattern is a timeout derived from the request:

```go
ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
defer cancel()
```

Pass `ctx` as the first parameter, named `ctx`. Do not store it in a struct. Always `defer cancel()` for `WithCancel`, `WithTimeout`, and `WithDeadline`; it releases the timer and the parent's reference. Deadlines only shrink as they go down the call chain. Check `ctx.Done()` in loops and before expensive work, and pass the context to database, broker, and HTTP calls.

**Why it matters.** Cancellation is how a request stops work when the client disconnects or a deadline expires. Without propagation, goroutines keep doing work nobody is waiting for, and they leak. `ctx.Err()` tells you whether the cause was `context.Canceled` or `context.DeadlineExceeded`.

**Focus on:** first parameter; `defer cancel()`; deadlines shrink; `ctx.Done()`; `ctx.Err()`.

**Ignore for now:** context internals, value lookup cost, and `WithCancelCause`/`WithoutCancel`/`AfterFunc`.

**Q: Why `defer cancel()`?** It releases the timer and the parent's reference to the child context.

**Q: What does `ctx.Done()` return?** A channel that closes when the context is canceled or its deadline passes.

**Q: How do you detect cancellation?** `errors.Is(err, context.Canceled)` or `context.DeadlineExceeded`.

**Remember:** pass context down, cancel it on the way out, and check `Done` in loops.

---

## L9. Context values and the server rule

`context.WithValue` is for request metadata such as a request ID or an authenticated subject, not for dependencies or optional parameters. Use an unexported key type to avoid collisions. `context.WithCancelCause` attaches a cause, `context.Cause(ctx)` reads it, and `context.WithoutCancel` keeps values but drops cancellation for best-effort work.

```go
type ctxKey struct{}
ctx = context.WithValue(ctx, ctxKey{}, requestID)
id, _ := ctx.Value(ctxKey{}).(string)
```

The server rule matters more than the extras: `r.Context()` is canceled when the client disconnects. If background work must outlive the request, detach it with a new context and a bounded lifetime; do not silently reuse the request context.

**Why it matters.** Context values are easy to abuse as a hidden dependency bag, which makes code hard to test and reason about. Detaching background work incorrectly can cancel it immediately or let it run forever. Both are production bugs.

**Focus on:** values for request metadata only; unexported key; `r.Context()` canceled on disconnect; detach with a bounded lifetime.

**Ignore for now:** custom context implementations, value lookup performance, and `AfterFunc` internals.

**Q: What should go in `context.Value`?** Request-scoped metadata, not dependencies.

**Q: Why an unexported key type?** To avoid collisions and accidental access.

**Q: What happens to `r.Context()` when the client disconnects?** It is canceled.

**Remember:** context carries request scope, not your application's dependencies.

---

## L10. Mutex or channel? Use the shape of the problem

Both synchronize, but they fit different shapes. A mutex guards fields in place. A channel passes ownership of data or signals completion. `RWMutex` helps read-mostly state; `atomic` handles one counter or flag; `sync.Once` initializes exactly once; `WaitGroup` waits for N goroutines; `errgroup` waits and returns the first error.

```go
type Counter struct {
    mu sync.Mutex
    n  int
}

func (c *Counter) Inc() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.n++
}
```

"Channels for handoff, locks for in-place state." Do not build a counter server out of channels: it adds a goroutine and a stop problem for no benefit.

**Why it matters.** Choosing the wrong primitive makes code more complex or slower. A channel around a counter is harder to stop and test than a mutex. A mutex around ownership transfer is awkward and easy to get wrong.

**Focus on:** mutex for state; channel for handoff or signal; the need-to-primitive table.

**Ignore for now:** lock fairness, `RWMutex` internals, and channel implementation details.

**Q: When use a mutex?** When guarding fields that are read and written in place.

**Q: When use a channel?** When passing ownership of data or signaling completion.

**Q: Is a channel always faster?** No; it has synchronization and scheduling cost. Use the simplest correct primitive.

**Remember:** channels hand off; mutexes protect state in place.

---

## L11. The `sync` rules that show up in interviews

`WaitGroup.Add` must happen before the `go` statement, not inside the goroutine; otherwise `Wait` can race with `Add` and return early. Never copy a type containing a mutex or `WaitGroup` after first use; pass a pointer. A mutex is not reentrant: calling `Lock` twice in the same goroutine deadlocks.

```go
var wg sync.WaitGroup
for i := 0; i < 3; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        work()
    }()
}
wg.Wait()
```

`RWMutex` is not automatically faster; it helps read-heavy workloads, but it adds overhead, and a waiting writer blocks new readers, so measure it. `sync.Once` blocks all callers until the function returns, and a panic counts as done, so later calls do not retry. `sync.Pool` is for temporary objects, not a guaranteed cache; items can be dropped at GC. Atomics protect one word; related fields still need a mutex or an immutable snapshot swapped with `atomic.Pointer`.

**Why it matters.** These are the bugs that pass a single test and fail under load. Add-before-go and no-copy-after-use are interview staples, and `sync.Once` panic behavior surprises people in production.

**Focus on:** `Add` before `go`; no copy after use; non-reentrant; `Once`/`Pool`/atomic caveats.

**Ignore for now:** lock implementation, starvation policies, and atomic memory-ordering details.

**Q: Why `Add` before `go`?** So `Wait` cannot return before the counter is incremented.

**Q: Can you copy a struct with a mutex?** Not after first use; pass a pointer.

**Q: Does `sync.Once` retry after a panic?** No; it is considered done.

**Remember:** add before go, don't copy locks, and don't treat `Pool` as a cache.

---

## L12. Data race vs race condition

A **data race** is two goroutines accessing the same memory concurrently, at least one writing, with no synchronization. It is undefined behavior in Go, even if it "works" on your machine. A **race condition** is a correctness bug where timing changes the result, even if there is no data race.

```go
var n int
go func() { n++ }()
go func() { n++ }()
// data race: concurrent writes with no synchronization
```

A classic race condition is two goroutines reading a balance of 100 and both writing 90. Each read and write may be locked, but the read-modify-write is not atomic; a mutex around the whole operation fixes it. Happens-before edges are the synchronization you can rely on: a channel send before the corresponding receive, an unlock before a later lock, `once.Do` before any return, and atomics on the same variable. `go f()` happens before `f` starts, but there is no guarantee about when it finishes.

**Why it matters.** `-race` can find a data race, but it cannot find a logical race condition. A service can have correct locks around every individual access and still lose an update because the transaction boundary was wrong.

**Focus on:** data race definition; race condition definition; happens-before edges.

**Ignore for now:** the formal memory model, compiler reordering, and atomic memory ordering.

**Q: What is a data race?** Concurrent access to the same memory, at least one write, with no synchronization.

**Q: Is a race condition always a data race?** No; it can exist with correct locking around individual accesses.

**Q: Name two happens-before edges.** A channel send before the receive; an unlock before a later lock.

**Remember:** a data race is undefined behavior; a race condition is a logic bug that can hide behind locks.

---

## L13. `-race` is evidence, not proof

`go test -race` detects unsynchronized memory accesses that actually occur during the test. It does not prove the absence of races, and it does not detect logical race conditions or time-of-check/time-of-use bugs. On Windows it needs a C toolchain.

```bash
go test -race ./...
go test -race -count=100 ./internal/service
```

`-count=100` runs the test many times to increase the chance of a scheduling interleaving that exposes the race. A green run is evidence that the test did not observe a data race; it is not a proof that the code is race-free.

**Why it matters.** A single green `-race` run can create false confidence. The race may need a different schedule, a different input, or more load. Use `-race` as one signal, along with code review and a design that makes synchronization obvious.

**Focus on:** evidence not proof; repeated runs; data races only, not logical races.

**Ignore for now:** race-detector internals, ThreadSanitizer, `testing/synctest`, and production overhead.

**Q: Does `-race` prove there are no races?** No; it only reports races it observes in that run.

**Q: Does it catch a read-modify-write race with per-access locks?** No; that is a logical race, not a data race.

**Q: Why `-count=100`?** More runs increase the chance of a scheduling interleaving that exposes the race.

**Remember:** `-race` is evidence, not proof.

---

## L14. A bounded worker pool with `errgroup`

A worker pool runs a fixed number of workers reading from a jobs channel. `errgroup.WithContext` returns a group and a derived context; the first non-nil error cancels the derived context, and `Wait` returns that error. The producer must also select on `ctx.Done()` when sending, or it can block forever after a worker error cancels the context.

```go
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
                        return err
                    }
                }
            }
        })
    }
    return g.Wait()
}
```

**Why it matters.** Launching one goroutine per job turns a spike into an outage. A pool bounds concurrency, gives every worker a stop path, and makes the first error visible. The producer's send is part of the pattern: if it ignores cancellation, the workers stop but the producer leaks.

**Focus on:** fixed workers; `errgroup.WithContext`; producer selects on `ctx.Done`; close `jobs`; `Wait`.

**Ignore for now:** pool sizing math, dynamic pools, and queueing theory.

**Q: What does `errgroup.WithContext` do?** The first error cancels the derived context, and `Wait` returns that error.

**Q: Why must the producer select on `ctx.Done`?** Otherwise it can block forever after the workers stop.

**Q: Who closes the jobs channel?** The producer, after sending all jobs.

**Remember:** fixed workers, a cancellable producer, and a first-error stop path.

---

## L15. Bound concurrency with `SetLimit` or a semaphore

`errgroup.SetLimit(8)` caps the number of goroutines in flight; `g.Go` blocks when the limit is reached. `TryGo` is the non-blocking admission check. A buffered channel `make(chan struct{}, 10)` is a counting semaphore: send to acquire a slot, receive to release it. For a per-second limit rather than a concurrency limit, use `golang.org/x/time/rate`.

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(8)
for _, item := range items {
    item := item
    g.Go(func() error {
        return fetch(ctx, item)
    })
}
return g.Wait()
```

**Why it matters.** "One goroutine per item" is the classic Go outage. `SetLimit` and semaphores give you a bound with less code than a full worker pool. Size CPU-bound work near `GOMAXPROCS`; size I/O-bound work to what the downstream can handle, not to the number of items.

**Focus on:** `SetLimit`; buffered-channel semaphore; no unbounded goroutines; size by CPU or I/O.

**Ignore for now:** rate-limiter algorithms, dynamic adjustment, and queueing theory.

**Q: What does `SetLimit` do?** It caps in-flight goroutines; `Go` blocks at the limit.

**Q: What is a buffered-channel semaphore?** Send to acquire a slot, receive to release it.

**Q: When use `rate` instead of a semaphore?** When you need a per-second rate, not just a concurrency cap.

**Remember:** bound the goroutines before you launch them.

---

## L16. Fan-out, fan-in, and pipeline stages

Fan-out sends work to multiple goroutines. Fan-in merges multiple channels into one. A pipeline is a chain of stages, each reading from an input channel and writing to an output channel. The stage that owns an output channel closes it. Cancellation must be checked on both the input and the output send.

```go
func stage(ctx context.Context, in <-chan int) <-chan int {
    out := make(chan int)
    go func() {
        defer close(out) // this stage owns out
        for {
            select {
            case <-ctx.Done():
                return
            case v, ok := <-in:
                if !ok {
                    return
                }
                select {
                case out <- v * 2:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()
    return out
}
```

Fan-in destroys ordering: multiple inputs race into one output, so arrival order is not preserved. If order matters, carry a sequence number and reorder downstream, or use a single consumer.

**Why it matters.** Pipelines compose nicely, but each stage must close its output and stop on cancellation; otherwise a downstream reader blocks forever. Fan-in is fast but unordered, and assuming order is a subtle production bug.

**Focus on:** stage owns output; close; cancellation on input and output; fan-in loses order.

**Ignore for now:** merge implementation details, ordering buffers, and backpressure math.

**Q: Who closes a pipeline stage's output?** The stage that owns it.

**Q: Why does fan-in lose order?** Multiple inputs race into one output; arrival order is not preserved.

**Q: What happens if a stage ignores `ctx.Done` on send?** It can block forever when downstream stops.

**Remember:** each stage owns and closes its output, and fan-in trades order for throughput.

---

## L17. Retry with backoff and jitter

Retry only **transient** errors: timeouts, connection resets, and retryable 5xx responses such as 502, 503, and 504. Do not retry validation failures, "not found," or a canceled request. Bound the attempts, respect the context, and add jitter so clients do not retry in lockstep.

```go
for i := 0; i < attempts; i++ {
    if err = fn(); err == nil || !isTransient(err) {
        return err
    }
    jitter := time.Duration(rand.Int64N(int64(backoff)))
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(backoff/2 + jitter):
    }
    backoff = min(backoff*2, 5*time.Second)
}
```

Exponential backoff increases the delay after each failure; jitter randomizes it. Without jitter, every client that failed at the same time retries at the same time, creating a second spike.

**Why it matters.** Retrying a non-transient error or a canceled request wastes capacity. Unbounded retries can keep a failing dependency down. Synchronized retries turn one outage into a retry storm.

**Focus on:** transient only; bounded attempts; context; jitter.

**Ignore for now:** full-jitter algorithms, retry budgets, and circuit breakers.

**Q: Which errors should you retry?** Transient ones such as timeouts, connection resets, and retryable 5xx responses.

**Q: Why jitter?** To spread retries and avoid a synchronized storm.

**Q: What should stop a retry?** Context cancellation or a deadline.

**Remember:** retry less, wait longer, add jitter, and stop when the caller is gone.

---

## L18. Graceful shutdown: stop accepting, drain, then close

On SIGINT or SIGTERM, a server should stop accepting new work, drain in-flight requests with a deadline, cancel background workers, and close the database and broker. `signal.NotifyContext` gives you a context that is canceled when a signal arrives. `srv.Shutdown(ctx)` stops accepting new connections and waits for in-flight requests until the context deadline.

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
```

Let a second signal hard-exit if shutdown hangs. The order is the point: fail readiness, stop accepting, drain, cancel workers, close resources.

**Why it matters.** A hard kill drops in-flight requests and can leave work half-done. A graceful shutdown gives requests a bounded chance to finish and closes resources in a safe order. Without a deadline, shutdown can hang forever.

**Focus on:** signal context; stop accepting; drain with a deadline; cancel workers; close resources; second signal.

**Ignore for now:** Kubernetes termination grace periods, connection-draining details, and zero-downtime deployment.

**Q: What does `signal.NotifyContext` do?** It returns a context canceled when a signal arrives.

**Q: What does `srv.Shutdown` do?** It stops accepting new connections and waits for in-flight requests until the context deadline.

**Q: Why a second signal?** To force exit if graceful shutdown hangs.

**Remember:** stop accepting, drain with a deadline, then close resources.

---

## L19. SSE: a loop with a disconnect check, a flush, and a heartbeat

Server-Sent Events (SSE) is one-way server push over HTTP. The handler sets `Content-Type: text/event-stream`, checks that the response writer supports `http.Flusher`, writes events, and flushes them. It watches `r.Context().Done()` so it stops when the client disconnects.

```go
func stream(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/event-stream")
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

Send heartbeats, disable compression for the stream, and watch for proxy buffering. Handle authentication on the initial request; SSE itself does not add a new auth mechanism.

**Why it matters.** Without the context check, the handler keeps writing to a disconnected client and leaks a goroutine. Without a flush, events sit in a buffer and the client sees nothing. Without heartbeats, proxies and clients may time out an idle stream.

**Focus on:** `text/event-stream`; `Flusher`; `r.Context().Done()`; heartbeat; proxy buffering.

**Ignore for now:** WebSocket, HTTP/2 push, and SSE fan-out infrastructure.

**Q: How does an SSE handler notice a disconnect?** `r.Context().Done()` closes.

**Q: Why flush?** Otherwise events stay buffered and the client sees nothing.

**Q: Why heartbeats?** To keep proxies and clients from timing out an idle stream.

**Remember:** an SSE handler is a loop with a disconnect check, a flush, and a heartbeat.

---

## L20. Leaks, deadlocks, and panics

Every goroutine needs a defined way to stop. A goroutine leak is a goroutine that can never reach its exit because it is blocked on a send, a receive, a lock, or a timer. A partial deadlock is silent: one request hangs while the rest of the service keeps running. A total deadlock makes the runtime report "all goroutines are asleep."

```go
func first(urls []string) string {
    ch := make(chan string)
    for _, u := range urls {
        go func() { ch <- fetch(u) }()
    }
    return <-ch
}
```

This returns the first result and leaks every other goroutine: they block on the send because nobody reads the channel again. Fix it with a buffered channel of `len(urls)`, a cancellation context, or an `errgroup` that stops the losers. A panic in an unrecovered goroutine kills the whole process; recover only in a deferred function in that goroutine, at a boundary.

**Why it matters.** Leaks accumulate until the process runs out of memory or latency collapses. A panic in a background goroutine can take down the service even if the HTTP handler has a recover. Find leaks with `runtime.NumGoroutine`, a stack dump (`SIGQUIT` or `pprof/goroutine?debug=2`), and `goleak` in tests.

**Focus on:** stop path; leak signals; stack dump; panic boundary.

**Ignore for now:** `goleak` internals, the pprof UI, and runtime tracing.

**Q: What is the first question for every goroutine?** How does it stop?

**Q: How do you find a leak?** `NumGoroutine`, a stack dump, or `goleak`.

**Q: What happens if a goroutine panics unrecovered?** The whole process crashes.

**Remember:** every goroutine needs a stop path; a leak is a goroutine that can never reach its exit.

---

## L21. Where Day 2 shows up in ControlPlane

ControlPlane uses concurrency for I/O and cancellation, not correctness. The consumer processes deliveries sequentially with prefetch 1 (`Qos(1)`), so at most one unacked task is in flight; a crash causes one redelivery, and ordering and failure classification stay simple. The outbox relay uses a buffered `wake` channel of 1 with a non-blocking send; the poll is the safety net, so the relay does not leak a goroutine. A signal context gives HTTP, the relay, and the consumer one cancellation tree with a 10-second drain.

The slug race and the version race are settled by Postgres constraints and conditional updates, not by a Go mutex. Go's job is to issue those statements with bounded concurrency and to stop cleanly across processes and replicas.

**Say this in the interview:**

> "I use goroutines for I/O concurrency and cancellation, but I do not use them as a correctness mechanism. The slug race and the version race are settled by Postgres constraints and conditional updates. Go's job is to issue those statements with bounded concurrency and to stop cleanly."

**Why it matters.** The interviewer wants to hear that a Go mutex only protects one process. A unique index or a conditional `UPDATE` protects the invariant across every replica. Connecting the concurrency pattern to the database boundary is the senior signal.

**Focus on:** sequential consumer/prefetch 1; buffered wake; one cancellation tree; the database is the arbiter.

**Ignore for now:** exact file paths, RabbitMQ tuning, and Postgres isolation details.

**Q: Why does the consumer process sequentially?** One unacked delivery per loop keeps ordering and failure handling simple; the DB is the concurrency boundary.

**Q: Why prefetch 1?** At most one unacked task is in flight, so a crash causes one redelivery and the worker does not hoard work.

**Q: Why isn't a Go mutex enough for the slug race?** The race is across processes and replicas; the Postgres unique index is the arbiter.

**Remember:** Go concurrency moves work; the database decides who wins.

---

## Coverage check

Every source section is accounted for; nothing is silently dropped.

| Source | Where it is covered | Status |
|---|---|---|
| §1 Scheduler | L1, L2, L3 | Covered |
| §2 Channels | L4, L5, L6 | Covered |
| §3 `select` | L7 | Covered |
| §4 `context` | L8, L9 | Covered |
| §5 `sync` and atomics | L10, L11 | Covered |
| §6 Memory model | L12, L13 | Covered |
| §7 Patterns | L14–L19 | Covered |
| §8 Leaks and failure modes | L20 | Covered |
| §9 Active practice | Day 2 drills | Practice, not re-taught |
| §10 Grill Q1–Q20 | Table below | Covered |
| §11 ControlPlane tie-in | L21 | Covered |
| §12 End-of-day checkpoint | Recall sheet below | Covered |

**Source grill coverage**

| Grill | Question | Lesson |
|---|---|---|
| Q1 | Goroutine vs OS thread | L1 |
| Q2 | G-M-P and `GOMAXPROCS` | L2, L3 |
| Q3 | Unbuffered vs buffered channel | L4, L6 |
| Q4 | Who closes, and send/receive after close | L5 |
| Q5 | Nil channel in `select` | L5, L7 |
| Q6 | Two ready `select` cases | L7 |
| Q7 | Why `defer cancel()` | L8, L9 |
| Q8 | Mutex or channel | L10 |
| Q9 | Happens-before | L12 |
| Q10 | Data race vs race condition | L12, L13 |
| Q11 | `WaitGroup.Add` before `go` | L11 |
| Q12 | `errgroup.WithContext` | L14 |
| Q13 | Bounding concurrency | L14, L15 |
| Q14 | Finding a goroutine leak | L20 |
| Q15 | `time.After` in a loop | L7, L20 |
| Q16 | Panic in an unrecovered goroutine | L20 |
| Q17 | Sequential ControlPlane consumer | L21 |
| Q18 | Prefetch 1 | L21 |
| Q19 | Biggest Go web-service mistake | L3, L15, L16, L17, L18, L19 |
| Q20 | Go concurrency and the slug race | L21 |

---

## One-line recall sheet

- A goroutine is runtime-scheduled; `main` returning kills all goroutines.
- G runs on M, M needs P, and `GOMAXPROCS` sets the number of Ps.
- Go 1.25 reads the Linux cgroup CPU limit for `GOMAXPROCS`; older versions need `automaxprocs`.
- Unbuffered is a handshake; buffered is a bounded waiting room.
- The sender closes; close means no more values, not cancellation.
- `select` picks a ready case at random; a nil channel disables its case.
- Pass context down, `defer cancel()`, and check `Done` in loops.
- Mutex for in-place state; channel for handoff.
- `Add` before `go`; do not copy a lock after first use.
- A data race is undefined behavior; a race condition can hide behind locks.
- `-race` is evidence, not proof.
- Bound goroutines; every goroutine needs a stop path.
- ControlPlane: sequential consumer, prefetch 1, the database is the arbiter.

**Next:** open [`day-2-concurrency.md`](day-2-concurrency.md) for the full worker-pool drill and grill, then run the worker-pool drill first. It is the fastest way to see bounded concurrency, cancellation, and the first-error stop path in one program.
