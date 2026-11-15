# Day 2b — Concurrency lab: 23 tiny examples

**This is the doing file.** Read less, run more. One program shows one idea. Run it, guess the output, then read the short note. Do not read this file top to bottom in one sitting; open one example, run it, and close the file.

**If a video helps:** watch only the 5–10 minutes that match the next example, then run the example. Do not watch a whole course first.

The file looks long because every example includes the full `package main` and imports. You are not reading it; you are running one block at a time.

**Tested on:** Go 1.27.4, Windows. Use Go 1.22 or newer; the loop-variable example depends on it. Every output below was produced by the code shown, except where it says "one possible run" or "not run in this lab".

**Time:** 50–60 minutes for Part 1, 35–40 minutes for Part 2, 20–25 minutes for Part 3. Split it into two sittings if that is easier.

**If you only have 30 minutes:** run #1, #2, #4, #5, #6, #8, #10, #11, #12, #14, #15, #16, #23. That is the shortest path to the ideas interviewers ask about.

**Theory is separate.** Do not open [`day-2-theory-cupboard.md`](day-2-theory-cupboard.md) before you run the lab. The cupboard is only for "the interviewer went deeper" moments.

> **Schedule note:** This lab is the Sun 15 Nov Day 2 practice. It replaces a second read of the long Day 2 file. The 19/20 Nov interview dates do not move.

| Time | Block |
|---|---|
| 0:00–0:10 | Read the map, pick the core path, open the first program |
| 0:10–0:55 | Part 1: start, stop, channels, select, context |
| 0:55–1:30 | Part 2: mutex, atomic, semaphore, pool, errgroup, pipeline |
| 1:30–1:50 | Part 3: leaks, timers, deadlock, shutdown shape |
| 1:50–2:00 | Final Q&A table, then log the weak spot |

**Stop at 2 hours.** If you finish Part 1 and Part 2, that is enough. Part 3 is the next pass.

---

## How to use this lab

1. Make one folder and one Go module:

   ```bash
   mkdir day2lab
   cd day2lab
   go mod init day2lab
   ```

2. For each example, save the code as its own file, for example `01-main-exit.go`. You do not need 23 files at once: create one, run it, then replace the contents with the next example.
3. Run **one file at a time**:

   ```bash
   go run 01-main-exit.go
   ```

   Do not run `go run .` in this folder. Every file has its own `main`, so the whole folder is not one program.

4. For the `errgroup` example only, add the dependency once:

   ```bash
   go get golang.org/x/sync/errgroup
   ```

5. Say the **If asked** line aloud before moving on. If you cannot say it, reread the note, not the whole file.

---

## The map: every main way to write Day 2 concurrency

| You need to... | Tool | Example |
|---|---|---|
| Start work in the background | `go` | 1 |
| Wait for N workers | `sync.WaitGroup` | 2 |
| Pass a value or hand off work | channel | 4–7 |
| Wait on many events | `select` | 8–9 |
| Cancel work or set a deadline | `context` | 10–11 |
| Protect shared state | `sync.Mutex` / `RWMutex` | 12 |
| Change one counter or flag safely | `sync/atomic` | 13 |
| Initialize exactly once | `sync.Once` | 13 |
| Stop a group on the first error | `errgroup` | 16 |
| Cap in-flight work | semaphore, `errgroup.SetLimit` | 14, 16 |
| Chain stream stages | pipeline | 17 |
| Merge streams | fan-in | 18 |
| Stop a blocked send when the consumer leaves | send + `ctx.Done()` in `select` | 23 |
| Do something periodically | `time.Ticker` | 20 |
| Stop a server on a signal | `signal.NotifyContext` | 22 |
| Find a leak | `runtime.NumGoroutine`, pprof, `goleak` | 19 |
| Find a data race | `go test -race` | 12 |

---

# Part 1 — Start, stop, and pass values

