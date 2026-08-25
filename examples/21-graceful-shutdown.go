package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ============================================================================
// GRACEFUL SHUTDOWN
//
// In Kubernetes (and most schedulers) a pod termination looks like this:
//   1. the pod is removed from Service endpoints  (traffic starts draining)
//   2. SIGTERM is delivered to PID 1
//   3. ... terminationGracePeriodSeconds (default 30s) ...
//   4. SIGKILL — no cleanup, no flush, no goodbye
//
// Your job is to finish in step 3. A process that ignores SIGTERM drops every
// in-flight request on the floor every single deploy. Users see 502s and your
// error budget pays for it.
//
// THE SHUTDOWN SEQUENCE (order matters):
//   1. stop accepting NEW work        (fail readiness, stop the listener)
//   2. finish IN-FLIGHT work          (bounded by a shutdown deadline)
//   3. stop background workers        (cancel their context)
//   4. release resources             (close DB pools, flush buffers/traces)
//
// The hard-won detail: shutdown itself needs a TIMEOUT. "Wait for everything
// to finish" turns one stuck request into a SIGKILL that skips steps 3 and 4.
// ============================================================================

// ----------------------------------------------------------------------------
// A toy server with in-flight tracking
// ----------------------------------------------------------------------------

type Server struct {
	// mu makes "check the gate, then register" a SINGLE atomic step.
	//
	// An atomic.Bool gate is NOT enough here, and the reason is subtle:
	// sync.WaitGroup forbids an Add that takes the counter up from zero from
	// running concurrently with Wait. With only an atomic flag, a request can
	// pass the check, get descheduled, and call Add while Shutdown is already
	// inside Wait — a genuine data race that `go run -race` reports. The
	// mutex closes that window: once accepting=false is published under mu,
	// no further Add can start, so Wait is safe.
	mu        sync.Mutex
	accepting bool           // readiness gate: false = reject new work
	inFlight  sync.WaitGroup // counts requests currently being served

	handled  atomic.Int64
	rejected atomic.Int64
}

func NewServer() *Server {
	return &Server{accepting: true}
}

var errNotAccepting = errors.New("server is shutting down")

// Handle serves one request. The gate check and the WaitGroup.Add happen
// together under mu, so Shutdown can trust the count it waits on.
func (s *Server) Handle(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	if !s.accepting {
		s.mu.Unlock()
		s.rejected.Add(1)
		return errNotAccepting
	}
	s.inFlight.Add(1) // registered before Shutdown can possibly call Wait
	s.mu.Unlock()

	defer s.inFlight.Done()

	select {
	case <-time.After(d):
		s.handled.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown drains in-flight work, bounded by ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	// 1. stop accepting new work. After this critical section every future
	//    Handle takes the reject path, so the counter can only go down.
	s.mu.Lock()
	s.accepting = false
	s.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		s.inFlight.Wait() // 2. wait for in-flight work
	}()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		// Deadline hit with requests still running. Report it — this is a
		// real signal that your grace period or your timeouts are wrong.
		return fmt.Errorf("drain incomplete: %w", ctx.Err())
	}
}

// ----------------------------------------------------------------------------
// Background workers that must also stop
// ----------------------------------------------------------------------------

type Worker struct {
	name  string
	ticks atomic.Int64
}

func (w *Worker) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return // 3. background workers stop on cancellation
		case <-ticker.C:
			w.ticks.Add(1)
		}
	}
}

// ----------------------------------------------------------------------------
// 1. The full sequence
// ----------------------------------------------------------------------------

