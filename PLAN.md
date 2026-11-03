# Go plan (P3 — light): fullstack breadth, teach-level concurrency

Part of the career plan: [AI/career](https://github.com/aashishGitHub/AI/tree/main/career).
Goal: screen as fullstack, and review concurrent Go confidently. Not a deep specialisation.
Existing practice here: `examples/`, `ticket-booking/`, `offline-sync-app/`. Go DSA track: [DSnAlgoUsingJS/Go](https://github.com/aashishGitHub/DSnAlgoUsingJS/tree/master/Go).

## To do
- [ ] Worker pool with `context` cancellation + error group
- [ ] SSE endpoint with client disconnect handling (see the AI repo `fe-hands-on/server`)
- [ ] Explain the Go memory model basics: happens-before, why data races are bugs

## QnA (light)
**1. Goroutine vs OS thread?** Goroutines are cheap (small growable stacks), scheduled by the Go runtime onto OS threads (the M:N scheduler).

**2. Buffered vs unbuffered channel?** Unbuffered: send blocks until receive (synchronisation). Buffered: send blocks only when full (decoupling, bounded queue).

**3. When a mutex instead of a channel?** Protecting shared state → mutex. Passing ownership of data or signalling → channel.

**4. What is `context.Context` for?** Cancellation, deadlines and request-scoped values across API boundaries. Pass it as the first parameter; never store it in a struct.

**5. How do you avoid goroutine leaks?** Every goroutine needs an exit path: `ctx.Done()`, a closed channel or a finished input. Test with `goleak` (verify the package), or check with `runtime.NumGoroutine` in tests.

**6. `errgroup` — why?** Runs goroutines and returns the first error, cancelling the shared context. ⚠ It's in `golang.org/x/sync/errgroup`.

**7. Interfaces in Go?** Implicit satisfaction. Keep them small and define them where they're *consumed*. "Accept interfaces, return structs."

**8. Optimistic concurrency write (like compare-and-swap)?** Read the value with its version/CAS, modify, write with that CAS; on conflict, re-read and retry with exponential backoff + jitter and a max attempts limit.