## 1. `main` does not wait for goroutines

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	go func() {
		time.Sleep(20 * time.Millisecond)
		fmt.Println("goroutine: hello")
	}()
	fmt.Println("main: done")
}
```

**Output:**

```text
main: done
```

**What happened:** `main` printed its line and returned. The process exited before the goroutine woke up.

**Why it matters:** `go f()` starts work, but it does not create a parent-child wait. If `main` returns, every goroutine dies. You need a `WaitGroup`, a done channel, or an `errgroup`.

**If asked:** "A goroutine has no automatic wait. When `main` returns, the process exits and all goroutines die."

---

## 2. Wait for workers, and add before `go`

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			fmt.Println("worker", n)
		}(i)
	}
	wg.Wait()
	fmt.Println("all done")
}
```

**Output (one possible run):**

```text
worker 3
worker 1
worker 2
all done
```

The order changes. The set of lines and `all done` do not.

**What happened:** `Add(1)` ran in the parent before each `go`. `Done()` ran when each worker finished. `Wait()` blocked until the counter reached zero.

**Why it matters:** If `Add` runs inside the goroutine, `Wait` can return before the counter goes up. That is a race, so it can look fine one day and lose work the next.

**Rule:** never copy a `WaitGroup` or a mutex after first use; pass a pointer.

**Wrong version, do not copy:**

```go
for i := 0; i < 3; i++ {
	go func() {
		wg.Add(1) // wrong: this races with Wait
		defer wg.Done()
		fmt.Println("work", i)
	}()
}
wg.Wait()
fmt.Println("main continues")
```

**Output (one possible run):**

```text
main continues
work 1
work 0
work 2
```

`Wait` returned before the workers added themselves. It can also panic with `sync: WaitGroup misuse` or lose lines. The output is not stable because this is a race.

**If asked:** "`Add` must happen before `go`, in the parent. `Wait` must see the count before it can reach zero."

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
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println("i =", i)
		}()
	}
	wg.Wait()
}
```

**Output (one possible run):**

```text
i = 1
i = 3
i = 2
```

**What happened:** Since Go 1.22, each loop iteration has its own variable. The old "all goroutines print the last value" bug is gone for this form.

**Why it matters:** In older Go, all three could print `i = 3`. Passing the value as a parameter still works in every version and makes the intent obvious:

```go
go func(n int) {
	defer wg.Done()
	fmt.Println("i =", n)
}(i)
```

**If asked:** "Go 1.22 made loop variables per-iteration. Passing the value as a parameter is still the clearest habit and works on every version."

---

## 4. Unbuffered channel: a handshake

```go
package main

import "fmt"

func main() {
	ch := make(chan string)
	done := make(chan struct{})

	go func() {
		fmt.Println("receiver: ready")
		fmt.Println("received:", <-ch)
		close(done)
	}()

	ch <- "ping"
	<-done
	fmt.Println("sender: done")
}
```

**Output:**

```text
receiver: ready
received: ping
sender: done
```

**What happened:** The sender waited until the receiver was ready. That is a rendezvous.

**Why it matters:** An unbuffered channel gives you backpressure for free. The sender cannot run ahead of the receiver.

**If asked:** "An unbuffered send completes when a receiver takes the value. It is a handshake, not a queue."

---

## 5. Buffered channel: a waiting room with a limit

```go
package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2

	select {
	case ch <- 3:
		fmt.Println("sent 3")
	default:
		fmt.Println("buffer full")
	}

	fmt.Println(<-ch)
	fmt.Println(<-ch)
}
```

**Output:**

```text
buffer full
1
2
```

**What happened:** The buffer holds two values. The third send cannot proceed, so `default` runs.

**Why it matters:** A buffer delays blocking. It does not remove the limit. A full queue should reject work or slow the producer; it should not grow without bound.

**If asked:** "A buffer is bounded. When it is full, a send blocks, or `default` rejects the work."

---

## 6. Close rules in one program

```go
package main

import "fmt"

func main() {
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	close(ch)

	for v := range ch {
		fmt.Println("range:", v)
	}

	v, ok := <-ch
	fmt.Println("after close:", v, ok)

	func() {
		defer func() {
			fmt.Println("recovered:", recover())
		}()
		ch <- 3
	}()
}
```

**Output:**

```text
range: 1
range: 2
after close: 0 false
recovered: send on closed channel
```

**What happened:** `range` drained the buffered values and ended because the channel was closed. A receive after close returned zero and `false`. A send after close panicked; the small function recovered so the output stays clean.

**Why it matters:** The sender closes. Close means "no more values", not "cancel". A receiver that closes can make another sender panic.

**If asked:** "The sender closes. Receive after close gives zero and `false`. Send on closed panics. Close is end-of-stream, not cancellation."

---

## 7. Nil channels and directional channels

```go
package main

