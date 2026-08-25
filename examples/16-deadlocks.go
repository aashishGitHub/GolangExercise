package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ============================================================================
// DEADLOCKS AND FRIENDS
//
// A deadlock needs all four Coffman conditions to hold at once:
//   1. mutual exclusion   — a resource is held exclusively
//   2. hold and wait      — a holder requests another resource
//   3. no preemption      — you cannot take a lock away from its holder
//   4. circular wait      — A waits on B, B waits on A
// Break ANY one of them and deadlock becomes impossible. Go's standard fix is
// to break #4 with a global lock ordering, or #2 with TryLock/timeouts.
//
// Go detects only the total case: when EVERY goroutine is blocked, the runtime
// panics with "fatal error: all goroutines are asleep - deadlock!". A partial
// deadlock — two goroutines stuck while the rest of the server runs — is
// invisible to the runtime and is what you actually hit in production.
//
// Every demo below uses a WATCHDOG so this file always terminates.
// ============================================================================

// watchdog runs f and reports whether it finished within the timeout.
// The stuck goroutine is deliberately abandoned — that is what a real
// deadlock costs you: a goroutine (and its locks) gone forever.
func watchdog(label string, timeout time.Duration, f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		fmt.Printf("   OK       %s\n", label)
	case <-timer.C:
		fmt.Printf("   DEADLOCK %s (still stuck after %v)\n", label, timeout)
	}
}

// ----------------------------------------------------------------------------
// 1. Lock-ordering deadlock (the AB-BA problem)
// ----------------------------------------------------------------------------

type BrokenAccount struct {
	id      int
	mu      sync.Mutex
	balance int
}

// Each transfer locks `from` then `to`. Two concurrent transfers in opposite
// directions produce a cycle: T1 holds A wants B, T2 holds B wants A.
func brokenTransfer(from, to *BrokenAccount, amount int) {
	from.mu.Lock()
	defer from.mu.Unlock()

	// This sleep makes the interleaving reliable for the demo. Remove it and
	// the bug still exists — it just becomes rare and non-reproducible.
	time.Sleep(10 * time.Millisecond)

	to.mu.Lock() // <-- both goroutines block here, forever
	defer to.mu.Unlock()

	from.balance -= amount
	to.balance += amount
}

func demoLockOrdering() {
	fmt.Println("1. Lock-ordering deadlock (AB-BA)")

	a := &BrokenAccount{id: 1, balance: 1000}
	b := &BrokenAccount{id: 2, balance: 1000}

	watchdog("A->B and B->A with inconsistent order", 200*time.Millisecond, func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); brokenTransfer(a, b, 100) }() // locks A, wants B
		go func() { defer wg.Done(); brokenTransfer(b, a, 200) }() // locks B, wants A
		wg.Wait()
	})
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. FIX A: impose a total order on lock acquisition
// ----------------------------------------------------------------------------

type Account16 struct {
	id      int
	mu      sync.Mutex
	balance int
}

// Always lock the lower id first, regardless of transfer direction. Now no
// cycle can form, because every goroutine walks the same ordering.
func orderedTransfer(from, to *Account16, amount int) {
	first, second := from, to
	if first.id > second.id {
		first, second = second, first
	}

	first.mu.Lock()
	defer first.mu.Unlock()
	time.Sleep(10 * time.Millisecond) // same interleaving as the broken version
	second.mu.Lock()
	defer second.mu.Unlock()

	from.balance -= amount
	to.balance += amount
}

func demoLockOrderingFix() {
	fmt.Println("2. FIX A — consistent lock ordering (breaks circular wait)")

	a := &Account16{id: 1, balance: 1000}
	b := &Account16{id: 2, balance: 1000}

	watchdog("A->B and B->A ordered by id", 500*time.Millisecond, func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); orderedTransfer(a, b, 100) }()
		go func() { defer wg.Done(); orderedTransfer(b, a, 200) }()
		wg.Wait()
	})
	fmt.Printf("   balances: A=%d B=%d (total %d, conserved)\n",
		a.balance, b.balance, a.balance+b.balance)
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. FIX B: TryLock + back off (breaks hold-and-wait)
// ----------------------------------------------------------------------------

// Use this when a total order is impossible (locks discovered dynamically).
// Take the second lock optimistically; if it is busy, release everything and
// retry so the other party can make progress.
func tryLockTransfer(from, to *Account16, amount int) bool {
	for attempt := 0; attempt < 100; attempt++ {
		from.mu.Lock()

		if to.mu.TryLock() { // Go 1.18+: acquires and returns true, or returns false
			from.balance -= amount
			to.balance += amount
			to.mu.Unlock()
			from.mu.Unlock()
			return true
		}

		from.mu.Unlock()             // release everything — no hold-and-wait
		time.Sleep(time.Millisecond) // back off so the peer can finish
	}
	return false
}

func demoTryLock() {
	fmt.Println("3. FIX B — TryLock with backoff (breaks hold-and-wait)")

	a := &Account16{id: 1, balance: 1000}
	b := &Account16{id: 2, balance: 1000}

	watchdog("A->B and B->A with TryLock", 500*time.Millisecond, func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); tryLockTransfer(a, b, 100) }()
		go func() { defer wg.Done(); tryLockTransfer(b, a, 200) }()
		wg.Wait()
	})
	fmt.Printf("   balances: A=%d B=%d (total %d, conserved)\n",
		a.balance, b.balance, a.balance+b.balance)
	fmt.Println("   caution: TryLock loops can livelock — everyone retries in lockstep,")
	fmt.Println("   nobody progresses. Randomised backoff avoids that.")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Self-deadlock: Go mutexes are NOT reentrant
