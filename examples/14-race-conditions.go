package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// DATA RACES
//
// A DATA RACE is two goroutines accessing the same memory concurrently with at
// least one write and no synchronization between them. In Go this is
// UNDEFINED BEHAVIOUR — not "you get one of the two values". The compiler may
// keep the variable in a register, reorder the access, or tear a multi-word
// value in half.
//
// A RACE CONDITION is broader: a correctness bug caused by timing, even when
// every individual access is properly synchronized (see checkThenAct below).
// The race detector finds data races. It cannot find race conditions.
//
//   RUN THIS FILE BOTH WAYS:
//     go run 14-race-conditions.go          # see the wrong number
//     go run -race 14-race-conditions.go    # see WHERE the race is
//
//   A race-instrumented BINARY exits with status 66 once it has reported a
//   race (`go run` wraps that and exits 1, printing "exit status 66").
//   Either way the exit is non-zero — that is the point: wire
//   `go test -race ./...` into CI and a detected race fails the build.
// ============================================================================

const iterations = 10_000

// ----------------------------------------------------------------------------
// 1. The classic lost update
// ----------------------------------------------------------------------------

func brokenCounter() int {
	counter := 0 // shared, unprotected

	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// counter++ is THREE machine operations: load, add, store.
			// Two goroutines can both load 5, both store 6, and one
			// increment vanishes. That is a lost update.
			counter++
		}()
	}
	wg.Wait()
	return counter
}

// ----------------------------------------------------------------------------
// 2. Fix A: mutex — correct for any critical section
// ----------------------------------------------------------------------------

func mutexCounter() int {
	counter := 0
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			counter++ // load-add-store is now indivisible
			mu.Unlock()
		}()
	}
	wg.Wait()
	return counter
}

// ----------------------------------------------------------------------------
// 3. Fix B: atomic — correct and cheaper for a single word
// ----------------------------------------------------------------------------

func atomicCounter() int64 {
	var counter atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter.Add(1) // one uninterruptible CPU instruction
		}()
	}
	wg.Wait()
	return counter.Load()
}

// ----------------------------------------------------------------------------
// 4. Fix C: don't share at all — shard, then combine
// ----------------------------------------------------------------------------

func shardedCounter() int {
	const workers = 8
	perWorker := make([]int, workers) // each goroutine owns one slot

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := id; i < iterations; i += workers {
				perWorker[id]++ // no sharing, so no synchronization needed
			}
		}(w)
	}
	wg.Wait() // Wait establishes happens-before: the reads below are safe

	total := 0
	for _, v := range perWorker {
		total += v
	}
	return total
}

// ----------------------------------------------------------------------------
// 5. A race CONDITION the race detector cannot see
// ----------------------------------------------------------------------------

type Account struct {
	mu      sync.Mutex
	balance int
}

func (a *Account) Balance() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.balance
}

func (a *Account) Withdraw(amount int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.balance -= amount
}

// Every method above is individually thread-safe, yet this function is broken:
// another goroutine can withdraw in the gap between the check and the act.
// This is TOCTOU (time-of-check to time-of-use) and -race stays silent.
func checkThenActBroken(a *Account, amount int) {
	if a.Balance() >= amount { // <-- lock released here
		// The sleep only WIDENS a window that already exists. In production the
		// gap is a few nanoseconds and the bug shows up once a month at 3am.
		time.Sleep(time.Millisecond)
		a.Withdraw(amount) // <-- re-acquired here; the world moved on
	}
}

// The fix is to make check-and-act ONE atomic operation. Widen the critical
// section instead of composing thread-safe calls.
func (a *Account) WithdrawIfAvailable(amount int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.balance < amount {
		return false
	}
	a.balance -= amount
	return true
}

func demoCheckThenAct() {
	fmt.Println("5. A race CONDITION that -race cannot detect")

	broken := &Account{balance: 100}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); checkThenActBroken(broken, 100) }()
	}
	wg.Wait()
	fmt.Printf("   composed thread-safe calls: balance = %d (should never go below 0)\n",
		broken.Balance())

	fixed := &Account{balance: 100}
	var granted atomic.Int64
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fixed.WithdrawIfAvailable(100) {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	fmt.Printf("   single atomic check-and-act: balance = %d, %d withdrawal(s) granted\n",
		fixed.Balance(), granted.Load())
	fmt.Println()
}

func main() {
	fmt.Println("=== Race Conditions ===")
	fmt.Printf("(each counter increments %d times; correct answer is %d)\n\n",
		iterations, iterations)

	got := brokenCounter()
	fmt.Println("1. Unsynchronized counter++")
	fmt.Printf("   got %d, lost %d increments  <-- DATA RACE\n\n", got, iterations-got)

	fmt.Println("2. sync.Mutex")
	fmt.Printf("   got %d\n\n", mutexCounter())

	fmt.Println("3. atomic.Int64")
	fmt.Printf("   got %d\n\n", atomicCounter())

	fmt.Println("4. Sharded, no sharing")
	fmt.Printf("   got %d\n\n", shardedCounter())

	demoCheckThenAct()

	fmt.Println("Rules:")
	fmt.Println("  - run tests with -race in CI; it has ~2-20x cost, so not in prod")
	fmt.Println("  - -race only reports races it actually OBSERVES at runtime")
	fmt.Println("  - a clean -race run is evidence, not proof")
	fmt.Println("  - composing two thread-safe calls does NOT give a thread-safe operation")
}
