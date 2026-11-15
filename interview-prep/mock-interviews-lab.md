# Mock interview lab — Question 1: the `Process(ids)` bug tree

**This is the doing file for [`mock-interviews.md`](mock-interviews.md).** The interview file has the question and the proposed answer. This file turns every issue and every fix in that answer into a small program you can run. Read less, run more. One program shows one idea. Predict the output, run it, then read the short note.

**Tested on:** Go 1.27.4, Windows. Use Go 1.22 or newer: the loop-variable rule and the `min` built-in depend on it, and `errors.Join` needs Go 1.20. Every output below was produced by the code shown, except where it says "one possible run", "expected", or where a number is machine-dependent.

**Time:** 55–70 minutes for Parts 1 and 2, 30–35 minutes for Parts 3 and 4. Split it into two sittings if that is easier. Stop at two hours.

**If you only have 30 minutes:** run #1, #3, #4, #6, #8, #13, #15, #17, #21. That is the shortest path to the ideas the interviewer will probe.

**`-race` note:** the race detector needs CGO and a C compiler. On this Windows box it prints:

```text
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
```

Run the race commands on Linux, WSL, or CI, or install a C compiler. The non-race programs all run here.

**Theory is separate.** The primitives live in [`day-2b-concurrency-lab.md`](day-2b-concurrency-lab.md). The deeper scheduler and memory-model notes live in [`day-2-theory-cupboard.md`](day-2-theory-cupboard.md). This lab ties those primitives to one interview question.

| Time | Block |
|---|---|
| 0:00–0:10 | Read the bug tree, say the 45-second answer aloud |
| 0:10–0:50 | Part 1: reproduce the failures |
| 0:50–1:25 | Part 2: fix the race, errors, and concurrency |
| 1:25–1:50 | Part 3: the million-id and 10k-req/s concerns |
| 1:50–2:00 | Part 4: assemble, test, final Q&A |

---

## How to use this lab

1. Make one folder and one Go module:

   ```bash
   mkdir mock1
   cd mock1
   go mod init mock1
   ```

2. For the `errgroup` examples, add the dependency once:

   ```bash
   go get golang.org/x/sync/errgroup
   ```

3. Save each example as its own file, for example `01-append-race.go`. Run **one file at a time**:

   ```bash
   go run 01-append-race.go
   ```

   Do not run `go run .` in this folder. Every file has its own `main`, so the folder is not one program.

4. For the old loop-variable demo in #3, make a second module:

   ```bash
   mkdir loop121
   cd loop121
   go mod init loop121
   go mod edit -go=1.21
   ```

5. Say the **If asked** line aloud before moving on. If you cannot say it, reread the note, not the whole file.

---

## The map: issue to example

| You need to... | Concept | Example |
|---|---|---|
| Reproduce the bug | concurrent `append` | 1 |
| Show why a map needs a lock | concurrent map writes | 2 |
| Explain the loop-variable rule | per-iteration vs shared | 3 |
| Show the cost of one goroutine per item | 100k stacks | 4 |
| Show a downstream stampede | max in-flight | 5 |
| Show a dropped error | zero `Result` | 6 |
| Show why `ctx` matters | cancellation | 7 |
| Fix the race and keep order | preallocated index | 8, 9 |
| Answer "why not a map?" | map + mutex | 10 |
| Let the caller classify errors | `%w`, `errors.Is/As` | 11 |
| Stop siblings on the first error | `errgroup` | 12 |
| Cap in-flight work | `SetLimit` | 13 |
| Avoid the `SetLimit` traps | nesting, blocked `Go` | 14 |
| Choose fail-fast or collect-all | per-item errors, `errors.Join` | 15 |
| Use a worker pool instead | fixed workers | 16 |
| Resume a 1M-item job | batch + checkpoint | 17 |
| Keep memory bounded | stream to a sink | 18 |
| Avoid duplicate work | dedupe | 19 |
| Handle empty input | nil vs empty slice | 20 |
| Assemble the full answer | `Process` + tests | 21 |
| Serve 1M ids at 10k req/s | 202 + queue | 22 |

---

# Part 0 — The bug tree

The snippet from the interview file:

```go
func (s *Server) Process(ids []int) []Result {
	var results []Result
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := s.fetch(id)
			results = append(results, r)
		}()
	}
	wg.Wait()
	return results
}
```

Annotated:

```go
func (s *Server) Process(ids []int) []Result {
	var results []Result                    // nil; append grows it
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)                           // OK: Add before go
		go func() {
			defer wg.Done()                 // OK
			r := s.fetch(id)                // error dropped; no ctx; pre-1.22 id capture
			results = append(results, r)    // DATA RACE
		}()
	}
	wg.Wait()                               // OK, but there is no deadline
	return results                          // completion order; no error; no ids
}
```

## Every issue, in interview order

| # | Issue | Why it bites | Fix | Example |
|---:|---|---|---|---|
| 1 | Data race on `results` | Two goroutines append at once. `append` reads and writes the slice header, so results can be lost or overwritten, and the header can be corrupted. | Preallocate `len(ids)`; each goroutine writes `results[i]`. | 1, 8, 9 |
| 2 | One goroutine per id | 100k goroutines cost ~781 MB of stack on this run; 1M is roughly 8 GB before any work. | `errgroup.SetLimit`, a semaphore, or a worker pool. | 4, 13, 16 |
| 3 | No downstream bound | 100 calls hit a 4-slot database at once: pool waits, timeouts, retries, cascading failure. | Size concurrency to the downstream pool or rate limit. | 5, 13 |
| 4 | Dropped error | `s.fetch` can fail, but the code appends `Result{}`. The caller cannot tell failure from an empty result. | Return a wrapped error, or record one error per item. | 6, 11, 15 |
| 5 | No context | A client disconnect or deadline cannot stop `fetch`; work keeps running after the caller is gone. | `ctx` first parameter; pass it into `fetch`. | 7, 12, 21 |
| 6 | Loop-variable capture | Before Go 1.22, every goroutine shares one `id`; the classic bug prints the final value. | Go 1.22+, or pass `id` as a parameter. | 3 |
| 7 | Order is completion order | `append` appends when a fetch finishes, not at the id's input position. | Index into the result slice by input position. | 8, 9, 10 |
| 8 | Return type has no error | `[]Result` cannot express "failed", "partial", or "retry these ids". | Return `([]Result, error)`, or per-item results. | 15, 21 |
| 9 | No capacity hint | `append` grows and copies the backing array repeatedly. | `make([]Result, len(ids))`. | 8 |
| 10 | No partial-failure policy | All-or-nothing vs per-item is decided by accident, not by the caller's needs. | Choose fail-fast or collect-all explicitly. | 15 |
| 11 | 1M ids inline | Memory, latency, no checkpoint, no resume, no dedupe; the HTTP request times out and is retried. | Batch + checkpoint + idempotency + queue. | 17, 18, 19, 22 |
| 12 | Panic safety | A panic in any worker kills the process. `errgroup` does not recover panics either. | Know the policy: recover at the boundary, or let it crash and alert. | 21 note |

## What is actually fine

Do not invent bugs. Interviewers notice.

- `wg.Add(1)` runs before `go`. Correct. If `Add` ran inside the goroutine, `Wait` could return before the counter goes up.
- `defer wg.Done()` is correct.
- `wg.Wait()` is correct; it waits for every worker.
- The loop variable is correct on a Go 1.22+ module. Passing `id` as a parameter is still clearer and works on every version.
- Returning a slice is not inherently wrong. The ambiguity is the missing error and the completion order, not the slice itself.

## Your answer vs the target

| Your answer | Verdict | Upgrade |
|---|---|---|
| "Catch errors and append when `error != nil`" | Right instinct, wrong shape. | Do not append a zero `Result`. Return or wrap the error, or keep `errs[i]` beside `results[i]`. Examples 6 and 15. |
| "Return `map[int]Result` keyed by id" | Works with a lock, but it is not the default. | A map is unordered, collapses duplicate ids, and costs more per entry. An indexed slice is better for `Process(ids []int)`. Example 10. |
| "`fetch` is async, so handle it" | Right. | Pass `ctx`, bound concurrency, and classify the error. Examples 7, 12, 13. |
| "1M ids should be batched, with pause/restart from a point" | Right direction. | Add a durable checkpoint, idempotent writes, dedupe, and a queue off the request path. Examples 17, 19, 22. |

