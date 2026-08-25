package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============================================================================
// SYNCHRONIZATION PRIMITIVES (package sync and sync/atomic)
//
// Channels are for TRANSFERRING OWNERSHIP of data between goroutines.
// Mutexes/atomics are for PROTECTING SHARED STATE that stays in place.
// Picking the wrong one is the most common Go concurrency design mistake:
// a mutex around a queue is usually a channel; a channel around a counter
// is usually an atomic.
// ============================================================================

// ----------------------------------------------------------------------------
// 1. sync.Mutex — mutual exclusion
// ----------------------------------------------------------------------------

// Embedding the mutex NEXT TO the data it guards (never exported, always
// documented) is the standard Go idiom. The zero value is ready to use, so
// no constructor is required.
type SafeCounter struct {
	mu     sync.Mutex // guards counts
	counts map[string]int
}

func NewSafeCounter() *SafeCounter {
	return &SafeCounter{counts: make(map[string]int)}
}

func (c *SafeCounter) Inc(key string) {
	c.mu.Lock()
	defer c.mu.Unlock() // defer survives early returns and panics
	c.counts[key]++
}

func (c *SafeCounter) Get(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[key]
}

func demoMutex() {
	fmt.Println("1. sync.Mutex — guard the map, not the goroutine")

	c := NewSafeCounter()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Inc("hits")
		}()
	}
	wg.Wait()

	fmt.Printf("   hits = %d (exactly 100, no lost updates)\n", c.Get("hits"))
	fmt.Println("   note: Go maps are NOT safe for concurrent write — this would")
	fmt.Println("         otherwise abort with 'concurrent map writes'")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. sync.RWMutex — many readers or one writer
// ----------------------------------------------------------------------------

// Use RWMutex only when reads genuinely dominate (roughly 10:1 or better) AND
// the critical section is long enough to matter. RLock is more expensive than
// Lock under write contention, so a plain Mutex often wins for short sections.
type ConfigStore struct {
	mu     sync.RWMutex
	values map[string]string
}

func (s *ConfigStore) Get(key string) string {
	s.mu.RLock() // concurrent with other readers
	defer s.mu.RUnlock()
	return s.values[key]
}

func (s *ConfigStore) Set(key, value string) {
	s.mu.Lock() // exclusive: blocks readers and writers
	defer s.mu.Unlock()
	s.values[key] = value
}

func demoRWMutex() {
	fmt.Println("2. sync.RWMutex — read-mostly shared state")

	store := &ConfigStore{values: map[string]string{"region": "us-east-1"}}

	var wg sync.WaitGroup
	var reads atomic.Int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.Get("region") != "" {
				reads.Add(1)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		store.Set("region", "eu-west-1")
	}()
	wg.Wait()

	fmt.Printf("   %d successful concurrent reads, final value = %q\n",
		reads.Load(), store.Get("region"))
	fmt.Println("   RWMutex is NOT reentrant: calling Get() while holding Lock() deadlocks")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. sync.Once — exactly-once initialisation
// ----------------------------------------------------------------------------

type connection struct{ dsn string }

var (
	initOnce sync.Once
	conn     *connection
	initRuns atomic.Int64
)

func getConnection() *connection {
	// Once.Do blocks all callers until the first invocation RETURNS, so nobody
	// ever observes a half-built value. It also runs only once even if f panics.
	initOnce.Do(func() {
		initRuns.Add(1)
		time.Sleep(5 * time.Millisecond) // pretend dialling is slow
		conn = &connection{dsn: "couchbase://localhost"}
	})
	return conn
}

func demoOnce() {
	fmt.Println("3. sync.Once — lazy singleton initialisation")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = getConnection()
		}()
	}
	wg.Wait()

	fmt.Printf("   20 concurrent callers, initialiser ran %d time(s)\n", initRuns.Load())
	fmt.Printf("   dsn = %s\n", conn.dsn)

	// Go 1.21+ adds ergonomic wrappers that remove the package-level variables.
	expensive := sync.OnceValue(func() string {
		return "computed once, cached forever"
	})
	fmt.Printf("   sync.OnceValue: %q == %q\n", expensive(), expensive())
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. sync/atomic — lock-free counters and flags
// ----------------------------------------------------------------------------

