package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ============================================================================
// CONTEXT — cancellation, deadlines and request scope
//
// context.Context is how a Go program answers one question at every level of
// the call stack: "should I still be doing this?"
//
// In a distributed system a request fans out across goroutines and services.
// When the client hangs up, or the deadline passes, EVERY branch of that tree
// must stop — otherwise you keep burning CPU, DB connections and downstream
// quota on an answer nobody will read. That is the difference between a
// service that sheds load under pressure and one that collapses under it.
//
// A Context is an IMMUTABLE node in a tree. Deriving a child never mutates the
// parent; cancellation always flows PARENT -> CHILD, never upward.
//
// CONVENTIONS (these are near-universal in Go):
//   - first parameter, always named ctx: func Do(ctx context.Context, ...) error
//   - never store a Context in a struct field
//   - never pass nil; use context.TODO() if you do not have one yet
//   - always `defer cancel()`, even when the deadline will fire anyway
// ============================================================================

// ----------------------------------------------------------------------------
// 1. Cancellation propagates down the whole tree
// ----------------------------------------------------------------------------

func demoPropagation() {
	fmt.Println("1. One cancel stops an entire tree of goroutines")

	root, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	stopped := make(chan string, 6)

	// Two "services", each with three "sub-tasks" — a 2-level tree.
	for s := 1; s <= 2; s++ {
		// A derived context: cancelling `root` cancels these too.
		svcCtx, svcCancel := context.WithCancel(root)
		defer svcCancel() //nolint:gocritic // deferred in a loop on purpose: demo scope

		for t := 1; t <= 3; t++ {
			wg.Add(1)
			go func(svc, task int) {
				defer wg.Done()
				<-svcCtx.Done() // parked until cancellation reaches us
				stopped <- fmt.Sprintf("svc%d/task%d", svc, task)
			}(s, t)
		}
	}

	time.Sleep(20 * time.Millisecond)
	cancel() // ONE call at the root
	wg.Wait()
	close(stopped)

	count := 0
	for range stopped {
		count++
	}
	fmt.Printf("   one cancel() at the root stopped %d goroutines across 2 services\n", count)
	fmt.Printf("   root error: %v\n", root.Err())
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. Timeouts and deadlines
// ----------------------------------------------------------------------------

// slowCall is the shape every network call should have: racing the work
// against ctx.Done() so a hung server cannot pin the goroutine.
func slowCall(ctx context.Context, name string, d time.Duration) (string, error) {
	select {
	case <-time.After(d):
		return name + " ok", nil
	case <-ctx.Done():
		// Wrap, do not replace: the caller wants both "which call" and "why".
		return "", fmt.Errorf("%s: %w", name, ctx.Err())
	}
}

func demoTimeout() {
	fmt.Println("2. Timeouts, deadlines and error identity")

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := slowCall(ctx, "fast-service", 10*time.Millisecond); err == nil {
		fmt.Println("   fast-service completed within budget")
	}

	_, err := slowCall(ctx, "slow-service", 500*time.Millisecond)
	fmt.Printf("   slow-service error: %v\n", err)

	// Distinguishing these two matters: DeadlineExceeded is often retryable
	// against another replica; Canceled means the CLIENT left, so retrying
	// is pure waste.
	fmt.Printf("   is DeadlineExceeded: %t\n", errors.Is(err, context.DeadlineExceeded))
	fmt.Printf("   is Canceled:         %t\n", errors.Is(err, context.Canceled))

	// Deadline() reports the absolute wall-clock instant, which is what lets a
	// server decide "not enough budget left, fail fast" before even dialling.
	if dl, ok := ctx.Deadline(); ok {
		fmt.Printf("   budget was %v; slow-service gave up after %v\n",
			dl.Sub(start).Round(10*time.Millisecond),
			time.Since(start).Round(10*time.Millisecond))
	}
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. The tightest deadline always wins
// ----------------------------------------------------------------------------

func demoDeadlineInheritance() {
	fmt.Println("3. A child can shorten a deadline but never extend it")

	parent, cancelParent := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelParent()

	// Ask for 5 seconds from a parent that only has 50ms: you get 50ms.
	child, cancelChild := context.WithTimeout(parent, 5*time.Second)
	defer cancelChild()

	start := time.Now()
	<-child.Done()

	fmt.Printf("   child asked for 5s under a 50ms parent, fired after %v\n",
		time.Since(start).Round(10*time.Millisecond))
	fmt.Printf("   child err: %v\n", child.Err())
	fmt.Println("   this is what stops a downstream service from outliving its caller")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Cancellation CAUSE (Go 1.20+)
// ----------------------------------------------------------------------------

func demoCause() {
	fmt.Println("4. context.Cause — why, not just that")

	errQuotaExceeded := errors.New("tenant quota exceeded")

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errQuotaExceeded)

	// ctx.Err() stays the coarse sentinel so existing code keeps working...
	fmt.Printf("   ctx.Err():       %v\n", ctx.Err())
	// ...while Cause carries the specific reason for your logs and metrics.
	fmt.Printf("   context.Cause(): %v\n", context.Cause(ctx))
	fmt.Printf("   errors.Is(cause, errQuotaExceeded): %t\n",
		errors.Is(context.Cause(ctx), errQuotaExceeded))
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 5. Request-scoped values
// ----------------------------------------------------------------------------

// An unexported key type makes collisions between packages impossible.
// Using a plain string key is the classic bug: any other package can
// accidentally read or overwrite it.
type ctxKey int

const (
	requestIDKey ctxKey = iota
	tenantKey
)

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func requestIDFrom(ctx context.Context) string {
	// Always use the comma-ok form: the value may be absent or the wrong type.
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return "unknown"
}

func demoValues() {
	fmt.Println("5. Request-scoped values (use sparingly)")

	ctx := withRequestID(context.Background(), "req-7f3a")
	ctx = context.WithValue(ctx, tenantKey, "acme-corp")

	// Values are looked up by walking UP the tree, so lookup is O(depth),
	// not O(1). Deep chains with hot lookups are a real cost.
	fmt.Printf("   requestID: %s\n", requestIDFrom(ctx))
	fmt.Printf("   tenant:    %v\n", ctx.Value(tenantKey))
	fmt.Printf("   missing:   %v (nil, not a panic)\n", ctx.Value(ctxKey(99)))
	fmt.Println("   ONLY for request-scoped metadata that crosses API boundaries:")
	fmt.Println("   trace IDs, auth principals, locale. NEVER for optional parameters —")
	fmt.Println("   those belong in the function signature where the compiler checks them")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 6. Detaching from cancellation (Go 1.21+)
// ----------------------------------------------------------------------------

func demoWithoutCancel() {
	fmt.Println("6. context.WithoutCancel — work that must outlive the request")

	reqCtx, cancel := context.WithCancel(context.Background())
	reqCtx = withRequestID(reqCtx, "req-7f3a")

	// Keeps the VALUES (trace id, tenant) but drops the cancellation signal.
	// Use for audit writes, metric flushes and cleanup that must still happen
	// after the client disconnects. Give it its own timeout — never unbounded.
	auditCtx := context.WithoutCancel(reqCtx)
	auditCtx, auditCancel := context.WithTimeout(auditCtx, time.Second)
	defer auditCancel()

	cancel() // the client hangs up

	fmt.Printf("   request ctx cancelled: %v\n", reqCtx.Err())
	fmt.Printf("   audit ctx still live:  err=%v, requestID=%s\n",
		auditCtx.Err(), requestIDFrom(auditCtx))
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 7. context.AfterFunc (Go 1.21+)
// ----------------------------------------------------------------------------

func demoAfterFunc() {
	fmt.Println("7. context.AfterFunc — run cleanup on cancellation")

	ctx, cancel := context.WithCancel(context.Background())
	cleaned := make(chan string, 1)

	// Registers a callback instead of dedicating a goroutine to <-ctx.Done().
	// The returned stop() unregisters it if you no longer need it.
	stop := context.AfterFunc(ctx, func() {
		cleaned <- "released connection back to the pool"
	})
	defer stop()

	cancel()
	fmt.Printf("   on cancel: %s\n", <-cleaned)
	fmt.Println()
}

func main() {
	fmt.Println("=== Context Patterns ===")
	fmt.Println()

	demoPropagation()
	demoTimeout()
	demoDeadlineInheritance()
	demoCause()
	demoValues()
	demoWithoutCancel()
	demoAfterFunc()

	fmt.Println("Context rules:")
	fmt.Println("  - ctx is the FIRST parameter, never a struct field")
	fmt.Println("  - defer cancel() immediately, always, no exceptions")
	fmt.Println("  - select on ctx.Done() in every blocking operation you write")
	fmt.Println("  - deadlines shrink down the tree; they never grow")
	fmt.Println("  - distinguish Canceled (client left) from DeadlineExceeded (too slow)")
	fmt.Println("  - WithValue is for request metadata only, not for parameters")
}
