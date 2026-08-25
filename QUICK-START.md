# Quick Start Guide

Welcome to GolangExercise! This guide will help you get started quickly.

## 🚀 Getting Started

### 1. Run Your First Example

```bash
cd examples
go run 01-basic-conversions.go
```

### 2. No Linter Errors! 🎉

This project is configured to **reduce linting noise** in learning files:

✅ **`.cursorignore`** - Tells Cursor to be lenient with example files  
✅ **`.golangci.yml`** - Configures Go linter for learning code  
✅ **`.vscode/settings.json`** - IDE settings optimized for learning  

You should see **fewer warnings** in:
- `examples/*.go` - Example files
- `go-workspace/*.go` - Your practice files

### 3. Browse Documentation

| Document | Purpose |
|----------|---------|
| [Primitive Types](docs/primitive-types.md) | Why everything is numeric |
| [Primitive Cheat Sheet](docs/primitive-types-cheatsheet.md) | Quick reference |
| [Type Conversions](docs/type-conversions.md) | Complete conversion guide |
| [Conversions Cheat Sheet](docs/type-conversions-cheatsheet.md) | Quick reference |
| [Concurrency](docs/concurrency.md) | Goroutines, channels, select, scheduler |
| [Synchronization](docs/synchronization.md) | Mutex, atomic, WaitGroup, Once |
| [Goroutine Problems](docs/goroutine-problems.md) | Leaks, races, deadlocks |
| [Concurrency Patterns](docs/concurrency-patterns.md) | Pools, pipelines, resilience |
| [Concurrency Cheat Sheet](docs/concurrency-cheatsheet.md) | Quick reference |
| [Managing Linters](docs/managing-linter-errors.md) | Reduce error noise |

## 📂 Project Structure

```
GolangExercise/
├── examples/           # 21 runnable examples
│   ├── 01-06          # Type conversions
│   ├── 07-10          # Primitive types
│   ├── 11-13          # Concurrency basics
│   ├── 14-16          # Goroutine problems (leaks, races, deadlocks)
│   ├── 17-19          # Concurrency patterns
│   └── 20-21          # Distributed systems patterns
├── docs/              # Learning documentation
├── go-workspace/      # Your practice area
└── README.md          # Full documentation
```

## 🎯 Learning Path

**Day 1-2: Type Conversions**
```bash
go run examples/01-basic-conversions.go
go run examples/02-numeric-conversions.go
go run examples/03-custom-types-methods.go
```

**Day 3-4: Understanding Types**
```bash
go run examples/07-primitive-types.go
go run examples/08-binary-representation.go
go run examples/09-invalid-data.go
```

**Day 5: Validation**
```bash
go run examples/10-user-input-validation.go
```

**Day 6-7: Concurrency Basics**
```bash
go run examples/11-goroutines-basics.go
go run examples/12-channels-select.go
go run examples/13-sync-primitives.go
```

**Day 8-9: What Goes Wrong**
```bash
go run examples/14-race-conditions.go          # see the wrong number
go run -race examples/14-race-conditions.go    # see exactly where
go run examples/15-goroutine-leaks.go
go run examples/16-deadlocks.go
```

**Day 10-11: Patterns**
```bash
go run examples/17-worker-pool.go
go run examples/18-pipeline-fanin-fanout.go
go run examples/19-context-patterns.go
```

**Day 12: Production Concerns**
```bash
go run examples/20-distributed-patterns.go
go run examples/21-graceful-shutdown.go
```

## 💡 If You Still See Linter Errors

### Option 1: Add to File Top (Per File)
```go
//nolint:all
package main
```

### Option 2: Add to Specific Line
```go
unusedVar := 42  //nolint:unused
```

### Option 3: Restart Cursor IDE
1. Close Cursor completely
2. Reopen your workspace
3. Linter config will reload

### Option 4: Check Configuration
```bash
# Verify .cursorignore exists
cat .cursorignore

# Verify .golangci.yml exists
cat .golangci.yml
```

## 🔧 Your Practice Workspace

Create your own learning files in `go-workspace/`:

```bash
cd go-workspace
# Edit main.go or create new files
# These files have reduced linting too!
```

## 📖 Need Help?

- **Linter issues?** → See [Managing Linter Errors](docs/managing-linter-errors.md)
- **Type questions?** → See [Primitive Types](docs/primitive-types.md)
- **Conversion help?** → See [Type Conversions Cheat Sheet](docs/type-conversions-cheatsheet.md)
- **Goroutine trouble?** → See [Goroutine Problems](docs/goroutine-problems.md)
- **Concurrency help?** → See [Concurrency Cheat Sheet](docs/concurrency-cheatsheet.md)

## ⚡ Quick Commands

```bash
# Run any example
cd examples
go run 08-binary-representation.go

# Run all examples
for f in *.go; do go run "$f"; echo ""; done

# Practice in workspace
cd go-workspace
go run main.go
```

## 🎓 Learning Tips

1. **Read the code** before running examples
2. **Modify examples** to see what happens
3. **Check documentation** when concepts are unclear
4. **Don't worry about warnings** in learning files - focus on concepts!
5. **Review linter errors** occasionally to learn best practices

## 🌟 Key Concepts

- **Everything is numeric** at the binary level
- **Types give meaning** to the numbers
- **Conversions must be explicit** in Go
- **Validate user input** in real applications
- **Learning code ≠ production code** - different standards!
- **Every goroutine needs a defined way to stop** - or it leaks
- **Channels for handoff, locks for in-place state**
- **Bound your concurrency** - never one goroutine per item

---

Happy Learning! 🚀

*For the full experience, see [README.md](README.md)*