import "fmt"

func main() {
	var nilCh chan int
	select {
	case v := <-nilCh:
		fmt.Println("got", v)
	default:
		fmt.Println("nil channel is not ready")
	}

	ch := make(chan int)
	go produce(ch)
	consume(ch)
}

func produce(out chan<- int) {
	for i := 1; i <= 3; i++ {
		out <- i
	}
	close(out)
}

func consume(in <-chan int) {
	for v := range in {
		fmt.Println("got", v)
	}
}
```

**Output:**

```text
nil channel is not ready
got 1
got 2
got 3
```

**What happened:** A nil channel blocks forever, so only `default` was ready. `chan<- int` is send-only; `<-chan int` is receive-only. The compiler enforces the direction.

**Why it matters:** Directional types document who sends and who receives. They stop a function from closing or writing the wrong side. A nil channel is useful in `select` when you want to turn a case off.

**If asked:** "A nil channel blocks forever, so its `select` case is disabled. Directional channels make ownership a compile-time rule."

---

## 8. `select`: ready cases are random

```go
package main

import "fmt"

func main() {
	for i := 0; i < 6; i++ {
		a := make(chan string, 1)
		b := make(chan string, 1)
		a <- "a"
		b <- "b"

		select {
		case v := <-a:
			fmt.Print(v)
		case v := <-b:
			fmt.Print(v)
		}
	}
	fmt.Println()
}
```

**Output (one possible run):**

```text
bbabba
```

**What happened:** Both cases were ready every time. Go picked one at random.

**Why it matters:** There is no priority. Random choice stops one case from starving the others. If you need priority, write it explicitly with nested selects or a state machine.

**If asked:** "If more than one case is ready, Go picks one at random. There is no priority."

---

## 9. Timeout: do not wait forever

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	slow := make(chan string, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		slow <- "result"
	}()

	select {
	case v := <-slow:
		fmt.Println("got", v)
	case <-time.After(20 * time.Millisecond):
		fmt.Println("timeout")
	}
}
```

**Output:**

```text
timeout
```

**What happened:** The worker slept for 100ms. The caller gave up after 20ms. The result channel is buffered, so in a long-running program the worker could still send and exit later.

**Why it matters:** With an unbuffered result channel, that worker would block forever after the timeout. A timeout without a stop path creates a leaked goroutine.

**If asked:** "A timeout stops the caller, not the worker. The worker still needs a buffer or a cancellation path so it can exit."

---

## 10. Context cancel

```go
package main

import (
	"context"
	"fmt"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		<-ctx.Done()
		fmt.Println("worker stopped:", ctx.Err())
		close(done)
	}()

	cancel()
	<-done
	fmt.Println("main done")
}
```

**Output:**

```text
worker stopped: context canceled
main done
```

**What happened:** `cancel()` closed `ctx.Done()`. The worker saw it, printed the reason, and exited.

**Why it matters:** This is how a request stops work when the client disconnects or the caller no longer needs the result.

**If asked:** "`ctx.Done()` returns a channel that closes on cancel or deadline. `ctx.Err()` tells you which one happened."

---

## 11. Context deadline

```go
package main

import (
	"context"
	"fmt"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	<-ctx.Done()

	fmt.Println(ctx.Err())
	fmt.Println("waited enough:", time.Since(start) >= 30*time.Millisecond)
}
```

**Output:**

```text
context deadline exceeded
waited enough: true
```

**What happened:** `WithTimeout` canceled the context automatically after 30ms. `ctx.Err()` reported the deadline.

**Why it matters:** `defer cancel()` releases the timer and the parent's reference as soon as you are done, instead of waiting for the deadline. Pass the context down to database, HTTP, and broker calls so they stop too.

**If asked:** "Pass context as the first parameter, `defer cancel()`, and check `Done()` in loops. Deadlines only shrink as they go down the call chain."

---

# Part 2 — Shared state and patterns

## 12. Mutex: protect a read-modify-write

