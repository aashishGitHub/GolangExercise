# Go Examples

This folder contains practical, runnable examples covering type conversions, primitive types, and concurrency.

## Running the Examples

Navigate to this folder and run any example:

```bash
cd examples
go run 01-basic-conversions.go
```

Or run directly from the root:

```bash
go run examples/01-basic-conversions.go
```

## Examples Overview

### Type Conversions
| File | Description |
|------|-------------|
| `01-basic-conversions.go` | Introduction to custom types and basic conversions |
| `02-numeric-conversions.go` | Converting between different numeric types (int, float, etc.) |
| `03-custom-types-methods.go` | Custom types with methods (Temperature conversion) |
| `04-type-safety.go` | Demonstrates how custom types prevent bugs |
| `05-string-conversions.go` | String ↔ bytes, numbers ↔ strings |
| `06-practical-money.go` | Real-world example: Currency conversion |

### Primitive Data Types
| File | Description |
|------|-------------|
| `07-primitive-types.go` | Overview of all primitive data types in Go |
| `08-binary-representation.go` | How everything is numeric at the binary level |
| `09-invalid-data.go` | Examples of invalid or problematic data |
| `10-user-input-validation.go` | Validating user input properly |

### Concurrency Basics
| File | Description |
|------|-------------|
| `11-goroutines-basics.go` | Goroutine lifecycle, WaitGroup, loop-variable capture, panic isolation |
| `12-channels-select.go` | Buffered vs unbuffered, closing, select, timeouts, nil channels |
| `13-sync-primitives.go` | Mutex, RWMutex, Once, atomic, sync.Map, Cond, Pool |

### Goroutine Problems
| File | Description |
|------|-------------|
| `14-race-conditions.go` | Lost updates, four fixes, and a TOCTOU bug the race detector cannot see |
| `15-goroutine-leaks.go` | Four leak patterns, each measured with `runtime.NumGoroutine()` and fixed |
| `16-deadlocks.go` | AB-BA lock ordering, self-deadlock, WaitGroup and channel deadlocks |

### Concurrency Patterns
| File | Description |
|------|-------------|
| `17-worker-pool.go` | Bounded concurrency, cancellation, fail-fast, restoring order |
| `18-pipeline-fanin-fanout.go` | Pipeline stages, fan-out/fan-in, early exit, backpressure |
| `19-context-patterns.go` | Cancellation trees, deadline inheritance, Cause, WithoutCancel, AfterFunc |

### Distributed Systems Patterns
| File | Description |
|------|-------------|
| `20-distributed-patterns.go` | Semaphore, rate limiter, retry+backoff+jitter, circuit breaker, singleflight, hedged requests |
| `21-graceful-shutdown.go` | SIGTERM handling, in-flight draining, ordered component shutdown |

> **Note:** examples 14-16 deliberately demonstrate *broken* code alongside its fix.
> `14-race-conditions.go` is meant to be run twice — once plain to see the wrong
> number, once with `-race` to see exactly where. Examples 15 and 16 leave
> goroutines deliberately stuck; they use watchdogs so the program always exits.

## Quick Start

Try running all examples in sequence:

```bash
for file in *.go; do
    echo "Running $file..."
    go run "$file"
    echo ""
done
```

## Learning Path

### Beginner: Type Conversions
1. Start with `01-basic-conversions.go` to understand the fundamentals
2. Move to `02-numeric-conversions.go` for numeric type handling
3. Study `03-custom-types-methods.go` to learn about methods
4. Review `04-type-safety.go` to see the benefits
5. Practice with `05-string-conversions.go` for string handling
6. Apply knowledge with `06-practical-money.go` real-world scenario

### Intermediate: Understanding Primitives
7. Learn all types with `07-primitive-types.go`
8. Understand binary representation with `08-binary-representation.go`
9. See what can go wrong with `09-invalid-data.go`
10. Learn validation with `10-user-input-validation.go`

### Advanced: Concurrency
11. Start goroutines correctly with `11-goroutines-basics.go`
12. Learn channels and select with `12-channels-select.go`
13. Pick the right lock with `13-sync-primitives.go`
14. See what breaks with `14-race-conditions.go` (run it with `-race` too)
15. Learn why goroutines leak with `15-goroutine-leaks.go`
16. Learn how deadlocks form with `16-deadlocks.go`
17. Bound your concurrency with `17-worker-pool.go`
18. Compose stages with `18-pipeline-fanin-fanout.go`
19. Master cancellation with `19-context-patterns.go`
20. Build resilience with `20-distributed-patterns.go`
21. Exit cleanly with `21-graceful-shutdown.go`

## Notes

- All examples are standalone and can be run independently
- Each example includes comments explaining the concepts
- Examples demonstrate both correct and incorrect approaches
- See the main documentation in `/docs/type-conversions.md` for detailed explanations
- For concurrency, see [`/docs/concurrency.md`](../docs/concurrency.md) and the
  [concurrency cheat sheet](../docs/concurrency-cheatsheet.md)