## The 45-second answer

Say this aloud before you open the first program:

> "There are three correctness bugs: a data race on the shared `results` slice, an unbounded goroutine per id, and a dropped error with no context. I would return `([]Result, error)`, preallocate `len(ids)` and write `results[i]`, pass `ctx` into `fetch`, and use `errgroup.WithContext` with `SetLimit` sized to the downstream. For a million ids I would dedupe, batch with a checkpoint, make the writes idempotent, and move the job off the request path."

That is the whole answer. The rest of this lab is the proof.

## The tree in one screen

```text
Process(ids)
├── results slice
│   ├── concurrent append ............ #1  → preallocate + results[i] #8
│   ├── completion order ............. #9  → index by input position #8
│   └── map[int]Result alternative ... #10 → mutex + sort, or keep the slice
├── goroutines
│   ├── one per id ................... #4  → SetLimit #13 or worker pool #16
│   ├── downstream stampede .......... #5  → semaphore sized to the pool #13
│   └── SetLimit traps ............... #14 → no nesting, no ctx-blind block
├── fetch
│   ├── error dropped ................ #6  → wrap with %w and the id #11
│   ├── no context ................... #7  → ctx + errgroup #12
│   ├── fail-fast vs collect-all ..... #15 → choose per item or per batch
│   └── panic ........................ #21 → errgroup does not recover
└── 1M ids at 10k req/s
    ├── batch + checkpoint ........... #17 → resume from the last committed batch
    ├── memory and latency ........... #18 → stream to a sink
    ├── duplicate ids ................ #19 → dedupe, or cache per id
    ├── empty input .................. #20 → decide nil vs []
    └── request shape ................ #22 → 202 + bounded queue + status
```

---

# Part 1 — Reproduce the failures

## 1. Concurrent `append` loses results

```go
package main

import (
	"fmt"
	"sync"
)

type Result struct {
	ID int
}

func main() {
	const n = 1000

	for round := 1; round <= 3; round++ {
		var results []Result
		var wg sync.WaitGroup
		start := make(chan struct{})

		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start // release all goroutines at once to widen the window
				results = append(results, Result{ID: id})
			}(i)
		}

		close(start)
		wg.Wait()
		fmt.Printf("round %d: got %d of %d\n", round, len(results), n)
	}
}
```

**Output (one possible run):**

```text
round 1: got 1000 of 1000
round 2: got 972 of 1000
round 3: got 996 of 1000
```

Round 1 got everything. That is the trap: the race does not fail every time. The next run can lose results, corrupt the slice, or crash.

**What happened:** `append` is not a single instruction. It reads the slice header (pointer, length, capacity), maybe allocates a bigger array and copies, writes the new element, then writes the new header back. Two goroutines can read the same length and write to the same slot, or one can overwrite the other's new length. The updates are lost.

**Why it matters:** This is the primary correctness bug in the interview snippet. At 10k req/s it will happen constantly. A test that passes once proves nothing.

**Wrong fix, do not copy:** capacity headroom does not help.

```go
results := make([]Result, 0, n) // removes reallocation, not the shared header race
```

**Output (one possible run):**

```text
round 1: got 900 of 1000
round 2: got 827 of 1000
round 3: got 817 of 1000
```

The capacity only removes the copy. Concurrent `append` still races on the length and the slots.

**How to see the detector:** on Linux or CI, run the same program with `go run -race 01-append-race.go`. It prints `WARNING: DATA RACE` with both goroutine stacks. On this Windows machine the detector needs CGO and a C compiler; the error message is in the header.

**If asked:** "`append` is a read-modify-write of the slice header plus a write to the backing array. Concurrent appends need a lock, or better, each goroutine writes its own index."

---

## 2. A map without a lock dies harder

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	m := make(map[int]int)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				m[id*100+j] = j
			}
		}(i)
	}

	close(start)
	wg.Wait()
	fmt.Println("entries:", len(m))
}
```

**Output (first line):**

```text
fatal error: concurrent map writes
```

The runtime then prints a stack trace and exits. The stack is omitted here.

**What happened:** Go maps are not safe for concurrent writes. The runtime sets a `hashWriting` flag on each write; a second concurrent write sees it and calls `fatal`. This is not a panic you can recover. The process is gone.

**Why it matters:** This is what happens if you take the "return a map" idea and write to it from the goroutines without a mutex. A pre-sized map does not help. `sync.Map` is not a default fix either; it is for specific access patterns, not a general map replacement.

**If asked:** "A Go map is not safe for concurrent writes. The runtime throws a fatal error, which `recover` cannot catch. You need a mutex, a shard per goroutine, or a different data shape."

---

## 3. Loop variables changed in Go 1.22

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup

	fmt.Println("--- Go 1.22+ loop variable ---")
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println("id =", i)
		}()
	}
	wg.Wait()

	fmt.Println("--- pass the value as a parameter ---")
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			fmt.Println("param =", id)
		}(i)
	}
	wg.Wait()
}
```

**Output (one possible run):**

```text
--- Go 1.22+ loop variable ---
id = 3
id = 2
id = 1
--- pass the value as a parameter ---
param = 1
param = 3
param = 2
```

**What happened:** Since Go 1.22, each iteration has its own loop variable, so each goroutine sees the value from its own iteration. The order is still random because the goroutines run concurrently.

**The old behavior, reproduced:** make a separate module with an old language version. The same program prints the final value in every goroutine.

```bash
mkdir loop121
cd loop121
go mod init loop121
go mod edit -go=1.21
```

Save only the first loop (the Go 1.22 section) as `main.go`, then run `go run .`.

**Output (one possible run):**

```text
id = 4
id = 4
id = 4
```

**Why 4, not 3?** The loop is `for i := 1; i <= 3; i++`. After the third body, `i++` makes `i` 4, the condition fails, and the goroutines usually run after that. They all read the same variable. The read also races with the loop's write, so mixed values are possible; `4 4 4` is the common result. The language version in `go.mod` controls this: `go 1.21` keeps the old semantics even when the toolchain is newer.

**Why it matters:** On a Go 1.22+ module the snippet is correct. In an older module it is a data race. Passing `id` as a parameter works in every version and makes the intent obvious.

**If asked:** "Before Go 1.22 the loop variable was shared, so all goroutines could see the final value. Since 1.22 it is per-iteration. I still pass it explicitly."

---

## 4. 100k goroutines is ~781 MB of stack

```go
package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

func main() {
	const n = 100_000
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // keep every goroutine alive until we measure
			time.Sleep(10 * time.Millisecond)
		}()
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Println("goroutines while running:", runtime.NumGoroutine())
	fmt.Println("heap MB while running:", m.HeapAlloc/1024/1024)
	fmt.Println("stack MB while running:", m.StackInuse/1024/1024)

	begin := time.Now()
	close(start)
	wg.Wait()
	fmt.Println("elapsed after release:", time.Since(begin).Round(time.Millisecond))
}
```

**Output (one possible run):**

```text
goroutines while running: 100001
heap MB while running: 59
stack MB while running: 781
elapsed after release: 91ms
```

**What happened:** The barrier (`<-start`) kept all 100,000 goroutines alive at the measurement. `NumGoroutine` counted them plus `main`. The stack number is the surprise: about 781 MB, or roughly 8 KB per goroutine on this Go version.

**Why it matters:** A million goroutines is not "just more of the same". At this rate it is roughly 8 GB of stack, plus scheduler and GC work. The broken snippet creates one goroutine per id for every request. At 10k req/s, that is not a concurrency limit; it is a memory and scheduling incident.

**The fix in one line:** `g.SetLimit(64)` keeps at most 64 goroutines alive. At roughly 8 KB each, that is about half a megabyte of initial stacks instead of gigabytes. Example 13 shows it.

**If asked:** "One goroutine per item is O(n) memory and scheduler work. 100k cost about 781 MB of stack here; a million is not something I would serve inline."

---

## 5. 100 goroutines stampede a 4-slot database