```go
package main

import (
	"fmt"
	"sync"
)

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) inc() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

func main() {
	var c counter
	var wg sync.WaitGroup

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.inc()
		}()
	}

	wg.Wait()
	fmt.Println("counter:", c.n)
}
```

**Output:**

```text
counter: 1000
```

**What happened:** `n++` is read, add, write. The mutex made that whole sequence one critical section, so two goroutines could not lose each other's update.

**Why it matters:** Without the lock, the count is usually less than 1000 and changes from run to run. To see the detector fire, remove the `Lock`/`Unlock` lines and run `go run -race 12-mutex.go` on Linux/CI. On this Windows machine it needs CGO and a C compiler; if you cannot run it, remember the rule.

**Variant:** `sync.RWMutex` lets many readers in or one writer. It fits read-mostly state, but it is not automatically faster; measure it.

**Rule:** a mutex is not reentrant. Locking it twice in the same goroutine deadlocks.

**If asked:** "Mutex for state read and written in place. Channel for handing off ownership or signaling completion."

---

## 13. Atomic and Once: one word, one time

```go
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	var n atomic.Int64
	var once sync.Once
	var wg sync.WaitGroup

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Add(1)
			once.Do(func() {
				fmt.Println("init once")
			})
		}()
	}

	wg.Wait()
	fmt.Println("atomic:", n.Load())
}
```

**Output:**

```text
init once
atomic: 1000
```

**What happened:** `atomic.Int64` made one counter safe without a mutex. `sync.Once` ran the init exactly once, even with 1000 goroutines.

**Why it matters:** Atomics are for one word, not a group of related fields. `Once` treats a panic as done, so a later call will not retry.

**If asked:** "Atomic for one counter or flag. Once for one-time init. For two related fields, use a mutex or an immutable snapshot."

---

## 14. Semaphore: cap in-flight work

```go
package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup

	for i := 1; i <= 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			fmt.Println("start", n)
			time.Sleep(30 * time.Millisecond)
			fmt.Println("end", n)
		}(i)
	}

	wg.Wait()
}
```

**Output (one possible run):**

```text
start 4
start 1
end 4
end 1
start 3
start 2
end 2
end 3
```

**What happened:** At most two goroutines were between `start` and `end`. Sending to the buffered channel took a permit; receiving gave it back.

**Why it matters:** "One goroutine per item" is the classic Go outage. A semaphore bounds the work without a full worker pool.

**If asked:** "A buffered channel is a counting semaphore: send to acquire, receive to release. Size it to the downstream, not to the number of items."

---

## 15. Worker pool: fixed workers, one jobs channel

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	jobs := make(chan int, 10)
	results := make(chan int, 10)

	var wg sync.WaitGroup
	for w := 1; w <= 3; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- j * j
			}
		}()
	}

	for i := 1; i <= 5; i++ {
		jobs <- i
	}
	close(jobs)

	wg.Wait()
	close(results)

	sum := 0
	for r := range results {
		sum += r
	}
	fmt.Println("sum:", sum)
}
```

**Output:**

```text
sum: 55
```

**What happened:** Three workers read from one jobs channel. The producer closed `jobs` after the last send. After `wg.Wait()`, the results channel was closed and drained. The sum is `1+4+9+16+25`.

**Why it matters:** The number of workers is fixed. A spike in jobs fills the queue and then blocks the producer; it does not create more goroutines.

**Production shape:** add cancellation to the worker loop:

```go
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
```

**If asked:** "Fixed workers, one jobs channel, the producer closes jobs, then `Wait` and close the results. The worker loop also selects on `ctx.Done()`."

---

## 16. `errgroup`: first error stops the group

Add the dependency once:

```bash
go get golang.org/x/sync/errgroup
```

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"
)

func main() {
	g, ctx := errgroup.WithContext(context.Background())

	g.Go(func() error {
		time.Sleep(20 * time.Millisecond)
		return errors.New("boom")
	})

	g.Go(func() error {
		<-ctx.Done()
		fmt.Println("sibling canceled:", ctx.Err())
		return ctx.Err()
	})

	err := g.Wait()
	fmt.Println("first error:", err)
}
```

**Output:**

```text
sibling canceled: context canceled
first error: boom
```

**What happened:** The first goroutine returned `boom`. `errgroup` canceled the derived context. The second goroutine saw `Done()`, returned `context.Canceled`, and `Wait` kept the first error.