func demoAtomic() {
	fmt.Println("4. sync/atomic — cheapest correct counter")

	// Typed atomics (Go 1.19+) are preferred over atomic.AddInt64(&x, 1):
	// the type makes non-atomic access impossible and handles alignment for you.
	var requests atomic.Int64
	var degraded atomic.Bool

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			requests.Add(1)
			if n == 42 {
				degraded.Store(true)
			}
		}(i)
	}
	wg.Wait()

	fmt.Printf("   requests=%d degraded=%t\n", requests.Load(), degraded.Load())

	// CompareAndSwap is the building block for lock-free algorithms:
	// "set to new ONLY IF it is still old". It returns false if someone
	// changed the value first, and you retry from the new state.
	var state atomic.Int64
	state.Store(10)
	swapped := state.CompareAndSwap(10, 20)
	failed := state.CompareAndSwap(10, 30) // stale expectation -> refused
	fmt.Printf("   CAS(10->20)=%t  CAS(10->30)=%t  final=%d\n",
		swapped, failed, state.Load())
	fmt.Println("   atomics protect ONE word; two related fields still need a mutex")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 5. sync.Map — a specialised concurrent map
// ----------------------------------------------------------------------------

func demoSyncMap() {
	fmt.Println("5. sync.Map — only for its two specific workloads")

	var cache sync.Map

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cache.Store(fmt.Sprintf("key-%d", n%3), n)
		}(i)
	}
	wg.Wait()

	// LoadOrStore is atomic: exactly one caller sees loaded=false. This is the
	// primitive behind "create the entry if absent" without a mutex.
	actual, loaded := cache.LoadOrStore("key-0", 999)
	fmt.Printf("   LoadOrStore(key-0) -> value=%v loaded=%t (existing kept)\n", actual, loaded)

	keys := []string{}
	cache.Range(func(k, _ any) bool {
		keys = append(keys, k.(string))
		return true // return false to stop iterating early
	})
	sort.Strings(keys)
	fmt.Printf("   keys: %s\n", strings.Join(keys, ", "))
	fmt.Println("   use sync.Map ONLY for: (a) write-once/read-many caches, or")
	fmt.Println("   (b) disjoint key sets per goroutine. Otherwise map+RWMutex is faster")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 6. sync.Cond — wait for a condition to become true
// ----------------------------------------------------------------------------

func demoCond() {
	fmt.Println("6. sync.Cond — waiting on a state predicate")

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	queue := []string{}
	consumed := make(chan string, 3)

	// Consumer: sleeps until there is work, without busy-polling.
	go func() {
		for i := 0; i < 3; i++ {
			mu.Lock()
			// ALWAYS wait in a for loop, never an if: Wait can return without
			// a matching Signal (spurious wakeup), and another consumer may
			// have taken the item between the Signal and your re-acquire.
			for len(queue) == 0 {
				cond.Wait() // atomically unlocks mu, sleeps, re-locks on wake
			}
			item := queue[0]
			queue = queue[1:]
			mu.Unlock()
			consumed <- item
		}
	}()

	for _, item := range []string{"job-a", "job-b", "job-c"} {
		mu.Lock()
		queue = append(queue, item)
		mu.Unlock()
		cond.Signal() // wake one waiter; Broadcast() wakes all
		time.Sleep(2 * time.Millisecond)
	}

	got := []string{<-consumed, <-consumed, <-consumed}
	fmt.Printf("   consumed in order: %s\n", strings.Join(got, ", "))
	fmt.Println("   in most Go code a buffered channel replaces Cond; reach for Cond")
	fmt.Println("   only when waiters need a predicate a channel cannot express")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 7. sync.Pool — reuse allocations, not state
// ----------------------------------------------------------------------------

func demoPool() {
	fmt.Println("7. sync.Pool — recycle short-lived buffers")

	pool := sync.Pool{
		New: func() any { return new(strings.Builder) },
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			b := pool.Get().(*strings.Builder)
			b.Reset() // CRITICAL: a pooled object carries the previous user's data
			defer pool.Put(b)

			fmt.Fprintf(b, "request-%d", n)
			_ = b.String()
		}(i)
	}
	wg.Wait()

	fmt.Println("   4 goroutines shared a small set of builders")
	fmt.Println("   caveats: the GC may drop pooled items at any time, and Pool is")
	fmt.Println("   a throughput optimisation only — never a cache with semantics")
	fmt.Println()
}

func main() {
	fmt.Println("=== Synchronization Primitives ===")
	fmt.Println()

	demoMutex()
	demoRWMutex()
	demoOnce()
	demoAtomic()
	demoSyncMap()
	demoCond()
	demoPool()

	fmt.Println("Choosing a primitive:")
	fmt.Println("  passing ownership of data ...... channel")
	fmt.Println("  guarding a struct's fields ..... sync.Mutex next to the fields")
	fmt.Println("  read-mostly shared state ....... sync.RWMutex")
	fmt.Println("  one counter or flag ............ sync/atomic")
	fmt.Println("  init exactly once .............. sync.Once / sync.OnceValue")
	fmt.Println("  wait for N goroutines .......... sync.WaitGroup")
	fmt.Println("  wait for a predicate ........... sync.Cond (rare)")
}