```go
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type fakeDB struct {
	inFlight atomic.Int32
	max      atomic.Int32
}

func (db *fakeDB) get(id int) {
	cur := db.inFlight.Add(1)
	for {
		old := db.max.Load()
		if cur <= old || db.max.CompareAndSwap(old, cur) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	db.inFlight.Add(-1)
}

func run(ids int, limit int) int32 {
	db := &fakeDB{}
	start := make(chan struct{})
	var wg sync.WaitGroup

	sem := make(chan struct{}, limit) // limit <= 0 means unbounded
	for i := 0; i < ids; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if limit > 0 {
				sem <- struct{}{}
				defer func() { <-sem }()
			}
			<-start
			db.get(id)
		}(i)
	}

	close(start)
	wg.Wait()
	return db.max.Load()
}

func main() {
	fmt.Println("unbounded max in-flight:", run(100, 0))
	fmt.Println("semaphore(4) max in-flight:", run(100, 4))
}
```

**Output:**

```text
unbounded max in-flight: 100
semaphore(4) max in-flight: 4
```

**What happened:** In the first run, all 100 goroutines entered `get` at once. In the second, the buffered channel held four permits, so only four entered.

**Why it matters:** A database has a connection pool, a CPU budget, and a rate limit. If the handler sends 100 queries at once, the pool queues them, the queries wait, the latency climbs, and the timeouts trigger retries. The retries make the stampede worse. Bounding at the caller is cheaper than timing out at the database.

**How to size it:** use the pool size, the downstream rate limit, or a measured saturation point. `64` in the proposed answer is a starting point, not a law. The right number is the largest concurrency that keeps the downstream healthy.

**If asked:** "Bound concurrency at the caller to what the downstream can take. A semaphore of four keeps four in flight; the other 96 goroutines wait instead of hammering the pool."

---

## 6. A dropped error becomes a zero result

```go
package main

import (
	"errors"
	"fmt"
)

type Result struct {
	ID    int
	Value string
}

var ErrNotFound = errors.New("not found")

func fetch(id int) (Result, error) {
	if id%3 == 0 {
		return Result{}, fmt.Errorf("id %d: %w", id, ErrNotFound)
	}
	return Result{ID: id, Value: fmt.Sprintf("v%d", id)}, nil
}

func main() {
	ids := []int{1, 2, 3, 4, 5}
	var results []Result
	for _, id := range ids {
		r, _ := fetch(id) // error dropped
		results = append(results, r)
	}

	for _, r := range results {
		fmt.Printf("id=%d value=%q\n", r.ID, r.Value)
	}
	fmt.Println("errors seen by caller: 0")
}
```

**Output:**

```text
id=1 value="v1"
id=2 value="v2"
id=0 value=""
id=4 value="v4"
id=5 value="v5"
errors seen by caller: 0
```

**What happened:** `fetch(3)` failed. The code threw the error away and appended the zero value: `ID: 0`, `Value: ""`. The caller sees five results and no error. It cannot tell that id 3 failed, or that the third entry is not the result for id 3.

**Why it matters:** This is silent data corruption. A zero result can be a valid domain value, so the caller cannot even use "zero means failure" as a rule. The error must travel with the result, or replace it.

**The fix in one line:** either return the first error (`return nil, fmt.Errorf("fetch %d: %w", id, err)`), or keep `results[i]` and `errs[i]` side by side. Examples 11 and 15.

**If asked:** "Never drop an error from a fan-out. Either fail the batch with a wrapped error, or record one error per item. Appending a zero value hides the failure."

---

## 7. No context means no cancellation

```go
package main

import (
	"context"
	"fmt"
	"time"
)

type Result struct{ ID int }

func fetchIgnoringContext(ctx context.Context, id int) (Result, error) {
	time.Sleep(500 * time.Millisecond) // no ctx check: cancellation cannot stop it
	return Result{ID: id}, nil
}

func fetchWithContext(ctx context.Context, id int) (Result, error) {
	select {
	case <-time.After(500 * time.Millisecond):
		return Result{ID: id}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	start := time.Now()
	_, err := fetchWithContext(ctx, 2)
	fmt.Printf("checks ctx:  %v, err=%v\n", time.Since(start).Round(10*time.Millisecond), err)
	cancel()

	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start = time.Now()
	_, err = fetchIgnoringContext(ctx, 1)
	fmt.Printf("ignores ctx: %v, err=%v\n", time.Since(start).Round(10*time.Millisecond), err)
}
```

**Output (one possible run):**

```text
checks ctx:  30ms, err=context deadline exceeded
ignores ctx: 500ms, err=<nil>
```

The context-aware call stopped at the deadline. The call that ignored `ctx` ran to completion. The 30ms instead of 20ms is timer granularity and scheduling; the point is the difference between ~20ms and 500ms.

**What happened:** `fetchWithContext` selects on `ctx.Done()`. `fetchIgnoringContext` never looks at it. Cancellation is cooperative: the callee has to check.

**Why it matters:** In the broken snippet, `fetch(id)` has no context at all. When the client disconnects or the request deadline passes, `Process` may return, but the goroutines keep calling the downstream. Their results are thrown away, and they compete with live requests. That is work you paid for and cannot use.

**The fix:** accept `ctx` as the first parameter of `Process`, pass it into `fetch`, and check it in the HTTP client, database call, or sleep. The proposed answer does this.

**If asked:** "`ctx` has to reach the I/O call. If `fetch` ignores it, cancellation stops the caller, not the work. Cancellation is a signal, not a kill."

---

# Part 2 — Fix the race and the error path

## 8. Preallocate the slice and write by index

```go
package main

import (
	"fmt"
	"sync"
)

type Result struct {
	ID    int
	Value string
}

func process(ids []int) []Result {
	results := make([]Result, len(ids))
	var wg sync.WaitGroup

	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = Result{ID: id, Value: fmt.Sprintf("v%d", id)}
		}()
	}

	wg.Wait()
	return results
}

func main() {
	ids := []int{30, 10, 20}
	results := process(ids)

	for i, r := range results {
		fmt.Printf("position %d: id=%d value=%s\n", i, r.ID, r.Value)
	}
}
```

**Output:**

```text
position 0: id=30 value=v30
position 1: id=10 value=v10
position 2: id=20 value=v20
```

**What happened:** The slice was allocated with exactly `len(ids)` elements. Each goroutine wrote a different index. Nobody wrote the slice header, so there was no shared read-modify-write and no race. The output is in input order because position 0 is the result for `ids[0]`, position 1 for `ids[1]`, and so on.

**Why it matters:** This is the core fix. It solves two problems at once: the data race and the ordering. There is no mutex around the result write and no `append`.

**The memory-model reason:** each goroutine writes a distinct element, so the writes do not overlap. `wg.Wait()` happens after every `wg.Done()`, and every `Done()` happens after that goroutine's write. So `main` sees all the writes after `Wait`.

**One decision to make:** if an item fails and you skip its index, the zero `Result` stays there. That may be fine if you also return an error; if not, use the per-item error slot from #15.

**If asked:** "Preallocate `len(ids)`, give each goroutine its own index. Distinct elements are separate memory; only the slice header is shared, and nobody writes it. `Wait` gives the happens-before edge."

---

## 9. Append under a mutex works but loses order

```go
package main

import (
	"fmt"
	"sync"
)

type Result struct{ ID int }

func main() {
	ids := []int{1, 2, 3, 4, 5, 6, 7, 8}
	var wg sync.WaitGroup

	// A: append under a mutex — race-free, but order is "who finished first".
	var mu sync.Mutex
	var appended []Result
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			appended = append(appended, Result{ID: id})
		}()
	}
	wg.Wait()

	// B: write each result to its own index — race-free and ordered.
	indexed := make([]Result, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			indexed[i] = Result{ID: id}
		}()
	}
	wg.Wait()

	fmt.Print("append+mutex: ")
	for _, r := range appended {
		fmt.Print(r.ID, " ")
	}
	fmt.Println()

	fmt.Print("indexed:      ")
	for _, r := range indexed {
		fmt.Print(r.ID, " ")
	}
	fmt.Println()
}
```

**Output (one possible run):**

```text
append+mutex: 8 1 2 3 4 5 6 7
indexed:      1 2 3 4 5 6 7 8
```