**Why it matters:** Without the shared context, the second goroutine would keep working after the first failure. `Wait` gives you one error to return.

**Cap the group:**

```go
g.SetLimit(4)
```

`SetLimit(4)` blocks `g.Go` when four goroutines are in flight. `TryGo` is the non-blocking version.

**If asked:** "`errgroup.WithContext` gives a group and a derived context. The first non-nil error cancels the context, and `Wait` returns that error."

---

## 17. Pipeline: each stage owns its output

```go
package main

import "fmt"

func square(in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for v := range in {
			out <- v * v
		}
	}()
	return out
}

func main() {
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 1; i <= 5; i++ {
			in <- i
		}
	}()

	for v := range square(in) {
		fmt.Println(v)
	}
}
```

**Output:**

```text
1
4
9
16
25
```

**What happened:** The producer closed `in`. The square stage read until `in` closed, then closed `out`. The final range ended when `out` closed.

**Why it matters:** The stage that creates an output channel owns it and closes it. If a stage forgets to close, the next stage blocks forever.

**If asked:** "Each stage owns and closes its output. Cancellation has to be checked on both the input receive and the output send."

---

## 18. Fan-in: merge two streams, lose order

```go
package main

import (
	"fmt"
	"sync"
)

func gen(start int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for i := start; i < start+3; i++ {
			out <- i
		}
	}()
	return out
}

func merge(a, b <-chan int) <-chan int {
	out := make(chan int)
	var wg sync.WaitGroup

	wg.Add(2)
	forward := func(in <-chan int) {
		defer wg.Done()
		for v := range in {
			out <- v
		}
	}
	go forward(a)
	go forward(b)

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func main() {
	for v := range merge(gen(1), gen(10)) {
		fmt.Println(v)
	}
}
```

**Output (one possible run):**

```text
10
1
11
12
2
3
```

**What happened:** Two producers raced into one output channel. The values are all there, but the arrival order is not the input order.

**Why it matters:** Fan-in is fast but unordered. If order matters, add a sequence number and reorder downstream, or use one consumer.

**If asked:** "Fan-in merges streams and loses order. Use one closer after all senders finish."

---

# Part 3 — Failure demos

## 19. Leak: a send with no receiver

**Broken:**

```go
package main

import (
	"fmt"
	"runtime"
	"time"
)

func main() {
	ch := make(chan int)
	go func() {
		ch <- 1
	}()
	time.Sleep(50 * time.Millisecond)
	fmt.Println("goroutines:", runtime.NumGoroutine())
}
```

**Output:**

```text
goroutines: 2
```

**Fixed:**

```go
package main

import (
	"fmt"
	"runtime"
	"time"
)

func main() {
	ch := make(chan int, 1)
	go func() {
		ch <- 1
	}()
	time.Sleep(50 * time.Millisecond)
	fmt.Println("goroutines:", runtime.NumGoroutine())
}
```

**Output:**

```text
goroutines: 1
```

**What happened:** In the broken version, the sender blocked forever because nobody received. `NumGoroutine` counted `main` plus the blocked sender. The buffer gave the sender a place to put the value, so it exited.

**Why it matters:** A buffer is one fix, but the real question is "how does this goroutine stop?" If the answer is "when someone receives", make sure someone always receives or cancel it.

**If asked:** "The first question for every goroutine is: how does it stop? Find leaks with `NumGoroutine`, a stack dump, or `goleak`."

---

## 20. Timers: `time.After` in a loop vs one ticker

**Bad:**

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	for i := 0; i < 3; i++ {
		select {
		case <-time.After(time.Hour):
		case <-time.After(5 * time.Millisecond):
			fmt.Println("tick", i)
		}
	}
}
```

**Output:**

```text
tick 0
tick 1
tick 2
```

**Good:**

```go
package main

import (
	"fmt"
	"time"
)

