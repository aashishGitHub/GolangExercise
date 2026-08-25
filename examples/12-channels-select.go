package main

import (
	"fmt"
	"time"
)

// ============================================================================
// CHANNELS AND SELECT
//
// "Do not communicate by sharing memory; instead, share memory by
//  communicating."  — Go proverb
//
// A channel is a typed, thread-safe queue that also carries a HAPPENS-BEFORE
// guarantee: everything a sender did before the send is visible to the
// receiver after the receive. That is why channels replace locks for
// ownership transfer — the data moves, so only one goroutine touches it.
// ============================================================================

// ----------------------------------------------------------------------------
// 1. Unbuffered vs buffered
// ----------------------------------------------------------------------------

func demoUnbufferedVsBuffered() {
	fmt.Println("1. Unbuffered vs buffered channels")

	// UNBUFFERED: a send blocks until a receiver is ready. It is a RENDEZVOUS,
	// so it doubles as a synchronization point ("I know you got it").
	unbuffered := make(chan string)
	go func() { unbuffered <- "delivered by hand" }()
	fmt.Printf("   unbuffered: %s\n", <-unbuffered)

	// BUFFERED: sends succeed until the buffer is full. It decouples producer
	// and consumer speed — this is your backpressure knob.
	buffered := make(chan int, 3)
	buffered <- 1
	buffered <- 2
	fmt.Printf("   buffered: len=%d cap=%d (2 queued, room for 1 more)\n",
		len(buffered), cap(buffered))
	fmt.Printf("   drained: %d %d\n", <-buffered, <-buffered)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. Direction-typed channels encode intent in the signature
// ----------------------------------------------------------------------------

// chan<- int  = send-only (the compiler rejects receives)
// <-chan int  = receive-only (the compiler rejects sends and close)
func produce(out chan<- int, n int) {
	// The producer OWNS the channel, so the producer closes it.
	// Rule: only the sender closes; a receiver closing causes a send panic.
	defer close(out)
	for i := 1; i <= n; i++ {
		out <- i * i
	}
}

func demoDirections() {
	fmt.Println("2. Direction-typed channels and closing")

	ch := make(chan int)
	go produce(ch, 5)

	// `range` over a channel reads until the channel is CLOSED and drained.
	// A closed channel keeps yielding buffered values first, then zero values.
	fmt.Print("   squares: ")
	for v := range ch {
		fmt.Printf("%d ", v)
	}
	fmt.Println()

	// comma-ok distinguishes "real value" from "channel closed".
	v, ok := <-ch
	fmt.Printf("   after close: value=%d ok=%t (zero value, not a real send)\n", v, ok)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. select: wait on several channels at once
// ----------------------------------------------------------------------------

func demoSelect() {
	fmt.Println("3. select multiplexes channel operations")

	fast := make(chan string)
	slow := make(chan string)

	go func() { time.Sleep(10 * time.Millisecond); fast <- "fast replica" }()
	go func() { time.Sleep(80 * time.Millisecond); slow <- "slow replica" }()

	// select blocks until ONE case is ready. If several are ready it picks
	// uniformly at random — that randomness prevents starvation between cases.
	select {
	case msg := <-fast:
		fmt.Printf("   first answer wins: %s\n", msg)
	case msg := <-slow:
		fmt.Printf("   first answer wins: %s\n", msg)
	}

	// Drain the loser so its goroutine is not left blocked forever
	// (an unread send on an unbuffered channel is a classic goroutine leak).
	<-slow
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Non-blocking operations with `default`
// ----------------------------------------------------------------------------

func demoNonBlocking() {
	fmt.Println("4. Non-blocking send/receive with default")

	queue := make(chan int, 1)

	// Non-blocking send = load shedding. This is how you drop work instead of
	// letting an overloaded producer block the whole service.
	for _, v := range []int{1, 2} {
		select {
		case queue <- v:
			fmt.Printf("   enqueued %d\n", v)
		default:
			fmt.Printf("   queue full, dropped %d (shedding load)\n", v)
		}
	}

	// Non-blocking receive = poll without parking the goroutine.
	select {
	case v := <-queue:
		fmt.Printf("   dequeued %d\n", v)
	default:
		fmt.Println("   nothing available")
	}
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 5. Timeouts
// ----------------------------------------------------------------------------

func demoTimeout() {
	fmt.Println("5. Timeouts with select")

	result := make(chan string, 1) // buffered: the worker can finish even if we walk away

	go func() {
		time.Sleep(200 * time.Millisecond) // a slow downstream call
		result <- "late response"
	}()

	// A timer is created here. Prefer time.NewTimer + defer Stop in hot loops:
	// on Go versions before 1.23 a time.After timer stays alive until it fires,
	// so a tight loop over time.After accumulates timers.
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()

	select {
	case msg := <-result:
		fmt.Printf("   got: %s\n", msg)
	case <-timer.C:
		fmt.Println("   timed out after 50ms (worker still running in background)")
	}
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 6. nil channels disable select cases
// ----------------------------------------------------------------------------

func demoNilChannel() {
	fmt.Println("6. nil channels block forever — use that as an off switch")

	a := make(chan int)
	b := make(chan int)

	go func() { a <- 1; a <- 2; close(a) }()
	go func() { b <- 10; close(b) }()

	// Merging two streams: as each closes we set its variable to nil so the
	// case is permanently disabled. Without this, a closed channel is ALWAYS
	// ready and the select spins at 100% CPU.
	sum, received := 0, 0
	for a != nil || b != nil {
		select {
		case v, ok := <-a:
			if !ok {
				a = nil // disable this case
				continue
			}
			sum += v
			received++
		case v, ok := <-b:
			if !ok {
				b = nil // disable this case
				continue
			}
			sum += v
			received++
		}
	}

	fmt.Printf("   merged %d values, sum=%d, both streams closed cleanly\n", received, sum)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 7. close as a broadcast
// ----------------------------------------------------------------------------

func demoCloseBroadcast() {
	fmt.Println("7. close() broadcasts to every waiting receiver")

	// chan struct{} carries no data and allocates nothing — it is a pure signal.
	start := make(chan struct{})
	done := make(chan struct{}, 3)

	for i := 1; i <= 3; i++ {
		go func(id int) {
			<-start // all three park here
			done <- struct{}{}
		}(i)
	}

	close(start) // one close releases ALL receivers — this is the "starting gun"
	for i := 0; i < 3; i++ {
		<-done
	}
	fmt.Println("   3 goroutines released by a single close()")
	fmt.Println()
}

func main() {
	fmt.Println("=== Channels and Select ===")
	fmt.Println()

	demoUnbufferedVsBuffered()
	demoDirections()
	demoSelect()
	demoNonBlocking()
	demoTimeout()
	demoNilChannel()
	demoCloseBroadcast()

	fmt.Println("Channel axioms (memorise these):")
	fmt.Println("  send on nil chan    -> blocks forever")
	fmt.Println("  recv on nil chan    -> blocks forever")
	fmt.Println("  send on closed chan -> panic")
	fmt.Println("  recv on closed chan -> zero value immediately, ok=false")
	fmt.Println("  close a closed chan -> panic")
	fmt.Println("  only the SENDER closes, and only when it is the sole sender")
}