**What happened:** Both versions are race-free. The mutex makes the append critical section safe, so all eight results survive. But the append order is the completion order, which is random. The indexed version preserves the input order.

**Why it matters:** A mutex is a valid fix for the race, not for the order. It also serializes every append. At 10k req/s the lock is held only for the append, so it may not dominate, but it is still contention you do not need. The indexed write is lock-free and ordered.

**Variant:** If you need the result of a map lookup later, an indexed slice plus a `map[int]int` from id to position also works. That is two structures, but it keeps order and lookup.

**If asked:** "A mutex makes `append` safe but leaves the order nondeterministic and serializes the append. Index writes are lock-free and ordered."

---

## 10. `map[int]Result`: your answer, and its cost

```go
package main

import (
	"fmt"
	"sort"
	"sync"
)

type Result struct {
	ID    int
	Value string
}

func fetch(id int) Result {
	return Result{ID: id, Value: fmt.Sprintf("v%d", id)}
}

func main() {
	ids := []int{2, 1, 2, 3} // id 2 appears twice
	byID := make(map[int]Result, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := fetch(id)
			mu.Lock()
			byID[id] = r
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	fmt.Println("ids:", ids)
	fmt.Println("map entries:", len(byID), "(duplicate id 2 collapsed)")

	keys := make([]int, 0, len(byID))
	for id := range byID {
		keys = append(keys, id)
	}
	sort.Ints(keys)

	fmt.Print("sorted keys: ")
	for _, id := range keys {
		fmt.Print(id, " ")
	}
	fmt.Println()

	// Without the mutex this is the fatal error from example 2.
}
```

**Output:**

```text
ids: [2 1 2 3]
map entries: 3 (duplicate id 2 collapsed)
sorted keys: 1 2 3 
```

**What happened:** The map accepted four writes, but id 2 appeared twice, so the final map has three entries. The mutex made the writes safe; without it, the process dies with `fatal error: concurrent map writes` (#2). The map has no order; the program sorts the keys just to print them.

**Why it matters:** This is the fair evaluation of your "return a map" answer.

| Property | Indexed slice | Map + mutex |
|---|---|---|
| Correct with concurrent writes | yes, no lock | yes, with a lock |
| Input order | preserved | lost |
| Duplicate ids | one result per occurrence | last write wins; duplicates collapse |
| Lookup by id | scan, or a second index map | O(1) |
| Lookup by position | O(1) | no |
| Memory | contiguous, one `Result` per slot | buckets, keys, values, overflow |
| Zero value on failure | visible at the position | missing key, or zero value |

**Verdict:** A map is not wrong. It is the right shape when the caller looks up results by id, ids are unique (or duplicates are meant to collapse), and order does not matter. For `Process(ids []int)`, the caller already knows the input order. An indexed slice is cheaper, ordered, and preserves one result per input occurrence. If you want dedupe, do it explicitly before the fetch (#19), not as a side effect of the map.

**If asked:** "A map fixes 'which id?' but adds a lock, loses order, and collapses duplicate ids. For a batch keyed by position, an indexed slice is cheaper and ordered. I would return a slice and an error, with one error per item if the caller needs the full report."

---

## 11. Wrap the error so the caller can classify it

```go
package main

import (
	"errors"
	"fmt"
)

var ErrNotFound = errors.New("not found")

type FetchError struct {
	ID  int
	Err error
}

func (e *FetchError) Error() string { return fmt.Sprintf("fetch %d: %v", e.ID, e.Err) }
func (e *FetchError) Unwrap() error { return e.Err }

func fetch(id int) (string, error) {
	if id == 3 {
		return "", &FetchError{ID: id, Err: ErrNotFound}
	}
	return fmt.Sprintf("v%d", id), nil
}

func main() {
	_, err := fetch(3)
	wrapped := fmt.Errorf("process: %w", err)

	fmt.Println(wrapped)
	fmt.Println("errors.Is(err, ErrNotFound):", errors.Is(wrapped, ErrNotFound))

	var fe *FetchError
	if errors.As(wrapped, &fe) {
		fmt.Println("errors.As found id:", fe.ID)
	}
}
```

**Output:**

```text
process: fetch 3: not found
errors.Is(err, ErrNotFound): true
errors.As found id: 3
```

**What happened:** `FetchError` wraps the sentinel `ErrNotFound`. `fmt.Errorf` with `%w` wraps the `FetchError` again. `errors.Is` walks the chain to the sentinel. `errors.As` walks the chain to the typed error and extracts the id.

**Why it matters:** The proposed answer uses `fmt.Errorf("fetch %d: %w", id, err)`. The id gives the caller the failing input, and `%w` keeps the chain alive. With `%v` the message would look the same, but `errors.Is` and `errors.As` would stop working. The caller would be left matching strings.

**Rules:**

- Use `%w` to preserve the chain; use `%v` only when you deliberately want to flatten it.
- Include the operation and the id: `fetch 42`, not just `error`.
- Put the sentinel or the typed error at the bottom; wrap at each layer with context.
- Do not compare error strings with `==`. Use `errors.Is` for sentinels and `errors.As` for types.
- A typed error with an `Unwrap` method is how you attach structured data like the id without losing `Is`.

**If asked:** "Wrap with `%w` and the id. Then the caller can use `errors.Is` or `errors.As`, and the log still says which id failed."

---

## 12. `errgroup`: the first error cancels the siblings

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

var errBoom = errors.New("boom")

func main() {
	g, ctx := errgroup.WithContext(context.Background())

	g.Go(func() error {
		time.Sleep(20 * time.Millisecond)
		return fmt.Errorf("fetch 42: %w", errBoom)
	})

	g.Go(func() error {
		<-ctx.Done()
		fmt.Println("sibling saw:", ctx.Err())
		return ctx.Err()
	})

	err := g.Wait()
	fmt.Println("Wait returned:", err)
	fmt.Println("errors.Is(err, errBoom):", errors.Is(err, errBoom))
}
```

**Output:**

```text
sibling saw: context canceled
Wait returned: fetch 42: boom
errors.Is(err, errBoom): true
```

**What happened:** The first goroutine returned an error. `errgroup` recorded it and canceled the derived context. The second goroutine saw `Done()`, printed the reason, and returned `context.Canceled`. `Wait` kept the first error, not the cancellation, and `errors.Is` found the wrapped sentinel.

**Why it matters:** This is the error path of the proposed answer. Without the shared context, the sibling would keep working after the first failure, burning downstream capacity for results the caller will never use. `Wait` returns one error, which is what the handler returns to its caller.

**Important details:**

- `Wait` waits for every goroutine, even after cancellation. It does not return early. A sibling that ignores `ctx` delays the whole call.
- "First error" means the first non-nil error to be recorded, not the first one in input order.
- `Wait` cancels the derived context when it returns, success or failure. Do not hand that context to work that outlives the group.
- Cancellation is cooperative. `fetch` must select on `ctx.Done()` or pass the context to its I/O call.

**If asked:** "`errgroup.WithContext` gives a group and a derived context. The first non-nil error cancels the context, `Wait` waits for all goroutines, and `Wait` returns that first error."

---

## 13. `SetLimit`: cap in-flight work

```go
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

func main() {
	var inFlight, maxInFlight atomic.Int32
	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(4)

	start := time.Now()
	for i := 0; i < 20; i++ {
		g.Go(func() error {
			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			inFlight.Add(-1)
			return nil
		})
	}
	_ = g.Wait()

	fmt.Println("max in-flight:", maxInFlight.Load())
	fmt.Println("elapsed:", time.Since(start).Round(10*time.Millisecond))
}
```

**Output (one possible run):**

```text
max in-flight: 4
elapsed: 50ms
```

**What happened:** `SetLimit(4)` put a four-slot semaphore inside the group. The first four `g.Go` calls started goroutines. The fifth `g.Go` blocked until a slot was free. Twenty jobs at four at a time is five waves; each wave slept 10ms, so the total was about 50ms.

**Why it matters:** This is the one-line bound in the proposed answer. It stops the 1M-goroutine problem without a separate worker-pool implementation. The live goroutine count is capped at four (or 64 in the real answer), so the stack and scheduler cost are bounded.

**Sizing:** use the downstream pool size, the rate limit, or the measured saturation point. `64` is a reasonable start for a database-backed service, but it is not universal. Too low and the request is slow; too high and the database queues.

**Variants:**

- `g.TryGo(fn)` starts the goroutine only if a slot is free. It returns `false` instead of blocking. Use it when you would rather reject work than queue it.
- `g.SetLimit(0)` prevents any new goroutines; `g.Go` blocks forever. Do not use it as "unlimited".
- A negative limit means unlimited.
- `g.SetLimit` must be called before any goroutine is active, and must not be changed while any are active. Example 14 shows the panic.

**If asked:** "`SetLimit(64)` means at most 64 goroutines run; the 65th `g.Go` blocks the producer. It is a semaphore inside `errgroup`. Size it to the downstream, not to `len(ids)`."

---

## 14. Three `SetLimit` traps

`SetLimit` is a blocking semaphore, not a queue with a timeout. These are the cases that bite in production.

### Trap A — never call `g.Go` from inside a limited `g.Go`

```go
package main

import (
	"context"

	"golang.org/x/sync/errgroup"
)

func main() {
	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(1)

	g.Go(func() error {
		// The outer goroutine holds the only slot. The inner g.Go
		// blocks forever waiting for a slot that cannot be released
		// until the outer function returns.
		g.Go(func() error { return nil })
		return nil
	})

	_ = g.Wait()
}
```

**Expected output (first line):**

```text
fatal error: all goroutines are asleep - deadlock!
```

The runtime prints the stack and exits. This is a real deadlock, not a slow call.

**What happened:** The outer function holds the only permit. The inner `g.Go` sends to the semaphore channel and blocks. The outer function cannot return until the inner call returns, and `Wait` cannot finish until the outer function returns. Every goroutine is asleep, so the runtime reports it.

**Rule:** spawn all work from the parent loop. If a task needs to spawn sub-tasks, use a separate group, or collect the sub-tasks and spawn them after the parent returns. Do not nest `g.Go` on the same limited group.

### Trap B — a blocked `g.Go` does not watch `ctx`

```go
package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(1)

	g.Go(func() error {
		time.Sleep(time.Hour) // a fetch that ignores cancellation
		return nil
	})

	cancel() // the caller has given up

	fmt.Println("TryGo started:", g.TryGo(func() error { return nil }))

	second := make(chan struct{})
	go func() {
		g.Go(func() error { return nil })
		close(second)
	}()

	select {
	case <-second:
		fmt.Println("second g.Go returned")
	case <-time.After(100 * time.Millisecond):
		fmt.Println("second g.Go is still blocked after cancel")
	}
}
```

**Output:**

```text
TryGo started: false
second g.Go is still blocked after cancel
```

**What happened:** The only slot is held by a fetch that ignores `ctx`. `cancel()` set the context, but `g.Go` is blocked on the semaphore channel, and that send does not select on `ctx.Done()`. It stays blocked. `TryGo` returns `false` immediately because the slot is full. The program exits after the 100ms timeout, killing the sleeping goroutine.

**Why it matters:** At 10k req/s, a handler can be stuck in `g.Go` after the client disconnected, waiting for a downstream call that will never return. The request goroutine leaks until a slot frees. The fix has two parts: make `fetch` observe `ctx`, and if you need a cancellable acquire, use your own semaphore:

```go
sem := make(chan struct{}, limit)
for _, id := range ids {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}

	g.Go(func() error {
		defer func() { <-sem }()
		// ... fetch ...
		return nil
	})
}
```

Do not combine this manual semaphore with `g.SetLimit`; pick one.

### Trap C — changing the limit while work is active panics

```go
g.SetLimit(1)
g.Go(func() error {
	time.Sleep(time.Second)
	return nil
})
g.SetLimit(10) // panic
```

**Expected panic:**

```text
panic: errgroup: modify limit while 1 goroutines in the group are still active
```

**Rule:** configure the limit once, before the first `g.Go`. If the limit has to change, wait for the group to finish and start a new one.

**If asked:** "`SetLimit` is a blocking semaphore, not a timed queue. Don't nest `g.Go`, don't change the limit while work is active, and don't expect the blocked producer to notice `ctx`. Make the work cancellable, or acquire your own permit with a `select` on `ctx.Done()`."

---

## 15. Fail-fast or collect-all? Pick one

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
)

type Result struct {
	ID    int
	Value string
}

var ErrNotFound = errors.New("not found")

func fetch(id int) (Result, error) {
	if id == 3 || id == 5 {
		return Result{}, fmt.Errorf("id %d: %w", id, ErrNotFound)
	}
	return Result{ID: id, Value: fmt.Sprintf("v%d", id)}, nil
}

func main() {
	ids := []int{1, 2, 3, 4, 5}

	// A: fail-fast — the first error cancels the group and no results are returned.
	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(2)
	for _, id := range ids {
		g.Go(func() error {
			_, err := fetch(id)
			return err
		})
	}
	fmt.Println("fail-fast:", g.Wait())

	// B: collect all — each item writes its own slot and returns nil, so the
	// group is never canceled and every item is attempted.
	out := make([]Result, len(ids))
	errs := make([]error, len(ids))

	g2, _ := errgroup.WithContext(context.Background())
	g2.SetLimit(2)
	for i, id := range ids {
		g2.Go(func() error {
			r, err := fetch(id)
			if err != nil {
				errs[i] = err
				return nil // do not cancel the group
			}
			out[i] = r
			return nil
		})
	}
	_ = g2.Wait()

	joined := errors.Join(errs...)
	for i, r := range out {
		fmt.Printf("index %d: id=%d value=%q err=%v\n", i, r.ID, r.Value, errs[i])
	}
	fmt.Println("joined:")
	fmt.Println(joined)
	fmt.Println("errors.Is(joined, ErrNotFound):", errors.Is(joined, ErrNotFound))
}
```