func main() {
	t := time.NewTicker(5 * time.Millisecond)
	defer t.Stop()

	for i := 0; i < 3; i++ {
		<-t.C
		fmt.Println("tick", i)
	}
}
```

**Output:**

```text
tick 0
tick 1
tick 2
```

**What happened:** The bad version created a new one-hour timer on every loop. Those timers live until they fire. The good version creates one ticker and stops it.

**Why it matters:** In a long-running loop, `time.After` leaks timers and memory. Use `time.NewTicker` for repeated work, or `time.NewTimer` and `Stop` when you need one timer.

**If asked:** "`time.After` in a loop creates a timer per iteration. Use a ticker or `NewTimer` and stop it."

---

## 21. Deadlock: everyone is asleep

```go
package main

import "fmt"

func main() {
	ch := make(chan int)
	ch <- 1
	fmt.Println("never printed")
}
```

**Output (first line):**

```text
fatal error: all goroutines are asleep - deadlock!
```

The runtime then prints a stack trace and exits. The stack is omitted here.

**What happened:** The only goroutine blocked on a send that could never complete. The runtime saw that every goroutine was blocked and stopped the program.

**Why it matters:** A total deadlock is loud. A partial deadlock is silent: one request hangs while the rest of the server keeps running. That is why every wait needs a timeout or a cancellation path.

**Related:** a panic in an unrecovered goroutine kills the whole process. `recover` only works in a deferred function in that same goroutine.

**If asked:** "A total deadlock makes the runtime report it. A partial deadlock just hangs one path and is much harder to see."

---

## 22. Graceful shutdown: the shape, not a lab run

This one is not run in this Windows lab. Sending a signal to your own process is not supported on Windows. Run it on Linux, or press `Ctrl+C` in the terminal.

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A second signal should force an exit if the drain hangs.
	// This lab keeps the shape short and handles only the first signal.

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})

	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
```

**Expected behavior:** `Ctrl+C` cancels the context. The server stops accepting new connections, waits up to 10 seconds for in-flight requests, and then returns.

**Order to say aloud:** stop accepting, drain with a deadline, cancel background workers, close the database and broker.

**If asked:** "`signal.NotifyContext` gives a context canceled on a signal. `srv.Shutdown` stops accepting and waits for in-flight requests until the context deadline. A second signal should force an exit if the drain hangs."

---

## 23. Cancellation-aware generator: stop a blocked send

The first block is the mock-interview snippet's flow: `defer cancel()`, then `range` over the returned channel. The second block adds the case the snippet does not show: the consumer leaves early. It combines the receive-only return (#7), the unbuffered handshake (#4), `defer close` (#6), `select` (#8), and `ctx.Done()` (#10).

```go
package main

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

func produce(ctx context.Context, n int) <-chan int {
	ch := make(chan int)
	go func() {
		defer close(ch)
		for i := 1; i <= n; i++ {
			select {
			case ch <- i:
			case <-ctx.Done():
				fmt.Println("producer exiting:", ctx.Err())
				return
			}
		}
	}()
	return ch
}

func consumeAll() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for v := range produce(ctx, 3) {
		fmt.Println("received:", v)
	}
}

func main() {
	fmt.Println("--- consume everything ---")
	consumeAll()

	fmt.Println("--- consumer stops early ---")
	ctx, cancel := context.WithCancel(context.Background())
	ch := produce(ctx, 100)
	for v := range ch {
		fmt.Println("received:", v)
		if v == 2 {
			fmt.Println("consumer stops early")
			break
		}
	}

	cancel()
	time.Sleep(50 * time.Millisecond) // let the producer observe Done and close

	v, ok := <-ch
	fmt.Println("after cancel:", v, ok)
	fmt.Println("goroutines:", runtime.NumGoroutine())
}
```

**Output:**

```text
--- consume everything ---
received: 1
received: 2
received: 3
--- consumer stops early ---
received: 1
received: 2
consumer stops early
producer exiting: context canceled
after cancel: 0 false
goroutines: 1
```

**What happened:** In the first block the consumer drained all three values, so the producer finished its loop and `defer close(ch)` ended the `range`. In the second block the consumer stopped after two values. With no receiver left, the producer was blocked in `case ch <- i` at `i = 3`. `cancel()` made `ctx.Done()` ready, so the select took that case, the function returned, and `defer close(ch)` ran. The later receive saw the closed channel: zero and `false`. `NumGoroutine` was back to one.

