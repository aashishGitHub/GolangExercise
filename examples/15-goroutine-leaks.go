package main

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// ============================================================================
// GOROUTINE LEAKS
//
// A goroutine leak is a goroutine that can never make progress and never
// returns. Nothing reclaims it: the Go GC collects unreachable MEMORY, but a
// blocked goroutine is always reachable from the scheduler, so it — and every
// object it references — lives until the process dies.
//
// Symptom in production: memory and runtime.NumGoroutine() climb linearly
// with traffic and never fall. Restarting "fixes" it for a few hours.
//
// THE RULE: whoever starts a goroutine must know, at that moment, how it will
// be told to stop. If you cannot answer "what makes this return?", it leaks.
// ============================================================================

func goroutines() int { return runtime.NumGoroutine() }

// Let the scheduler settle so counts are stable before we sample them.
func settle() { time.Sleep(50 * time.Millisecond) }

// ----------------------------------------------------------------------------
// LEAK 1: abandoned sender on an unbuffered channel
// ----------------------------------------------------------------------------

// The caller takes only the first result and walks away. The other senders
// block forever on an unbuffered channel that nobody will ever read again.
func leakyFirstResponse() string {
	ch := make(chan string) // UNBUFFERED — every send needs a live receiver

	for i := 1; i <= 3; i++ {
		go func(id int) {
			time.Sleep(time.Duration(id) * 10 * time.Millisecond)
			ch <- fmt.Sprintf("replica-%d", id) // replicas 2 and 3 block here forever
		}(i)
	}

	return <-ch // one receive, three sends -> 2 permanently parked goroutines
}

// FIX: give the channel enough capacity for every sender, so a send always
// succeeds whether or not anyone is still listening.
func fixedFirstResponse() string {
	ch := make(chan string, 3) // BUFFERED for all 3 senders

	for i := 1; i <= 3; i++ {
		go func(id int) {
			time.Sleep(time.Duration(id) * 10 * time.Millisecond)
			ch <- fmt.Sprintf("replica-%d", id) // never blocks
		}(i)
	}

	return <-ch
}

// ----------------------------------------------------------------------------
// LEAK 2: a worker with no stop signal
// ----------------------------------------------------------------------------

// A background loop with no exit condition. Every call to this function adds
// a goroutine (and a ticker) that runs until the process exits.
func leakyPoller() {
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		for range ticker.C { // no way out — the ticker never closes
			_ = "poll"
		}
	}()
}

// FIX: take a context. The caller now owns the lifetime, and cancellation
// propagates down the whole call tree for free.
func fixedPoller(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop() // release the timer too, not just the goroutine
		for {
			select {
			case <-ctx.Done():
				return // the ONLY reason this goroutine exists is gone
			case <-ticker.C:
				_ = "poll"
			}
		}
	}()
}

// ----------------------------------------------------------------------------
// LEAK 3: forgetting to cancel a context
// ----------------------------------------------------------------------------

// context.WithTimeout starts an internal timer goroutine. Not calling cancel
// keeps the parent holding a reference to this child until the deadline fires.
// This is why `go vet` has a lostcancel check — it catches exactly this.
func leakyContext() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	_ = cancel // NEVER CALLED — the classic bug

	go func() {
		<-ctx.Done() // parked for an hour
	}()
}

// FIX: `defer cancel()` right after the call, every single time. Calling
// cancel after the work completes is not an error — it is required cleanup.
func fixedContext() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel() // releases the timer and unblocks ctx.Done() on return

	done := make(chan struct{})
	go func() {
		defer close(done)
		// The worker must be able to finish EITHER way: work done, or
		// cancelled. Waiting only on ctx.Done() here would deadlock, because
		// the deferred cancel cannot run until this goroutine releases <-done.
		select {
		case <-ctx.Done(): // cancelled or deadline exceeded
		case <-time.After(10 * time.Millisecond): // work finished normally
		}
	}()

	<-done
}

// ----------------------------------------------------------------------------
// LEAK 4: range over a channel the producer never closes
// ----------------------------------------------------------------------------

func leakyRange() {
	ch := make(chan int)

	go func() {
		for range ch { // blocks forever once the producer stops without closing
			_ = "consume"
		}
	}()

	go func() {
		for i := 0; i < 3; i++ {
			ch <- i
		}
		// BUG: no close(ch) — the consumer waits for a value that never comes
	}()

	time.Sleep(20 * time.Millisecond)
}

// FIX: the sender closes when it is done. `range` then terminates cleanly.
func fixedRange() {
	ch := make(chan int)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for range ch {
			_ = "consume"
		}
	}()

	go func() {
		defer close(ch) // producer owns the channel, so producer closes it
		for i := 0; i < 3; i++ {
			ch <- i
		}
	}()

	<-done
}

// ----------------------------------------------------------------------------
// Measurement helper: run f, then report the goroutine delta it left behind
// ----------------------------------------------------------------------------

func measure(label string, f func()) {
	settle()
	before := goroutines()

	f()

	settle()
	delta := goroutines() - before

	status := "OK   "
	if delta > 0 {
		status = "LEAK "
	}
	fmt.Printf("   %s %-34s goroutines %d -> %d  (delta %+d)\n",
		status, label, before, before+delta, delta)
}

func main() {
	fmt.Println("=== Goroutine Leaks ===")
	fmt.Println()
	fmt.Printf("baseline goroutines: %d\n\n", goroutines())

	fmt.Println("1. Abandoned senders on an unbuffered channel")
	measure("leakyFirstResponse()", func() { _ = leakyFirstResponse() })
	measure("fixedFirstResponse()", func() { _ = fixedFirstResponse() })
	fmt.Println()

	fmt.Println("2. Background worker with no stop signal")
	measure("leakyPoller()", leakyPoller)
	measure("fixedPoller(ctx) + cancel", func() {
		ctx, cancel := context.WithCancel(context.Background())
		fixedPoller(ctx)
		time.Sleep(20 * time.Millisecond)
		cancel()
	})
	fmt.Println()

	fmt.Println("3. Context created but never cancelled")
	measure("leakyContext()", leakyContext)
	measure("fixedContext()", fixedContext)
	fmt.Println()

	fmt.Println("4. range over a channel that is never closed")
	measure("leakyRange()", leakyRange)
	measure("fixedRange()", fixedRange)
	fmt.Println()

	fmt.Printf("final goroutines: %d — every one above baseline is leaked\n\n", goroutines())

	fmt.Println("How to catch leaks:")
	fmt.Println("  - go.uber.org/goleak in TestMain fails tests that leak goroutines")
	fmt.Println("  - expose runtime.NumGoroutine() as a metric and alert on the slope")
	fmt.Println("  - net/http/pprof: /debug/pprof/goroutine?debug=2 dumps every stack")
	fmt.Println("  - SIGQUIT (Ctrl-\\) dumps all goroutine stacks and exits")
	fmt.Println()
	fmt.Println("How to prevent them:")
	fmt.Println("  - pass context.Context as the first parameter, honour ctx.Done()")
	fmt.Println("  - defer cancel() immediately after every WithCancel/WithTimeout")
	fmt.Println("  - buffer result channels for ALL possible senders")
	fmt.Println("  - the sender closes the channel; the receiver never does")
}
