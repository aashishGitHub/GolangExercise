# Day 2 theory cupboard

**Do not read this before the lab.** This file is the extra detail behind [`day-2b-concurrency-lab.md`](day-2b-concurrency-lab.md). Open it only when a question goes deeper than the lab, or when you have spare time after the day's 2-hour core.

**How to use it:** read one section, say the one-screen answer at the end aloud, then close the file. Do not try to memorize the diagrams.

---

## 1. G-M-P in one picture

The Go scheduler has three nouns:

| Piece | What it is | What it is not |
|---|---|---|
| **G** | A goroutine: a function with its own stack and instruction pointer, plus scheduler state. | Not an OS thread. Not a C# `Task` or a JavaScript promise. |
| **M** | A machine: one OS thread, created by the Go runtime, that the kernel schedules onto a CPU core. | Not a goroutine. It executes machine instructions. |
| **P** | A processor: a logical scheduling context that owns a local run queue of runnable Gs. | Not a CPU core. The OS decides which core an M runs on. |

The sentence to memorize is: **G is the work, M is the thread, and P is the permission and queue that let an M run the work.**

`GOMAXPROCS` is the number of Ps. It is not the number of goroutines, and it is not the number of OS threads. The runtime can create more Ms than Ps, especially when threads are blocked in system calls, but only an M holding a P may execute Go code.

```text
   G1 G2 G3 G4          G5 G6 G7
       |                   |
   +---------+         +---------+
   |   P0    |         |   P1    |
   | runqueue|         | runqueue|
   +---------+         +---------+
       |                   |
      M0                  M1          OS threads
       |                   |
    CPU core            CPU core      OS scheduler decides
```

With `GOMAXPROCS=2`, at most two Ms execute Go code at the same time. The other Gs are waiting in a run queue. They are alive; they are just not running at this instant.

### Coming from C# or JavaScript

- A C# `Task` is a handle to work that may finish later; `async`/`await` frees the calling thread while the operation is pending. A Go goroutine is a running function with its own stack. It can block, and the runtime parks it and runs another G on the same M.
- The .NET thread pool manages worker threads. Go does its own M:N scheduling: many Gs, fewer Ps, and as many Ms as needed. `GOMAXPROCS` is closer to "how many Ms may run Go code at once" than to "how many goroutines exist".
- JavaScript has one call stack and an event loop. A blocking call freezes the whole process. Go has many goroutines and multiple Ms, so a blocking syscall does not freeze the program; the P moves to another M.

There is no exact C# or JavaScript equivalent of P. The closest idea is an execution permit plus a local work queue. Do not call P a core.

---

## 2. How a goroutine gets scheduled

At startup the runtime creates `GOMAXPROCS` Ps and starts Ms as needed to run Go code. The `main` goroutine is a G like any other.

1. `go f()` creates a new G and puts it on the current P's local run queue. If that queue is full, the G goes to a global queue.
2. An M that holds a P takes a G from that P's queue and runs it.
3. The G runs until it blocks, returns, or is preempted. Then the M takes the next G.
4. If a P's queue is empty, it steals runnable Gs from another P's queue. That is work stealing.
5. If a G blocks on a channel, mutex, or timer, it is parked. The M does not block; it runs another G from the P's queue.
6. If a G enters a truly blocking system call, the M blocks in the kernel. The runtime detaches the P from that M and hands it to another M (or wakes one), so other Gs keep running. When the syscall returns, the G becomes runnable again and the M tries to get a P back.

The syscall handoff is the part that surprises people:

```text
G1 on M0 calls a blocking read()

before:  P0 -> M0 -> G1 (running)
during:  P0 -> M1 -> G2 (running)      M0 -> G1 (blocked in kernel)
after:   G1 becomes runnable; M0 tries to get a P again
```

M0 is still alive and blocked in the kernel, but it does not hold a P, so it is not stopping other Go code from running. This is why one slow file read or cgo call does not freeze the whole program.

Network I/O is usually different. Go's netpoller parks the G and keeps the M free, so a slow socket does not consume an OS thread for the whole wait. A truly blocking syscall, such as some file operations or cgo, is what triggers the P handoff above.

---

## 3. `GOMAXPROCS` and containers

`GOMAXPROCS` sets the number of Ps the scheduler can use. Before Go 1.25, it defaulted to the number of logical CPUs visible to the process, not the container's CPU limit. A container limited to 2 CPUs on a 16-CPU host could still start with 16 Ps, causing throttling and latency spikes.

Go 1.25 changed the default on Linux: the runtime reads the cgroup CPU bandwidth limit and uses the lower value, then updates `GOMAXPROCS` when the limit changes. Setting `GOMAXPROCS` manually disables that automatic behavior. On older Go versions, or when you override it, set it from the cgroup limit or use `automaxprocs`.

```go
fmt.Println(runtime.GOMAXPROCS(0))  // current P count
fmt.Println(runtime.NumGoroutine()) // current goroutine count
```

`runtime.NumGoroutine()` is a cheap leak signal, but it is a snapshot, not proof.

What the setting changes:

- `GOMAXPROCS=1`: one P, so one M executes Go code at a time. You still have many goroutines, and they interleave at blocking points and preemption points. This is concurrency without CPU parallelism.
- `GOMAXPROCS=4`: up to four Ms execute Go code at once, so CPU-bound goroutines can use four cores in parallel.
- If you force `GOMAXPROCS=64` on a four-core container: there are 64 Ps, but the OS still has four cores. CPU-bound work now pays extra scheduling and context-switch cost. This is why the container CPU limit matters.

Since Go 1.14, a long-running goroutine can be preempted asynchronously, so a tight loop cannot monopolize a P forever. Preemption is a fairness mechanism, not a guarantee about when a particular goroutine runs.

---

## 4. The memory model in plain words

A **data race** is two goroutines accessing the same memory at the same time, at least one writing, with no synchronization. Go treats it as undefined behavior, even if it "works" on your machine.

A **race condition** is a timing-dependent logic bug. It can exist even when every individual memory access is locked, because the read-modify-write as a whole is not atomic.

```go
var n int
go func() { n++ }()
go func() { n++ }()
// data race: concurrent writes with no synchronization
```

A classic race condition is two goroutines reading a balance of 100 and both writing 90. Each read and write may be locked, but the read-modify-write is not atomic. A mutex around the whole operation fixes it.

**Happens-before** is the synchronization you can rely on. If A happens before B, B sees A's writes. The common edges are:

- A channel send happens before the matching receive.
- An unlock happens before a later lock on the same mutex.
- `once.Do` happens before any return from `Do`.
- An atomic operation on a variable happens before a later atomic operation on the same variable that observes it.
- `go f()` happens before `f` starts, but there is no guarantee about when `f` finishes.

Without an edge, concurrent access is a data race. `-race` can find a data race that actually happens in that run. It cannot prove the absence of races, and it cannot find a logical race condition built from correctly locked steps.

---

## 5. Scheduler internals you may be asked about

**Work stealing.** A P with an empty run queue takes Gs from another P's queue. This keeps cores busy without a central lock. It is why a pool of Ps can share a large number of goroutines.

**Preemption.** A goroutine that runs too long is asked to stop at a safe point. Since Go 1.14 this can happen asynchronously, so a tight loop does not block other goroutines on the same P forever.

**`sysmon`.** A runtime background thread that watches for long-running syscalls, network polling, and other housekeeping. You do not call it; it is part of the runtime.

**Netpoller.** For network I/O, the runtime parks the goroutine and registers the file descriptor with the OS poller. When the socket is ready, the goroutine becomes runnable again. This is why thousands of network goroutines can wait without thousands of OS threads.

**Why the runtime creates more Ms than Ps.** If an M blocks in a syscall, it cannot run Go code. The runtime hands the P to another M, or creates one, so the Ps stay busy. The thread count can go above `GOMAXPROCS`; the number of Ps does not.

**What not to say in an interview:** "P is a core" or "GOMAXPROCS is the number of threads". Both are wrong. P is a scheduling context, and `GOMAXPROCS` is the number of Ps.

---

## 6. Channel and lock internals (only if asked)

**Channel.** A channel is a runtime structure with a buffer, a send queue, and a receive queue. An unbuffered channel has no buffer, so a send hands the value directly to a waiting receiver. A buffered channel stores values until the buffer is full. A blocked sender or receiver is parked and woken by the runtime. You do not need the struct names to use channels; know the behavior.

**Close.** Closing marks the channel as closed and wakes all waiters. Receivers get zero values with `ok == false` after the buffer drains. Senders panic because there is no valid "send after end-of-stream" state.

**Mutex.** A `sync.Mutex` has a fast path and a slow path. Uncontended locks are cheap. Under contention, waiters queue, and the runtime may hand the lock to a waiter to avoid starvation. You do not tune this directly. Keep critical sections short and do not copy a mutex after first use.

**`RWMutex`.** Multiple readers can hold the lock at once, but a writer is exclusive. It helps read-mostly state, but it adds overhead, and a waiting writer blocks new readers. Measure before choosing it.

**`sync.Pool`.** A pool of temporary objects that can be reused. It is not a cache: the GC may drop items. Use it for allocation-heavy short-lived objects, not for state you must keep.

---

## 7. One-screen interview answers

Say these aloud. If you can say all six, you know the cupboard.

1. **G-M-P:** "G is the work, M is the OS thread, P is the scheduling context and run queue. An M must hold a P to run Go code. `GOMAXPROCS` sets the number of Ps."
2. **Blocking syscall:** "The M blocks in the kernel, but the runtime hands its P to another M, so other goroutines keep running."
3. **`GOMAXPROCS` in a container:** "Go 1.25 reads the Linux cgroup CPU limit by default. Older versions see host CPUs unless you set it or use `automaxprocs`."
4. **Data race vs race condition:** "A data race is unsynchronized access with a write; it is undefined behavior. A race condition is a timing bug that can hide behind correct per-access locks."
5. **Happens-before:** "A channel send before the receive, an unlock before a later lock, and `once.Do` before any return are the edges you can rely on."
6. **`-race`:** "It is evidence, not proof. It only reports races it observes in that run."

**Remember:** the lab teaches the moves; this cupboard explains the engine. Do not confuse the two.
