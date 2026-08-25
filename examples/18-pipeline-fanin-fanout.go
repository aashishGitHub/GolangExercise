package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ============================================================================
// PIPELINES, FAN-OUT AND FAN-IN
//
// A PIPELINE is a chain of stages connected by channels. Each stage:
//   1. receives from an inbound channel
//   2. does one thing
//   3. sends on an outbound channel it OWNS and CLOSES
//
// Why this shape wins:
//   - every stage runs concurrently, so throughput = the SLOWEST stage,
//     not the sum of all stages (that is the whole point)
//   - unbuffered channels give you backpressure for free: a slow consumer
//     automatically throttles the producer instead of queueing into an OOM
//   - each stage is an ordinary function you can unit test in isolation
//
// FAN-OUT: several goroutines read from one channel (parallelise a slow stage)
// FAN-IN:  one goroutine merges several channels back into one
//
// This is the in-process version of a stream-processing topology — the same
// model as Kafka consumer groups or a Flink DAG, just inside one binary.
// ============================================================================

// ----------------------------------------------------------------------------
// Stage 1: source
// ----------------------------------------------------------------------------

func generator(ctx context.Context, nums ...int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out) // this stage owns `out`, so this stage closes it
		for _, n := range nums {
			select {
			case out <- n:
			case <-ctx.Done():
				return // cancellation unwinds the whole pipeline from the front
			}
		}
	}()
	return out
}

// ----------------------------------------------------------------------------
// Stage 2: transform (deliberately slow, so it is worth parallelising)
// ----------------------------------------------------------------------------

func square(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for n := range in { // ends when the UPSTREAM closes -> closure cascades
			time.Sleep(20 * time.Millisecond) // pretend this is expensive
			select {
			case out <- n * n:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// ----------------------------------------------------------------------------
// Stage 3: filter
// ----------------------------------------------------------------------------

func filterEven(ctx context.Context, in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for n := range in {
			if n%2 != 0 {
				continue
			}
			select {
			case out <- n:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// ----------------------------------------------------------------------------
// FAN-IN: merge N channels into one
// ----------------------------------------------------------------------------

func merge(ctx context.Context, channels ...<-chan int) <-chan int {
	out := make(chan int)
	var wg sync.WaitGroup

	// One forwarding goroutine per input channel.
	forward := func(c <-chan int) {
		defer wg.Done()
		for n := range c {
			select {
			case out <- n:
			case <-ctx.Done():
				return
			}
		}
	}

	wg.Add(len(channels))
	for _, c := range channels {
		go forward(c)
	}

	// Same closer idiom as the worker pool: close only after every forwarder
	// has finished, and only from a goroutine that owns `out`.
	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// ----------------------------------------------------------------------------
// 1. Sequential pipeline
// ----------------------------------------------------------------------------

func demoSequential() {
	fmt.Println("1. Linear pipeline: generate -> square -> filterEven")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()

	// Composition reads exactly like a shell pipe: gen | square | filter
	out := filterEven(ctx, square(ctx, generator(ctx, 1, 2, 3, 4, 5, 6, 7, 8)))

	var got []int
	for v := range out {
		got = append(got, v)
	}

	fmt.Printf("   even squares: %v\n", got)
	fmt.Printf("   took %v (8 items x 20ms, one square stage)\n",
		time.Since(start).Round(10*time.Millisecond))
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. Fan-out / fan-in to parallelise the slow stage
// ----------------------------------------------------------------------------

func demoFanOutFanIn() {
	fmt.Println("2. Fan-out x4 across the slow stage, then fan-in")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()

	source := generator(ctx, 1, 2, 3, 4, 5, 6, 7, 8)

	// FAN-OUT: four square stages all reading the SAME source channel.
	// The runtime distributes items among them automatically.
	workers := make([]<-chan int, 4)
	for i := range workers {
		workers[i] = square(ctx, source)
	}

	// FAN-IN: merge the four outputs back into one stream.
	merged := filterEven(ctx, merge(ctx, workers...))

	var got []int
	for v := range merged {
		got = append(got, v)
	}
	sort.Ints(got) // fan-in destroys ordering — sort if order matters

	fmt.Printf("   even squares: %v\n", got)
	fmt.Printf("   took %v (~4x faster; same result set)\n",
		time.Since(start).Round(10*time.Millisecond))
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. Early exit — the consumer stops reading
// ----------------------------------------------------------------------------

func demoEarlyExit() {
	fmt.Println("3. Early exit: consumer takes 3 items and leaves")

	ctx, cancel := context.WithCancel(context.Background())

	out := square(ctx, generator(ctx, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10))

	var got []int
	for v := range out {
		got = append(got, v)
		if len(got) == 3 {
			break // abandoning the loop is exactly where pipelines leak
		}
	}

	// cancel() is what unblocks the upstream stages still parked on their sends.
	// Without it, generator and square stay blocked forever holding their items.
	cancel()
	time.Sleep(50 * time.Millisecond) // let the stages notice and unwind

	fmt.Printf("   consumed %v then cancelled\n", got)
	fmt.Println("   the ctx.Done() case in every stage is what makes early exit safe")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Backpressure
// ----------------------------------------------------------------------------

func demoBackpressure() {
	fmt.Println("4. Backpressure: a slow consumer throttles a fast producer")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	produced := make(chan int)
	var sent int

	go func() {
		defer close(produced)
		for i := 0; i < 5; i++ {
			select {
			case produced <- i: // blocks until the slow consumer is ready
				sent++
			case <-ctx.Done():
				return
			}
		}
	}()

	start := time.Now()
	for range produced {
		time.Sleep(20 * time.Millisecond) // deliberately slow consumer
	}

	fmt.Printf("   producer sent %d items in %v — it ran at the CONSUMER's pace\n",
		sent, time.Since(start).Round(10*time.Millisecond))
	fmt.Println("   an unbuffered channel is a natural flow-control mechanism;")
	fmt.Println("   a large buffer just hides the imbalance until you run out of RAM")
	fmt.Println()
}

func main() {
	fmt.Println("=== Pipelines, Fan-Out and Fan-In ===")
	fmt.Println()

	demoSequential()
	demoFanOutFanIn()
	demoEarlyExit()
	demoBackpressure()

	fmt.Println("Pipeline rules:")
	fmt.Println("  - each stage OWNS its output channel and closes it on return")
	fmt.Println("  - closure cascades downstream: close(out) ends the next `range`")
	fmt.Println("  - every send is guarded by a ctx.Done() case")
	fmt.Println("  - fan-in needs WaitGroup + a dedicated closer goroutine")
	fmt.Println("  - fan-out destroys ordering; re-sort or carry an index")
	fmt.Println("  - buffer size is a tuning knob, never a correctness fix")
}
