package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// ============================================================================
// GOROUTINE BASICS
//
// A goroutine is a function scheduled by the *Go runtime*, not by the OS.
// It starts with a small stack (~2 KB) that grows on demand, which is why a
// single process can hold hundreds of thousands of goroutines while it could
// only hold a few thousand OS threads.
//
// Mental model: goroutines are the UNIT OF CONCURRENCY (independent work),
// OS threads are the UNIT OF PARALLELISM (simultaneous execution).
// Concurrency is about structure; parallelism is about execution.
// ============================================================================

// ----------------------------------------------------------------------------
// 1. Starting a goroutine
// ----------------------------------------------------------------------------

func demoStart() {
	fmt.Println("1. Starting a goroutine")

	// `go f()` evaluates f's ARGUMENTS immediately, then runs the call
	// concurrently. The caller does NOT wait.
	go fmt.Println("   hello from a goroutine (may or may not print)")

	// Without synchronization there is no guarantee the goroutine ever runs:
	// when main returns, the whole process exits and every other goroutine is
	// killed mid-flight. The sleep below is a DEMO CRUTCH, never a real fix.
	time.Sleep(10 * time.Millisecond)
	fmt.Println("   main continued")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. Arguments are evaluated at `go` time, not at execution time
// ----------------------------------------------------------------------------

func demoArgumentEvaluation() {
	fmt.Println("2. Arguments are evaluated when `go` executes")

	x := 1
	// x is copied NOW (value 1), even though the goroutine runs later.
	go func(captured int) {
		fmt.Printf("   passed as argument: %d (snapshot)\n", captured)
	}(x)

	x = 99 // changing x afterwards cannot affect the snapshot above

	time.Sleep(10 * time.Millisecond)
	fmt.Printf("   x is now: %d\n", x)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. Waiting properly with sync.WaitGroup
// ----------------------------------------------------------------------------

func demoWaitGroup() {
	fmt.Println("3. Waiting with sync.WaitGroup")

	var wg sync.WaitGroup

	for i := 1; i <= 3; i++ {
		// Add BEFORE `go`. Calling wg.Add inside the goroutine races with
		// wg.Wait and can let Wait return before the work is registered.
		wg.Add(1)

		go func(id int) {
			// defer guarantees Done runs even if the body panics or returns early.
			defer wg.Done()
			time.Sleep(time.Duration(id) * 10 * time.Millisecond)
			fmt.Printf("   worker %d finished\n", id)
		}(i)
	}

	wg.Wait() // blocks until the counter reaches zero
	fmt.Println("   all workers finished")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Loop-variable capture (semantics changed in Go 1.22)
// ----------------------------------------------------------------------------

func demoLoopVariable() {
	fmt.Println("4. Loop-variable capture")

	// Go 1.22+ : `i` is a NEW variable each iteration, so closing over it is safe.
	// Go 1.21- : `i` was ONE variable shared by every iteration, so all three
	//            goroutines typically printed 3.
	// Which behaviour you get is decided by the `go` directive in go.mod
	// (this module declares go 1.22.1), NOT by your installed toolchain version.
	var wg sync.WaitGroup
	results := make([]int, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = i // safe: each goroutine writes a distinct slice element
		}()
	}
	wg.Wait()

	fmt.Printf("   captured values: %v (Go 1.22+ per-iteration variable)\n", results)
	fmt.Println("   pre-1.22 this printed [3 3 3]; passing `i` as an argument works on every version")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 5. Counting goroutines and CPUs
// ----------------------------------------------------------------------------

func demoRuntime() {
	fmt.Println("5. Runtime introspection")

	fmt.Printf("   NumCPU:     %d  (logical cores visible to the process)\n", runtime.NumCPU())
	fmt.Printf("   GOMAXPROCS: %d  (goroutines running Go code in parallel)\n", runtime.GOMAXPROCS(0))
	fmt.Printf("   goroutines before: %d\n", runtime.NumGoroutine())

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-done // park here until released
		}()
	}

	time.Sleep(10 * time.Millisecond)
	fmt.Printf("   goroutines during: %d\n", runtime.NumGoroutine())

	close(done) // closing a channel wakes EVERY receiver at once
	wg.Wait()

	fmt.Printf("   goroutines after:  %d\n", runtime.NumGoroutine())
	fmt.Println("   (runtime.NumGoroutine is the cheapest leak detector you have)")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 6. A panic in ANY goroutine kills the whole process
// ----------------------------------------------------------------------------

func demoPanicIsolation() {
	fmt.Println("6. Panics do not stay inside their goroutine")

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()

		// recover() only works in the goroutine that panicked. The parent's
		// defer/recover CANNOT catch this — an unrecovered panic anywhere
		// terminates the entire program.
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("   recovered inside the goroutine: %v\n", r)
			}
		}()

		panic("something went wrong")
	}()

	wg.Wait()
	fmt.Println("   process survived because recover() was in the SAME goroutine")
	fmt.Println("   rule: every long-lived goroutine you spawn needs its own recover")
	fmt.Println()
}

func main() {
	fmt.Println("=== Goroutine Basics ===")
	fmt.Println()

	demoStart()
	demoArgumentEvaluation()
	demoWaitGroup()
	demoLoopVariable()
	demoRuntime()
	demoPanicIsolation()

	fmt.Println("Key takeaways:")
	fmt.Println("  - `go f()` returns immediately; main exiting kills everything")
	fmt.Println("  - wg.Add before `go`, wg.Done in a defer")
	fmt.Println("  - never use time.Sleep as synchronization in real code")
	fmt.Println("  - an unrecovered panic in any goroutine crashes the process")
}