**Why it matters:** The select is the difference between a generator that stops and one that leaks. Without `case <-ctx.Done()` the producer stays blocked on `ch <- 3` forever: `defer close(ch)` never runs, the channel never closes, and the goroutine is the leak from #19 in generator form. A bigger buffer only delays it; `n` can always exceed the buffer.

**The three lines that do the work:**

- The producer owns the channel and closes it with `defer close(ch)`; the consumer never closes it.
- Every send is paired with `ctx.Done()` in a `select`, so the producer has a stop path even when nobody is receiving.
- `return` after `<-ctx.Done()` still runs `defer close(ch)`, which is what tells every remaining consumer that the stream is over.

**One subtlety:** if a receiver and `ctx.Done()` are ready at the same moment, `select` picks at random, so one more value can be delivered after cancellation. Cancellation means "stop soon", not "drop the in-flight value". The range still ends when the producer returns and closes.

**If asked:** "The producer returns a receive-only channel and closes it with `defer`. Every send selects between the channel and `ctx.Done()`, so a consumer that walks away does not leak the producer. The close is what ends the consumer's `range`."

---

# Final Q&A recall

Cover the right column with your hand. Say the answer aloud. Then check.

| # | Question | One-line answer |
|---|---|---|
| 1 | Does `main` wait for goroutines? | No. When `main` returns, the process exits and all goroutines die. |
| 2 | Why `Add` before `go`? | So `Wait` cannot return before the counter goes up. |
| 3 | Unbuffered vs buffered channel? | Unbuffered is a handshake. Buffered is a bounded waiting room. |
| 4 | Who closes a channel? | The sender, or one closer after all senders finish. |
| 5 | Send or receive after close? | Send panics. Receive gives zero and `false` after the buffer drains. |
| 6 | What does a nil channel do? | Blocks forever, so its `select` case is disabled. |
| 7 | Two ready `select` cases? | One is chosen at random. No priority. |
| 8 | Why `defer cancel()`? | It releases the timer and the parent's reference now. |
| 9 | Mutex or channel? | Mutex for state in place. Channel for handoff or signal. |
| 10 | What is a data race? | Two goroutines touch the same memory, at least one writes, with no synchronization. |
| 11 | Does `-race` prove there are no races? | No. It only reports races it observed in that run. |
| 12 | How do you bound concurrency? | Worker pool, semaphore, or `errgroup.SetLimit`. |
| 13 | What does `errgroup.WithContext` do? | First error cancels the group; `Wait` returns that error. |
| 14 | Why is `time.After` in a loop a problem? | It creates a timer per iteration. Use a ticker or `NewTimer`. |
| 15 | What is the first question for every goroutine? | How does it stop? |
| 16 | What is happens-before? | A channel send before the receive, an unlock before a later lock, and `once.Do` before any return. |
| 17 | What does `GOMAXPROCS` control? | The number of Ps, not goroutines or OS threads. |
| 18 | A consumer stops receiving; how does the producer stop? | It selects on `ctx.Done()` and returns; `defer close(ch)` ends the consumer's `range`. |

---

## 30-second answer template

When asked "how would you build this concurrent piece?", use four sentences:

1. **Bound:** "I bound the work with a worker pool, a semaphore, or `errgroup.SetLimit`."
2. **Stop:** "Every goroutine stops when the context is canceled, the input closes, or the job finishes."
3. **Protect:** "Shared state in one process is protected by a mutex or an atomic; handoff uses a channel."
4. **Settle:** "Races that cross processes are settled by the database constraint or conditional update, not by a Go lock."

That is the whole Day 2 answer in four lines. The details go in the middle, not the opening.

---

## What to do next

- If a program surprised you, change one line and run it again. That is the lab.
- If you have 20 more minutes, run the semaphore (#14) and the worker pool (#15) from memory, without looking.
- For the deep scheduler and memory-model detail, open [`day-2-theory-cupboard.md`](day-2-theory-cupboard.md) only after the lab.
- Then use the Day 2 drills in [`day-2-concurrency.md`](day-2-concurrency.md) for the worker-pool proof and the three broken snippets.
- For the full worker-pool, retry, SSE, and shutdown patterns, use [`examples/go-concurrency.md`](examples/go-concurrency.md).

**One line to keep:** bound the goroutines, give every one a stop path, and let the database settle races that cross processes.