// ----------------------------------------------------------------------------

type Registry struct {
	mu    sync.Mutex
	items map[string]int
}

// BUG: Add already holds mu, then calls Count which locks mu again.
// In Java this would be fine (reentrant locks); in Go it deadlocks instantly.
func (r *Registry) AddBroken(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[key] = 1
	return r.CountBroken() // <-- tries to re-lock a mutex this goroutine holds
}

func (r *Registry) CountBroken() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items)
}

// FIX: split each operation into a locked public method and an unlocked
// private helper. The convention is that a `xxxLocked` method REQUIRES the
// caller to already hold the lock.
func (r *Registry) Add(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[key] = 1
	return r.countLocked() // no re-lock
}

func (r *Registry) countLocked() int { return len(r.items) } // caller holds r.mu

func demoReentrancy() {
	fmt.Println("4. Self-deadlock — sync.Mutex is not reentrant")

	broken := &Registry{items: map[string]int{}}
	watchdog("AddBroken() re-locks its own mutex", 200*time.Millisecond, func() {
		broken.AddBroken("a")
	})

	fixed := &Registry{items: map[string]int{}}
	watchdog("Add() uses an unlocked helper", 200*time.Millisecond, func() {
		fixed.Add("a")
		fixed.Add("b")
	})
	fmt.Printf("   fixed registry holds %d items\n", len(fixed.items))
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 5. WaitGroup misuse
// ----------------------------------------------------------------------------

func demoWaitGroupMisuse() {
	fmt.Println("5. WaitGroup counter never reaches zero")

	watchdog("missing wg.Done() on an early return", 200*time.Millisecond, func() {
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(n int) {
				if n == 1 {
					return // BUG: returns without Done -> counter stuck at 1
				}
				wg.Done()
			}(i)
		}
		wg.Wait()
	})

	watchdog("defer wg.Done() as the first statement", 200*time.Millisecond, func() {
		var wg sync.WaitGroup
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done() // fires on EVERY exit path, including panics
				if n == 1 {
					return
				}
			}(i)
		}
		wg.Wait()
	})
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 6. Channel deadlocks
// ----------------------------------------------------------------------------

func demoChannelDeadlock() {
	fmt.Println("6. Channel deadlocks")

	watchdog("send on unbuffered chan with no receiver", 200*time.Millisecond, func() {
		ch := make(chan int)
		ch <- 1 // no concurrent receiver -> blocks forever
	})

	watchdog("buffered chan with room", 200*time.Millisecond, func() {
		ch := make(chan int, 1)
		ch <- 1 // fits in the buffer
		<-ch
	})

	watchdog("receive from a chan nobody closes", 200*time.Millisecond, func() {
		ch := make(chan int)
		go func() { /* producer forgets to send or close */ }()
		<-ch
	})

	// NOTE: if the ENTIRE program were blocked like this, the runtime would
	// abort with "fatal error: all goroutines are asleep - deadlock!".
	// Here the watchdog goroutines are still runnable, so the runtime stays
	// quiet — which is exactly why partial deadlocks survive into production.
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 7. Sorting out who is stuck
// ----------------------------------------------------------------------------

func demoDiagnosis() {
	fmt.Println("7. Diagnosing a live deadlock")

	// Goroutines abandoned by the demos above are still parked. In a real
	// service you find them by name in a stack dump.
	stuck := []string{
		"brokenTransfer  (blocked on sync.Mutex.Lock)",
		"AddBroken       (blocked on sync.Mutex.Lock)",
		"wg.Wait         (blocked on sync.WaitGroup.Wait)",
		"chan send/recv  (blocked on chan send / chan receive)",
	}
	sort.Strings(stuck)
	for _, s := range stuck {
		fmt.Printf("   leaked: %s\n", s)
	}
	fmt.Println()
	fmt.Println("   Get the stacks with any of:")
	fmt.Println("     kill -QUIT <pid>              dump all goroutines and exit")
	fmt.Println("     curl :6060/debug/pprof/goroutine?debug=2   (net/http/pprof)")
	fmt.Println("     GOTRACEBACK=all               include runtime frames")
	fmt.Println("   Look for many goroutines parked in 'semacquire' on the same lock.")
	fmt.Println()
}

func main() {
	fmt.Println("=== Deadlocks ===")
	fmt.Println()

	demoLockOrdering()
	demoLockOrderingFix()
	demoTryLock()
	demoReentrancy()
	demoWaitGroupMisuse()
	demoChannelDeadlock()
	demoDiagnosis()

	fmt.Println("Prevention checklist:")
	fmt.Println("  - define ONE global lock order and document it")
	fmt.Println("  - never call an unknown function while holding a lock")
	fmt.Println("    (callbacks and interface methods can re-enter your code)")
	fmt.Println("  - keep critical sections short; no I/O under a lock")
	fmt.Println("  - one mutex per struct beats one mutex per program")
	fmt.Println("  - prefer channels for handoff; locks only for in-place state")
	fmt.Println("  - remember: sync.Mutex and sync.RWMutex are NOT reentrant")
}
