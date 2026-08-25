# GolangExercise
Learn golang daily

## 📚 Documentation

### Type System
- [Primitive Data Types](docs/primitive-types.md) - Understanding Go's primitive types and why they're all numeric
- [Primitive Types Cheat Sheet](docs/primitive-types-cheatsheet.md) - Quick reference for all primitive types
- [Type Conversions](docs/type-conversions.md) - Comprehensive guide to type conversions in Go
- [Type Conversions Cheat Sheet](docs/type-conversions-cheatsheet.md) - Quick reference for common conversions

### Concurrency
- [Concurrency](docs/concurrency.md) - Goroutines, channels, select, the scheduler and the memory model
- [Synchronization](docs/synchronization.md) - `sync` and `sync/atomic`: Mutex, RWMutex, WaitGroup, Once, Cond, Pool
- [Goroutine Problems](docs/goroutine-problems.md) - Leaks, data races, deadlocks and how to find them
- [Concurrency Patterns](docs/concurrency-patterns.md) - Worker pools, pipelines and distributed-systems patterns
- [Concurrency Cheat Sheet](docs/concurrency-cheatsheet.md) - Quick reference for everything above

### Development Setup
- [Managing Linter Errors](docs/managing-linter-errors.md) - How to reduce linting noise in learning code

## 🎯 Examples

The `examples/` folder contains practical, runnable examples:

### Type Conversions (01-06)
| Example | Description |
|---------|-------------|
| `01-basic-conversions.go` | Introduction to custom types and basic conversions |
| `02-numeric-conversions.go` | Converting between different numeric types |
| `03-custom-types-methods.go` | Custom types with methods (Temperature conversion) |
| `04-type-safety.go` | How custom types prevent bugs |
| `05-string-conversions.go` | String ↔ bytes, numbers ↔ strings |
| `06-practical-money.go` | Real-world currency conversion example |

### Primitive Data Types (07-10)
| Example | Description |
|---------|-------------|
| `07-primitive-types.go` | All primitive data types in Go |
| `08-binary-representation.go` | How everything is numeric at binary level |
| `09-invalid-data.go` | Invalid data examples and edge cases |
| `10-user-input-validation.go` | Validating user input properly |

### Concurrency Basics (11-13)
| Example | Description |
|---------|-------------|
| `11-goroutines-basics.go` | Goroutine lifecycle, WaitGroup, loop variables, panics |
| `12-channels-select.go` | Buffered vs unbuffered, closing, select, nil channels |
| `13-sync-primitives.go` | Mutex, RWMutex, Once, atomic, sync.Map, Cond, Pool |

### Goroutine Problems (14-16)
| Example | Description |
|---------|-------------|
| `14-race-conditions.go` | Lost updates, four fixes, and a TOCTOU bug `-race` can't see |
| `15-goroutine-leaks.go` | Four leak patterns, each measured and each fixed |
| `16-deadlocks.go` | AB-BA, self-deadlock, WaitGroup and channel deadlocks |

### Concurrency Patterns (17-19)
| Example | Description |
|---------|-------------|
| `17-worker-pool.go` | Bounded concurrency, cancellation, fail-fast, ordering |
| `18-pipeline-fanin-fanout.go` | Pipeline stages, fan-out/fan-in, backpressure |
| `19-context-patterns.go` | Cancellation trees, deadlines, Cause, WithoutCancel |

### Distributed Systems Patterns (20-21)
| Example | Description |
|---------|-------------|
| `20-distributed-patterns.go` | Semaphore, rate limiter, retry+backoff, circuit breaker, singleflight, hedging |
| `21-graceful-shutdown.go` | SIGTERM handling, draining, ordered component shutdown |

### Running Examples

```bash
cd examples
go run 01-basic-conversions.go
```

Or see the [examples README](examples/README.md) for more details.

## 🚀 Quick Start

1. Clone this repository
2. Navigate to the examples folder
3. Run any example to see Go concepts in action

```bash
cd examples
go run 03-custom-types-methods.go
```

## 📖 Learning Resources

- [Go by Example](https://gobyexample.com/)
- [Go Documentation](https://go.dev/doc/)
- [Go Learning Path](https://go.dev/learn/)