**Output (one possible run for the fail-fast line; the rest is deterministic):**

```text
fail-fast: id 3: not found
index 0: id=1 value="v1" err=<nil>
index 1: id=2 value="v2" err=<nil>
index 2: id=0 value="" err=id 3: not found
index 3: id=4 value="v4" err=<nil>
index 4: id=0 value="" err=id 5: not found
joined:
id 3: not found
id 5: not found
errors.Is(joined, ErrNotFound): true
```

**What happened:** The fail-fast group returned the first error and canceled the rest. The collect-all group had every goroutine return `nil`, so no cancellation happened. Each item wrote its own `out[i]` and `errs[i]` slot. `errors.Join` combined the non-nil errors into one multi-error. `errors.Is` still finds the sentinel inside the join.

**Why it matters:** The proposed answer chooses fail-fast: `if err := g.Wait(); err != nil { return nil, err }`. That is simple and correct when one dependency failure means the whole batch is invalid. It is the wrong choice when the caller needs a per-item report: "these 998 succeeded, these 2 ids are invalid."

**Choose by semantics, not habit:**

| Policy | Use when | Cost |
|---|---|---|
| Fail-fast, one error | A shared dependency failed; retrying the batch is reasonable. | Discards successful work; the caller does not know which items succeeded. |
| Per-item result + error | The caller needs a report; invalid items are independent. | More memory; no cancellation, so slow items still run to completion. |
| Partial results + one error | The caller can use the successes and retry the failures. | The contract must say whether the slice is valid on error. |

**Details:**

- `errors.Join` ignores `nil` entries. If every item succeeded, it returns `nil`.
- The joined error prints one line per error; `errors.Is` and `errors.As` traverse the whole join.
- The collect-all group does not cancel on the first error. Add a context with a timeout so a hung item cannot hold the request forever.
- The per-item slot pattern relies on distinct indices, the same memory-model rule as #8.

**If asked:** "Fail-fast for a shared dependency failure; collect-all for per-item validation where the caller wants the whole report. `errors.Join` makes the collect-all error still work with `errors.Is`."

---

## 16. Worker pool: the other bounded design