func demoFullSequence() {
	fmt.Println("1. Full shutdown sequence")

	srv := NewServer()

	// Background workers get their own context so we control them separately
	// from request handling — they must outlive the drain, then stop.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	workers := []*Worker{{name: "metrics-flusher"}, {name: "cache-refresher"}}

	var workerWG sync.WaitGroup
	for _, w := range workers {
		workerWG.Add(1)
		go func(w *Worker) {
			defer workerWG.Done()
			w.Run(workerCtx, 10*time.Millisecond)
		}(w)
	}

	// Simulate live traffic: 10 requests of varying duration.
	var trafficWG sync.WaitGroup
	for i := 0; i < 10; i++ {
		trafficWG.Add(1)
		go func(n int) {
			defer trafficWG.Done()
			_ = srv.Handle(context.Background(), time.Duration(10+n*5)*time.Millisecond)
		}(i)
	}

	time.Sleep(20 * time.Millisecond) // let some requests get in flight
	fmt.Println("   -- SIGTERM received --")

	// The grace period. Real value: slightly LESS than the scheduler's
	// terminationGracePeriodSeconds, so you finish before SIGKILL.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()

	// A late request arriving DURING the drain must be refused, not queued.
	// It fires 5ms in, by which time Shutdown has already closed the gate.
	lateResult := make(chan error, 1)
	go func() {
		time.Sleep(5 * time.Millisecond)
		lateResult <- srv.Handle(context.Background(), time.Millisecond)
	}()

	err := srv.Shutdown(shutdownCtx) // steps 1 + 2
	fmt.Printf("   drain: err=%v after %v\n", err, time.Since(start).Round(10*time.Millisecond))

	lateErr := <-lateResult

	stopWorkers() // step 3
	workerWG.Wait()

	trafficWG.Wait()

	// step 4 would be: db.Close(), tracer.Shutdown(ctx), logger.Sync() ...
	fmt.Printf("   handled=%d rejected=%d\n", srv.handled.Load(), srv.rejected.Load())
	fmt.Printf("   late request during drain -> %v (refused: %t)\n",
		lateErr, errors.Is(lateErr, errNotAccepting))
	for _, w := range workers {
		fmt.Printf("   worker %-16s ticked %d times, then stopped\n", w.name, w.ticks.Load())
	}
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 2. When the drain deadline is too short
// ----------------------------------------------------------------------------

func demoDrainTimeout() {
	fmt.Println("2. Drain deadline exceeded — the case you must handle")

	srv := NewServer()

	go func() { _ = srv.Handle(context.Background(), 500*time.Millisecond) }() // a stuck request
	time.Sleep(10 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := srv.Shutdown(shutdownCtx)

	fmt.Printf("   after %v: %v\n", time.Since(start).Round(10*time.Millisecond), err)
	fmt.Printf("   is DeadlineExceeded: %t\n", errors.Is(err, context.DeadlineExceeded))
	fmt.Println("   log this and exit non-zero: it means requests WERE dropped")
	fmt.Println("   fix by making per-request timeouts shorter than the grace period")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 3. Real signal handling
// ----------------------------------------------------------------------------

func demoSignalHandling() {
	fmt.Println("3. signal.NotifyContext — the real entry point")

	// This is the whole idiom. It returns a context cancelled on the first
	// matching signal, so cancellation flows through your existing ctx tree.
	//
	//   ctx, stop := signal.NotifyContext(context.Background(),
	//       os.Interrupt, syscall.SIGTERM)
	//   defer stop()
	//   <-ctx.Done()   // blocks until Ctrl-C or SIGTERM
	//
	// SIGKILL and SIGSTOP cannot be caught — that is the point of the grace
	// period: it is the only window you get.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Deliver SIGTERM to ourselves so the demo exercises the real path
	// instead of just describing it.
	go func() {
		time.Sleep(20 * time.Millisecond)
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			return
		}
		_ = p.Signal(syscall.SIGTERM)
	}()

	start := time.Now()
	<-ctx.Done()

	fmt.Printf("   caught SIGTERM after %v, ctx.Err()=%v\n",
		time.Since(start).Round(10*time.Millisecond), ctx.Err())

	// stop() restores default handling. A SECOND Ctrl-C should hard-exit —
	// operators expect an escape hatch when a graceful shutdown wedges.
	stop()
	fmt.Println("   stop() restored default disposition: a second signal now kills us")
	fmt.Println()
}

// ----------------------------------------------------------------------------
// 4. Coordinating several components
// ----------------------------------------------------------------------------

// Component is anything with a lifecycle: an HTTP server, a Kafka consumer,
// a gRPC server, a DB pool.
type Component struct {
	Name         string
	ShutdownTime time.Duration
	Fails        bool
}

func (c *Component) Shutdown(ctx context.Context) error {
	select {
	case <-time.After(c.ShutdownTime):
		if c.Fails {
			return fmt.Errorf("%s: flush failed", c.Name)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", c.Name, ctx.Err())
	}
}

func demoComponents() {
	fmt.Println("4. Shutting down several components at once")

	components := []*Component{
		{Name: "http-server", ShutdownTime: 20 * time.Millisecond},
		{Name: "kafka-consumer", ShutdownTime: 40 * time.Millisecond},
		{Name: "db-pool", ShutdownTime: 10 * time.Millisecond},
		{Name: "trace-exporter", ShutdownTime: 30 * time.Millisecond, Fails: true},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()

	// Shut down in PARALLEL where components are independent: total time is
	// the slowest one, not the sum. Order them explicitly only where there is
	// a real dependency (drain the consumer BEFORE closing the DB it writes to).
	errCh := make(chan error, len(components))
	var wg sync.WaitGroup
	for _, c := range components {
		wg.Add(1)
		go func(c *Component) {
			defer wg.Done()
			if err := c.Shutdown(ctx); err != nil {
				errCh <- err
			}
		}(c)
	}
	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	// errors.Join (Go 1.20+) reports EVERY failure, not just the first.
	// During shutdown you want the full picture in one log line.
	joined := errors.Join(errs...)
	fmt.Printf("   all components stopped in %v (slowest, not the sum)\n",
		time.Since(start).Round(10*time.Millisecond))
	fmt.Printf("   errors: %v\n", joined)
	fmt.Println()
}

func main() {
	fmt.Println("=== Graceful Shutdown ===")
	fmt.Println()

	demoFullSequence()
	demoDrainTimeout()
	demoSignalHandling()
	demoComponents()

	fmt.Println("Shutdown checklist:")
	fmt.Println("  1. catch SIGTERM and SIGINT via signal.NotifyContext")
	fmt.Println("  2. fail readiness FIRST so the LB stops sending traffic")
	fmt.Println("  3. stop accepting, then drain in-flight with a deadline")
	fmt.Println("  4. cancel background workers after the drain, not before")
	fmt.Println("  5. close resources last: DB pools, tracers, log flushes")
	fmt.Println("  6. bound EVERY step; never wait forever")
	fmt.Println("  7. let a second signal hard-exit")
	fmt.Println()
	fmt.Println("For net/http this is mostly built in:")
	fmt.Println("  srv.Shutdown(ctx)  — stops listeners, drains idle+active conns")
	fmt.Println("  it returns ctx.Err() if the deadline passes first")
}