```go
package main

import (
	"errors"
	"fmt"
	"sync"
)

type Result struct {
	ID    int
	Value string
}

var ErrNotFound = errors.New("not found")

func fetch(id int) (Result, error) {
	if id == 3 {
		return Result{}, fmt.Errorf("id %d: %w", id, ErrNotFound)
	}
	return Result{ID: id, Value: fmt.Sprintf("v%d", id)}, nil
}

func main() {
	ids := []int{1, 2, 3, 4, 5, 6}
	results := make([]Result, len(ids))
	errs := make([]error, len(ids))

	type job struct {
		index int
		id    int
	}

	jobs := make(chan job) // unbuffered: the producer blocks when workers are busy
	var wg sync.WaitGroup

	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				r, err := fetch(j.id)
				if err != nil {
					errs[j.index] = err
					continue
				}
				results[j.index] = r
			}
		}()
	}

	for i, id := range ids {
		jobs <- job{index: i, id: id}
	}
	close(jobs)
	wg.Wait()

	for i := range ids {
		fmt.Printf("%d: id=%d value=%q err=%v\n", i, results[i].ID, results[i].Value, errs[i])
	}
}
```

**Output:**

```text
0: id=1 value="v1" err=<nil>
1: id=2 value="v2" err=<nil>
2: id=0 value="" err=id 3: not found
3: id=4 value="v4" err=<nil>
4: id=5 value="v5" err=<nil>
5: id=6 value="v6" err=<nil>
```

**What happened:** Three fixed workers read from one jobs channel. The producer sent six jobs; the unbuffered channel made it wait whenever all workers were busy. The workers wrote by index, so the output stayed in input order. The error went into `errs[j.index]`; the zero `Result` stayed at that position.

**Why it matters:** This is the alternative to `errgroup.SetLimit`. Both bound concurrency; the tradeoff is in how they do it.

| | `errgroup.SetLimit(n)` | Worker pool |
|---|---|---|
| Code | less | more |
| Goroutines | one per job, at most `n` live | exactly `n`, reused |
| Queue | none; `g.Go` blocks the producer | jobs channel; you choose buffered or not |
| Per-worker state | no | yes, one per worker |
| Shutdown | `ctx` + group | close the jobs channel and `ctx` |
| 1M jobs | 1M goroutine creations over time | `n` goroutines total |

**Add cancellation:** the worker loop should stop when the request is canceled, and the producer should not block forever on a canceled job.

```go
for {
	select {
	case <-ctx.Done():
		return
	case j, ok := <-jobs:
		if !ok {
			return
		}
		r, err := fetch(ctx, j.id)
		// ... store result or error ...
	}
}
```

```go
defer close(jobs)
for i, id := range ids {
	select {
	case jobs <- job{index: i, id: id}:
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

**Order to say aloud:** workers `range` jobs, one producer closes the channel, wait for the workers, then read the results. A single producer should `defer close(jobs)` so an early `ctx` return still closes it. If there are multiple producers, add a separate closer after a `WaitGroup`.

**If asked:** "A worker pool is fixed workers plus a jobs channel. It reuses goroutines and gives you a real queue; `SetLimit` is the smaller `errgroup` version. For a million items I would use the pool with batching."

---

# Part 3 — A million ids and 10k req/s

## 17. Batch with a checkpoint you can resume from

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
)

type Result struct{ ID int }

var ErrBadID = errors.New("bad id")

func fetch(ctx context.Context, id int, failID int) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if id == failID {
		return Result{}, fmt.Errorf("id %d: %w", id, ErrBadID)
	}
	return Result{ID: id}, nil
}

func processBatch(ctx context.Context, ids []int, start, size, limit, failID int) (int, error) {
	end := min(start+size, len(ids))
	batch := ids[start:end]

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	for i, id := range batch {
		index := start + i
		g.Go(func() error {
			_, err := fetch(gctx, id, failID)
			if err != nil {
				return fmt.Errorf("index %d: %w", index, err)
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return start, err // checkpoint stays at the start of the failed batch
	}
	return end, nil
}

func main() {
	ids := make([]int, 23)
	for i := range ids {
		ids[i] = i + 1
	}

	offset := 0
	failID := 13

	for offset < len(ids) {
		next, err := processBatch(context.Background(), ids, offset, 5, 3, failID)
		if err != nil {
			fmt.Printf("batch [%d:%d] failed: %v\n", offset, min(offset+5, len(ids)), err)
			fmt.Println("resume from checkpoint", offset)
			failID = -1
			continue
		}
		offset = next
		fmt.Println("checkpoint", offset)
	}

	fmt.Println("done:", offset, "of", len(ids))
}
```

**Output:**

```text
checkpoint 5
checkpoint 10
batch [10:15] failed: index 12: id 13: bad id
resume from checkpoint 10
checkpoint 15
checkpoint 20
checkpoint 23
done: 23 of 23
```

**What happened:** The program processed the ids in batches of five. After each successful batch it advanced the offset. When id 13 failed, the checkpoint stayed at 10, the start of the failed batch. The retry re-ran the whole `[10:15]` batch and then continued.

**Why it matters:** This is the concrete version of your "batch and resume from a point" answer. A million ids in one `Process` call is not just slow; a single failure at item 900,000 throws away all the work, and a restart begins at zero. A checkpoint makes progress durable.

**The ordering rule:** write the batch's effects first, then advance the checkpoint. If you crash after the write and before the checkpoint, the retry repeats the batch, so the write must be idempotent (upsert, `ON CONFLICT DO NOTHING`, or a deterministic key). If you advance the checkpoint before the write, a crash loses the batch. Idempotency is what makes the duplicate window safe.

**Durable vs in-memory:** the `offset` here lives in memory, so it survives a failed batch inside one process, not a process crash. For a real job, store the checkpoint in the database or the queue; otherwise a restart begins at zero.

**A permanent failure needs a cap:** the example resets `failID = -1` only to demonstrate the resume. In production, a permanent error (bad id, schema mismatch) would make this loop retry forever. Count attempts, back off, and after a limit mark the batch failed or send it to a dead-letter queue.

**Sizing the batch:** tie it to the request deadline and the downstream. Smaller batches checkpoint more often and lose less work on failure; larger batches amortize overhead. For a background job, a batch of a few hundred to a few thousand is common. Measure.

**If asked:** "Batch with a checkpoint. Write idempotently, then advance the checkpoint; on failure resume from the last committed batch. A crash between write and checkpoint causes a duplicate, so the write must be idempotent."

---

## 18. Stream results to a sink instead of holding 1M

```go
package main

import (
	"context"
	"fmt"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

type Result struct {
	ID    int
	Value string
}

func fetch(ctx context.Context, id int) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	return Result{ID: id, Value: fmt.Sprintf("v%d", id)}, nil
}

func processEach(ctx context.Context, ids []int, limit int, sink func(Result) error) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for _, id := range ids {
		g.Go(func() error {
			r, err := fetch(gctx, id)
			if err != nil {
				return fmt.Errorf("fetch %d: %w", id, err)
			}
			if err := gctx.Err(); err != nil {
				return err
			}
			return sink(r) // hand the result off; do not keep a 1M slice
		})
	}

	return g.Wait()
}

func main() {
	ids := make([]int, 1000)
	for i := range ids {
		ids[i] = i + 1
	}

	var count atomic.Int64
	err := processEach(context.Background(), ids, 8, func(r Result) error {
		count.Add(1)
		return nil
	})

	fmt.Println("streamed:", count.Load())
	fmt.Println("held in memory: one Result per in-flight worker, not 1000")
	fmt.Println("err:", err)
}
```

**Output:**

```text
streamed: 1000
held in memory: one Result per in-flight worker, not 1000
err: <nil>
```

**What happened:** Each worker fetched one result and passed it to the sink immediately. The program never allocated a `[]Result` with 1000 entries. At most eight results were alive at once (the limit), plus the sink's own state.

**Why it matters:** A million ids is 8 MB just for the `[]int` on a 64-bit machine. A million `Result` values of this small shape is another 24 MB before any strings. That is not catastrophic on its own, but at 10k req/s, per-request arrays multiply, and the latency of collecting everything before responding is worse than the memory. Streaming keeps the working set proportional to concurrency, not to input size.

**The contract:** the sink is called from multiple goroutines. It must be safe for concurrent use. A database pool or an atomic counter is; a single `bytes.Buffer`, a plain slice append, or one file writer is not. Guard those with a mutex, or use a single collector goroutine and a channel.

**Backpressure:** a slow sink blocks the worker, which keeps its slot, which eventually blocks `g.Go`, which slows the producer. That is the behavior you want; it does not grow an unbounded queue in memory.

**When not to stream:** if the API must return the full array in one response, streaming does not fit. Then batch and paginate, or return a job id and let the client poll (#22).

**If asked:** "If the caller does not need all results at once, stream each result to a sink. Keep only the in-flight window in memory; make the sink concurrency-safe or use one collector."

---

## 19. Dedupe before fan-out

```go
package main

import "fmt"

func dedupe(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func main() {
	ids := []int{5, 3, 5, 1, 3, 5}
	unique := dedupe(ids)

	fmt.Println("input: ", ids)
	fmt.Println("unique:", unique)
	fmt.Println("saved:", len(ids)-len(unique), "fetches")
}
```

**Output:**

```text
input:  [5 3 5 1 3 5]
unique: [5 3 1]
saved: 3 fetches
```

**What happened:** The set remembered the ids already seen. The output keeps the first-seen order. Three duplicate fetches disappeared.

**Why it matters:** Duplicate ids are common when a client retries, when two streams are merged, or when the same entity appears in multiple batches. Fetching the same id twice doubles the downstream load and can produce two different results. Dedupe before the fan-out when the ids represent a set.

**Two semantics, choose deliberately:**

- **Ids are a set:** dedupe, then fetch once per id. The output has one result per unique id. This is what the example does.
- **Every occurrence needs a result:** do not dedupe the output. Use a cache keyed by id so duplicate occurrences share one fetch but still write a result for each position.

**The in-place trap:** a common variant is `out := ids[:0]` to reuse the caller's array. It works, but it overwrites the input slice. If the caller still needs `ids`, use a new slice as shown. If you own the input and want to save an allocation, the in-place version is fine; say so in the comment.

**Relationship to the map answer:** `map[int]Result` implicitly collapses duplicates (#10). If that is not the intent, the loss is silent. Dedupe explicitly, or keep the slice and a cache.

**If asked:** "If ids are a set, dedupe before fetching; it saves downstream calls and makes the result count predictable. If each occurrence matters, cache the fetch by id but keep one output per position."

---

## 20. Empty input: nil vs empty slice

```go
package main

import (
	"encoding/json"
	"fmt"
)

type Result struct{ ID int }

func process(ids []int) []Result {
	results := make([]Result, len(ids))
	for i, id := range ids {
		results[i] = Result{ID: id}
	}
	return results
}

func main() {
	for _, ids := range [][]int{nil, {}} {
		r := process(ids)
		b, _ := json.Marshal(r)
		fmt.Printf("input=%v len=%d nil_slice=%v json=%s\n", ids, len(r), r == nil, b)
	}

	var nilResults []Result
	b, _ := json.Marshal(nilResults)
	fmt.Printf("var results []Result -> nil_slice=%v json=%s\n", nilResults == nil, b)
}
```

**Output:**

```text
input=[] len=0 nil_slice=false json=[]
input=[] len=0 nil_slice=false json=[]
var results []Result -> nil_slice=true json=null
```

**What happened:** `make([]Result, 0)` is non-nil, so it marshals to `[]`. A nil slice marshals to `null`. `len` and `range` treat both as empty, and `append` works on both. The difference appears at `== nil`, reflection, and JSON.

**Why it matters:** The proposed answer uses `make([]Result, len(ids))`, so an empty or nil input returns an empty non-nil slice and JSON `[]`. That is usually the better API default: clients can iterate without a null check. If the contract distinguishes "no ids were supplied" from "processing has not happened", use an explicit status field, not nil-vs-empty.

**Rules:**

- `len(nil) == 0`; `range nil` runs zero times; `append(nil, x)` works.
- `make([]T, 0)` is non-nil; `var s []T` is nil.
- JSON: empty non-nil → `[]`, nil → `null`.
- Pick one and test it. Clients in typed languages often treat `null` and `[]` differently.

**If asked:** "`make([]Result, 0)` is non-nil and marshals to `[]`; a nil slice marshals to `null`. I pick `[]` for list responses and test it."

---

# Part 4 — Put it together

## 21. The full corrected `Process`, with tests

This is the proposed answer, verbatim, plus a fake `fetch` so it runs.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

type Result struct {
	ID    int
	Value string
}

var ErrNotFound = errors.New("not found")

type Server struct {
	failID int
}

func (s *Server) fetch(ctx context.Context, id int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-time.After(2 * time.Millisecond):
	}
	if id == s.failID {
		return Result{}, fmt.Errorf("id %d: %w", id, ErrNotFound)
	}
	return Result{ID: id, Value: fmt.Sprintf("v%d", id)}, nil
}

// Process is the corrected version from the interview answer.
func (s *Server) Process(ctx context.Context, ids []int) ([]Result, error) {
	results := make([]Result, len(ids))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(64) // g.Go blocks when the limit is hit, so no 1M goroutines

	for i, id := range ids {
		g.Go(func() error { // Go 1.22+: i, id are per-iteration
			r, err := s.fetch(ctx, id)
			if err != nil {
				return fmt.Errorf("fetch %d: %w", id, err)
			}
			results[i] = r // distinct index, no race
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}

func main() {
	s := &Server{failID: 3}

	results, err := s.Process(context.Background(), []int{10, 20, 30})
	fmt.Println("ok:", len(results), err)
	for _, r := range results {
		fmt.Printf("  %d=%s\n", r.ID, r.Value)
	}

	results, err = s.Process(context.Background(), []int{1, 2, 3, 4})
	fmt.Println("fail-fast:", results == nil, err)
	fmt.Println("errors.Is:", errors.Is(err, ErrNotFound))
}
```

**Output:**

```text
ok: 3 <nil>
  10=v10
  20=v20
  30=v30
fail-fast: true fetch 3: id 3: not found
errors.Is: true
```

**The tests:** save this as `process_test.go` beside the main file, then run `go test`.

```go
package main

import (
	"context"
	"errors"
	"testing"
)

func TestProcessKeepsInputOrder(t *testing.T) {
	s := &Server{failID: -1}
	ids := []int{3, 1, 2}

	got, err := s.Process(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ids) {
		t.Fatalf("len = %d, want %d", len(got), len(ids))
	}
	for i, id := range ids {
		if got[i].ID != id {
			t.Fatalf("position %d: got id %d, want %d", i, got[i].ID, id)
		}
	}
}

func TestProcessEmpty(t *testing.T) {
	s := &Server{failID: -1}

	got, err := s.Process(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestProcessReturnsError(t *testing.T) {
	s := &Server{failID: 2}

	got, err := s.Process(context.Background(), []int{1, 2, 3})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got != nil {
		t.Fatalf("results = %v, want nil", got)
	}
}

func TestProcessCanceled(t *testing.T) {
	s := &Server{failID: -1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := s.Process(ctx, []int{1, 2, 3})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
```

**Test output:**

```text
=== RUN   TestProcessKeepsInputOrder
--- PASS: TestProcessKeepsInputOrder (0.00s)
=== RUN   TestProcessEmpty
--- PASS: TestProcessEmpty (0.00s)
=== RUN   TestProcessReturnsError
--- PASS: TestProcessReturnsError (0.00s)
=== RUN   TestProcessCanceled
--- PASS: TestProcessCanceled (0.00s)
PASS
ok  	mock1	0.752s
```

Your module name will be whatever you chose in `go mod init`, and the timing varies; the PASS lines are what matter.

**What the fix gets right:**

- `ctx` is the first parameter and reaches `fetch`.
- The result slice is preallocated and written by index; the race and the order are fixed.
- `errgroup.WithContext` cancels the siblings on the first error.
- `SetLimit(64)` bounds the live goroutines, so a 1M-id input does not create 1M live goroutines.
- The error is wrapped with the id and `%w`, so `errors.Is` works.
- The empty input returns a non-nil empty slice.

**What it still leaves out:** know these before the interviewer finds them.

| Gap | Consequence | Where to go |
|---|---|---|
| `SetLimit` blocks but does not watch `ctx` | If every in-flight call ignores cancellation, the producer can stay blocked after the client leaves. | #14 trap B |
| `64` is a fixed guess | Too low wastes capacity; too high saturates the database. | #13 |
| Fail-fast returns `nil` | Successful results are discarded; the caller cannot retry only the failed ids. | #15 |
| One goroutine creation per id | Live goroutines are bounded, but 1M jobs still create 1M goroutines over time. | #16, #17 |
| The full `[]Result` is kept | Memory scales with input, not concurrency. | #18 |
| Duplicate ids are fetched twice | Wasted downstream calls; order still maps to position, so duplicates are visible. | #19 |
| A panic in `fetch` kills the process | `errgroup` does not recover panics. | note below |
| No checkpoint or queue | A 1M-id request still runs inline and cannot resume. | #17, #22 |

**Panic note:** `errgroup` deliberately does not recover panics. A panic in any `g.Go` function propagates and crashes the process, which is usually the right default: a broken invariant should be loud, and `recover` in a random worker hides it. If a specific dependency can panic on bad input, recover inside that call, convert it to an error with a stack log, and let the group handle the error. Do not add a blanket `recover` to every worker.

**Race note:** run `go test -race ./...` on Linux, WSL, or CI. On this Windows box the command fails with the `-race requires cgo` message at the top of this file. A passing race run only means the detector did not observe a race in that run; it is not a proof. The distinct-index argument is the proof for #8.

**If asked:** "This is the shape: context first, preallocated slice by index, `errgroup` with a limit, a wrapped error, and no shared append. For a million ids I would add batching, dedupe, and a queue."

---

## 22. A 1M-id request is a job, not an HTTP handler

```go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
)

type Queue struct {
	jobs chan []int
}

func (q *Queue) Enqueue(ids []int) bool {
	select {
	case q.jobs <- ids:
		return true
	default:
		return false
	}
}

type handler struct{ q *Queue }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *handler) process(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids required"})
		return
	}
	if !h.q.Enqueue(req.IDs) {
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "queue full"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "queued",
		"job_id": "job-123", // in real code: a generated UUID
	})
}

func main() {
	q := &Queue{jobs: make(chan []int, 2)}
	h := &handler{q: q}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/process", strings.NewReader(`{"ids":[1,2,3]}`))
	h.process(rec, req)

	fmt.Println("status:", rec.Code)
	fmt.Println("body:", strings.TrimSpace(rec.Body.String()))
	fmt.Println("queued jobs:", len(q.jobs))
}
```

**Output:**

```text
status: 202
body: {"job_id":"job-123","status":"queued"}
queued jobs: 1
```

**What happened:** The handler decoded the request, put the id list on a bounded channel, and returned `202 Accepted` with a job id. A worker (not shown) would consume the job and run the batched `Process` from #17.

**Why it matters:** At 10k req/s, a handler has a latency budget measured in milliseconds. A million-id fetch loop is seconds to minutes. If the handler does it inline, the client times out, retries, and creates another million-id job. The queue absorbs the spike; the worker drains it.

**The production shape:**

- **Bounded queue:** if `Enqueue` can grow without bound, you moved the memory problem from the slice to the queue. Reject with `503` and `Retry-After` when full.
- **Durable queue:** a channel is not durable. If the job must survive a process crash, use a broker or a database-backed queue with an ack, not an in-memory channel.
- **Idempotency key:** a client retry should not create a second identical job. Store the key with the job and return the same job id.
- **Status endpoint:** `202` means accepted, not done. Expose `GET /jobs/{id}` with `queued|running|succeeded|failed`, a progress count, and the error or result location.
- **Checkpoint:** the worker uses #17's offset; a crash resumes, not restarts.
- **Dedupe:** the worker dedupes each job's ids before fetching (#19).
- **Backpressure:** the queue size and the worker concurrency are both limits. Size them to the downstream, not to the arrival rate.
- **Small requests:** if the id list is small and fits the deadline, inline is fine. The threshold is the latency budget and the downstream capacity, not a fixed count.

**If asked:** "At 10k req/s I would not process a million ids inline. I would accept with `202`, enqueue a bounded job with an idempotency key, process it in batches with a checkpoint, and expose a status endpoint."

---

# Final Q&A recall

Cover the right column with your hand. Say the answer aloud. Then check.

| # | Question | One-line answer |
|---:|---|---|
| 1 | First bug in the snippet? | Data race on the shared `results` slice via concurrent `append`. |
| 2 | Why is `append` racy? | It is a read-modify-write of the slice header plus a write to the backing array. |
| 3 | Does preallocating capacity fix it? | No. It removes growth, not the shared header race. |
| 4 | Best fix for ordered results? | `make([]Result, len(ids))` and write `results[i]`. |
| 5 | Is a mutex around append correct? | Yes, but order becomes completion order and the append serializes. |
| 6 | Why not `map[int]Result`? | It needs a lock, loses order, collapses duplicate ids, and costs more per entry. |
| 7 | When is a map the right shape? | When the caller looks up by id, ids are unique, and order does not matter. |
| 8 | What is wrong with one goroutine per id? | O(n) stacks and scheduler work; 100k ≈ 781 MB here, 1M ≈ 8 GB. |
| 9 | How do you bound it? | `errgroup.SetLimit`, a semaphore, or a worker pool. |
| 10 | What does `SetLimit` do? | Blocks `g.Go` when the limit is reached; a semaphore inside the group. |
| 11 | Does a blocked `g.Go` watch `ctx`? | No. The semaphore send does not select on `Done()`. |
| 12 | Can you nest `g.Go`? | Not on a limited group; the inner call can deadlock the outer. |
| 13 | What does the first error do? | Cancels the derived context; `Wait` returns that error after all goroutines finish. |
| 14 | Does `Wait` return early? | No. A sibling that ignores `ctx` delays the whole call. |
| 15 | Fail-fast or collect-all? | Business choice: shared dependency → fail-fast; per-item report → collect-all. |
| 16 | How do you join errors? | `errors.Join(errs...)`; `errors.Is/As` still traverse it. |
| 17 | Why wrap with `%w`? | Preserves the chain for `errors.Is/As`; include the id. |
| 18 | Why pass `ctx` into `fetch`? | Cancellation and deadlines must reach the I/O call; it is cooperative. |
| 19 | What does a 1M batch need? | Checkpoint, idempotent writes, dedupe, bounded concurrency, and a queue off the request path. |
| 20 | Where does the checkpoint go? | Advance only after the batch is durably written; a crash between write and checkpoint produces a duplicate, so writes must be idempotent. |
| 21 | Duplicate ids? | Dedupe if ids are a set; cache the fetch if each occurrence still needs a result. |
| 22 | Empty input: nil or empty slice? | `make([]Result, 0)` marshals to `[]`; nil marshals to `null`. Pick a contract. |
| 23 | Does `errgroup` recover panics? | No. A panic in a worker crashes the process. |
| 24 | Does `-race` prove there are no races? | No. It only reports races it observed in that run. |

---

## 30-second answer template

When the interviewer says "walk me through your fix", use four sentences:

1. **Bound:** "I preallocate `len(ids)` and bound the group with `SetLimit` sized to the downstream."
2. **Stop:** "I pass `ctx` into `fetch`; the first error cancels the group, and every worker checks `Done`."
3. **Protect:** "Each goroutine writes its own index, so there is no shared append and no race."
4. **Scale:** "For a million ids I dedupe, batch with a checkpoint, make the writes idempotent, and move the job to a bounded queue."

That is the whole answer. The details go in the middle, not the opening.

---

## What to do next

- Run the broken programs first. Then rewrite #21 from memory, with the tests, before looking.
- If you have 20 more minutes, rewrite #17 (batch + checkpoint) from memory. That is the part most candidates skip.
- If the interviewer pushes on `SetLimit`, use #14: nested `g.Go`, blocked `g.Go` ignoring `ctx`, and changing the limit while active.
- If the interviewer pushes on the map, use #10: correctness, order, duplicates, memory.
- For the primitives behind these examples, use [`day-2b-concurrency-lab.md`](day-2b-concurrency-lab.md).
- For the worker-pool, retry, and shutdown production shapes, use [`examples/go-concurrency.md`](examples/go-concurrency.md).
- Log the weak spot in [`progress.md`](progress.md) and retest it in two days.

**One line to keep:** preallocate the result, give each goroutine one index, bound the group to the downstream, and move a million ids off the request path.
