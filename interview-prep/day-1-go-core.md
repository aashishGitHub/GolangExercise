# Day 1 — Go language core, errors, interfaces, testing

> **New to Go?** Read [`day-1a-go-core.md`](day-1a-go-core.md) first. It covers the same concepts in the same order, with slower explanations and one example per idea.

**Goal:** refresh the language model you will be judged on, not trivia. By the end you can explain
why the ControlPlane code is shaped the way it is, and write a small, correct Go test without an IDE.

**You should be able to answer:** zero values, slice aliasing, nil maps, value vs pointer receivers,
the nil-interface trap, `errors.Is` vs `errors.As`, `defer` order, and when to use generics.

---

## Timebox (120 min)

| Time      | Block                                                         |
| --------- | ------------------------------------------------------------- |
| 0:00–0:10 | Cold recall: write what Go gives you that TypeScript does not |
| 0:10–0:50 | Mechanisms below                                              |
| 0:50–1:25 | Active practice: three drills                                 |
| 1:25–1:45 | Grill: answer aloud, then open                                |
| 1:45–1:55 | Explain-back: five bullets, no notes                          |
| 1:55–2:00 | Log weak spots in [`progress.md`](progress.md)                |

---

## 1. Types, zero values, and the memory model you actually need

Go has no "undefined". Every variable starts at a **zero value**:

| Type                                              | Zero value                |
| ------------------------------------------------- | ------------------------- |
| `int`, `float64`                                  | `0`                       |
| `bool`                                            | `false`                   |
| `string`                                          | `""`                      |
| pointer, slice, map, channel, function, interface | `nil`                     |
| struct                                            | each field's zero value   |
| array                                             | each element's zero value |

This is why `var mu sync.Mutex` is usable, `var buf bytes.Buffer` is usable, and a struct with a
zero value can be a valid configuration. Design types so the zero value is useful when you can.

### Slices are a 3-word header

A slice is `{pointer, len, cap}`. Copying a slice copies the header, not the elements.

```go
a := []int{1, 2, 3}
b := a[:2]          // same backing array, len=2, cap=3
b = append(b, 99)   // cap is enough, so this writes into a[2]
fmt.Println(a)      // [1 2 99] — a changed
```

If `append` exceeds capacity, it allocates a new array and the aliasing stops. This is why
`append` results must always be assigned: `s = append(s, x)`.

Useful rules:

- `nil` slice is valid for `len`, `range`, `append`; `nil` marshals to JSON `null`.
- Empty slice `[]T{}` marshals to `[]`; use it when the API contract says array, not null.
- `copy(dst, src)` copies `min(len(dst), len(src))`.
- `s[:0]` reuses the backing array; it retains references to old elements, so clear if elements are large.
- `slices.Clone`, `slices.Contains`, `slices.Sort`, `maps.Clone` are in the standard library.

### Maps

```go
var m map[string]int // nil: reads and range are fine; writes panic
m = make(map[string]int)
m["a"]++             // zero value + 1
v, ok := m["a"]      // comma-ok distinguishes missing from zero
delete(m, "a")
```

- Iteration order is deliberately randomized.
- Maps are **not safe for concurrent writes**. Use a mutex, `sync.Map` only for specific access
  patterns, or shard.
- A nil map is useful as a read-only/empty value; initialize before writing.

### Strings, bytes, runes

Strings are immutable byte sequences, usually UTF-8.

```go
s := "héllo"
len(s)                    // 6 bytes, not 5 characters
utf8.RuneCountInString(s) // 5
for i, r := range s {     // i is the byte index, r is a rune
    _, _ = i, r
}
```

- `[]byte(s)` and `string(b)` copy.
- Build with `strings.Builder`, not `+=` in a loop.
- `s[i]` is a byte; `for range` gives runes.

### Pointers and value semantics

Go is pass-by-value. A pointer is also passed by value; it points at the same object.

- Use a pointer when you need mutation, `nil` as "absent", or to avoid copying a large struct.
- Use a value when the type is small, immutable-ish, or should not be shared.
- Do not return pointers to loop-local variables just to "be safe"; Go's escape analysis handles it.
- A `nil` pointer receiver can be valid, but only if the method handles it.

---

## 2. Structs, interfaces, method sets

### Embedding is composition, not inheritance

```go
type Base struct{ ID string }
func (b Base) Describe() string { return b.ID }

type User struct {
    Base
    Name string
}
// u.Describe() is promoted
```

Embedding promotes fields and methods. It does not create a subtype relationship. Prefer a named
field when you want to make the relationship explicit; embed when the outer type genuinely "is a"
or wants the full surface.

### Interfaces are implicit and should be small

```go
type Reader interface{ Read(p []byte) (int, error) }
type Writer interface{ Write(p []byte) (int, error) }
```

- A type implements an interface by having the methods; no `implements` keyword.
- Define interfaces at the **consumer**, not the producer. The consumer knows what it needs.
- Small interfaces compose: `io.ReadWriter` is `Reader + Writer`.
- Return concrete types from constructors; accept interfaces at boundaries.
- Do not create an interface only to mock it. Create it when there is a real seam or multiple
  implementations.

### Method sets: the pointer-receiver rule

```go
type Counter struct{ n int }
func (c *Counter) Inc() { c.n++ }

var _ interface{ Inc() } = &Counter{} // OK
// var _ interface{ Inc() } = Counter{} // compile error: Inc is not in Counter's method set
```

- `T` has methods with receiver `T`.
- `*T` has methods with receiver `T` and `*T`.
- If a method mutates, use `*T`.
- If a value implements an interface with a pointer-receiver method, pass `&v`.

### The nil-interface trap

An interface value is `(type, value)`. It is nil only when **both** are nil.

```go
type MyError struct{}
func (e *MyError) Error() string { return "my error" }

var p *MyError       // typed nil pointer
var err error = p    // interface holds type *MyError, value nil
fmt.Println(err == nil) // false
```

This is why returning a typed nil pointer as `error` is a bug. Return `nil` explicitly:

```go
func mayFail() error {
    var e *MyError
    if e == nil {
        return nil // not e
    }
    return e
}
```

### Type assertions and type switches

```go
if s, ok := v.(string); ok { ... }       // safe assertion
switch x := v.(type) {
case string: ...
case error: ...
default: ...
}
```

Use `errors.As` for errors, not a type assertion, because it unwraps.

### Generics: use them when the algorithm is the same

```go
func Map[T, U any](in []T, f func(T) U) []U {
    out := make([]U, 0, len(in))
    for _, v := range in {
        out = append(out, f(v))
    }
    return out
}
```

Good uses: collections, type-safe pools, generic result/option types. Bad uses: replacing a clear
interface, or inventing type-level abstractions the codebase does not need. Go favors concrete code.

---

## 3. Errors, defer, panic

### Errors are values

```go
var ErrNotFound = errors.New("not found")

func find(id string) (Item, error) {
    if id == "" {
        return Item{}, fmt.Errorf("find %q: %w", id, ErrNotFound)
    }
    return Item{}, nil
}

if errors.Is(err, ErrNotFound) { ... }
```

- Sentinel error: identity matters (`ErrNotFound`).
- Typed error: carries data (`*domain.Error` with `Code`, `Message`).
- Opaque error: callers only log it.
- Wrap with `%w` to preserve the chain; `%v` breaks `errors.Is/As`.
- `errors.Is` walks the chain comparing values; `errors.As` walks looking for a type.
- `errors.Join` (Go 1.20) combines multiple errors; `errors.Is` matches any.
- Add context at each boundary: `fmt.Errorf("service: create tenant: %w", err)`.
- Do not log and return the same error at every layer; log once at the edge.
- Compare sentinel errors with `errors.Is`, never `==` after wrapping.

### `errors.As` needs a pointer to the target type

```go
var derr *domain.Error
if errors.As(err, &derr) {
    fmt.Println(derr.Code)
}
```

`errors.As(err, &target)` where `target` is `*MyError`; pass `&target`. The target must implement
`error` (or be an interface).

### `defer` is LIFO and runs on panic

```go
func f() (n int) {
    defer func() { n++ }()
    return 1 // returns 2: deferred func modifies the named result
}
```

- `defer` runs when the function returns, including on panic.
- Arguments to a deferred call are evaluated immediately.
- Use `defer resp.Body.Close()`, `defer tx.Rollback(ctx)`, `defer cancel()`.
- A deferred `Rollback` after a successful `Commit` is a no-op in pgx; it is still the right pattern.
- Do not defer inside a long loop unless the function ends soon; defers accumulate.

### Panic and recover

Use `panic` for programmer bugs or truly unrecoverable state, not expected failures. Recover only at
a boundary (HTTP middleware, worker delivery handler) so one bad request/message does not kill the
process. A panic in any goroutine that is not recovered kills the whole process.

```go
func safe(fn func()) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("panic: %v", r)
        }
    }()
    fn()
    return nil
}
```

---

## 4. Testing and tooling

### Table-driven tests

```go
func TestTaskUpdateAllowed(t *testing.T) {
    tests := []struct {
        name          string
        current, next domain.TaskStatus
        want          bool
    }{
        {"accepted to in_progress", domain.TaskAccepted, domain.TaskInProgress, true},
        {"duplicate done", domain.TaskDone, domain.TaskDone, false},
        {"late in_progress", domain.TaskDone, domain.TaskInProgress, false},
        {"accepted to failed", domain.TaskAccepted, domain.TaskFailed, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := domain.TaskUpdateAllowed(tt.current, tt.next); got != tt.want {
                t.Fatalf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

- `t.Run` gives subtests and names.
- `t.Parallel` is safe only when cases do not share mutable state.
- `t.Cleanup` is for teardown; it runs even if the test fails.
- `httptest.NewRecorder` and `httptest.NewServer` test HTTP without a real port.
- Integration tests can use real Postgres/RabbitMQ when the behavior is in the database/broker
  (constraints, locks, confirms). Do not mock the thing you are trying to prove.

### Commands

```bash
gofmt -l .                 # should print nothing
go vet ./...               # should print nothing
go test ./... -count=1     # -count=1 disables the test cache
go test -race ./...        # data races; needs cgo/gcc
go test ./internal/domain -run TestTaskUpdateAllowed -v
go test ./... -cover
```

### Modules and embedding

```go
import _ "embed"

//go:embed schema.sql
var schemaSQL string
```

`go.mod` pins the language version and dependencies. `go mod tidy` removes unused requirements.
Build tags (`//go:build integration`) split slow tests from unit tests. `internal/` prevents external
imports.

---

## 5. Active practice (35 min)

### Drill 1 — slice aliasing (10 min)

Without running it, predict the output. Then run it and explain the difference.

```go
package main

import "fmt"

func main() {
    a := []int{1, 2, 3, 4}
    b := a[:3]
    b = append(b, 99)
    b[0] = 100
    fmt.Println("a:", a)
    fmt.Println("b:", b)

    c := make([]int, len(a))
    copy(c, a)
    c[0] = -1
    fmt.Println("a after copy:", a)
    fmt.Println("c:", c)
}
```

### Drill 2 — error taxonomy (10 min)

Write a small program with:

- `ErrNotFound` sentinel.
- A `ValidationError` struct with `Field` and `Message`.
- A function that wraps both with `%w`.
- A caller that uses `errors.Is` and `errors.As` and prints a different message for each.

Then answer: why not compare `err == ErrNotFound`?

### Drill 3 — table-driven test (15 min)

Write a table-driven test for `domain.ValidSlug` from ControlPlane
([`slug.go`](../../Avid/ControlPlane/internal/domain/slug.go)). Cover:

- `"acme"` → valid
- `"a"` → invalid (too short)
- `"Acme"` → invalid (uppercase)
- `"acme-"` → invalid (trailing hyphen)
- `"acme_corp"` → invalid (underscore)
- a 28-character slug → valid
- a 29-character slug → invalid

Run it with `go test ./internal/domain -run TestValidSlug -v` from the ControlPlane repo.

---

## 6. Grill (answer aloud before opening)

<details>
<summary><strong>Q1. What is a zero value, and why does it matter in API design?</strong></summary>

Every type has a defined zero value, so a variable is never undefined. It matters because a struct
with a useful zero value needs no constructor, and because `0`, `""`, and `nil` are ambiguous in JSON:
you cannot tell "absent" from "explicitly zero" without a pointer or a presence flag.

</details>

<details>
<summary><strong>Q2. Nil slice vs empty slice — what is the difference?</strong></summary>

Both have `len == 0` and work with `append`/`range`. A nil slice has no backing array and marshals to
JSON `null`; an empty slice marshals to `[]`. Use `[]T{}` when the API contract promises an array.

</details>

<details>
<summary><strong>Q3. Why can `append` mutate the caller's slice?</strong></summary>

A slice is a header pointing at a backing array. If the slice has spare capacity, `append` writes into
that array, which may be shared with another slice. If capacity is exceeded, it allocates a new array.
Always use the returned slice and copy when you need isolation.

</details>

<details>
<summary><strong>Q4. When does a map write panic?</strong></summary>

Writing to a nil map panics. Concurrent writes (or a write concurrent with a read) are a data race and
can panic with "concurrent map writes". Reads from a nil map are safe and return the zero value.

</details>

<details>
<summary><strong>Q5. Value receiver vs pointer receiver — how do you choose?</strong></summary>

Use a pointer receiver when the method mutates the receiver, when the struct is large, or when nil is a
meaningful state. Use a value receiver for small immutable values. Be consistent: if one method needs a
pointer, use pointers for that type's methods.

</details>

<details>
<summary><strong>Q6. Why is a typed nil pointer not equal to a nil error?</strong></summary>

An interface holds a type and a value. Assigning a nil `*MyError` to `error` stores the type
`*MyError`, so the interface is non-nil even though the pointer is nil. Return an untyped `nil`
explicitly.

</details>

<details>
<summary><strong>Q7. `errors.Is` vs `errors.As`?</strong></summary>

`errors.Is` compares an error against a value/sentinel through the wrap chain. `errors.As` searches
the chain for a specific type and assigns it to a target. Use `Is` for identity, `As` when you need
fields from a typed error.

</details>

<details>
<summary><strong>Q8. Why wrap with `%w` instead of `%v`?</strong></summary>

`%w` preserves the original error in the chain, so `errors.Is`/`errors.As` still work. `%v` formats
the message and discards the identity. Wrap at boundaries; do not wrap the same error five times with
no new information.

</details>

<details>
<summary><strong>Q9. When is `panic` acceptable?</strong></summary>

For programmer errors and impossible states (nil dependency, broken invariant), or during init. Not for
expected I/O failures, validation, or "not found". Recover at process boundaries (HTTP middleware,
message handler) so one bad input does not take down the service.

</details>

<details>
<summary><strong>Q10. Why can a deferred function change a named return value?</strong></summary>

Named results are variables in the function's scope. A deferred closure runs after the return values
are assigned but before the function actually returns, so it can modify them. This is useful for
recovering a panic into an error, but can be surprising.

</details>

<details>
<summary><strong>Q11. When should you not use generics?</strong></summary>

When an interface expresses the behavior more clearly, when there is only one concrete type, or when
the generic abstraction adds type parameters without removing real duplication. Go favors simple,
concrete code; generics are a tool, not a style.

</details>

<details>
<summary><strong>Q12. Why does ControlPlane define `store.DB` instead of taking `*pgxpool.Pool` everywhere?</strong></summary>

`*pgxpool.Pool` and `pgx.Tx` both satisfy `Exec`, `Query`, and `QueryRow`. A store function that takes
`DB` can run standalone for a read or inside a caller-owned transaction, and the store never decides
where the transaction boundary is. That keeps transaction ownership in the service layer.

</details>

---

## 7. ControlPlane tie-in — where Day 1 shows up

| Concept                     | File                                                                                                   | Why it matters                                                                         |
| --------------------------- | ------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- |
| Typed domain error          | [`internal/domain/errors.go`](../../Avid/ControlPlane/internal/domain/errors.go)                       | `*domain.Error` carries a stable code; HTTP maps it to a status.                       |
| `errors.As` at the boundary | [`internal/httpapi/errors.go`](../../Avid/ControlPlane/internal/httpapi/errors.go)                     | Unknown errors become `internal_error`; clients never see SQL details.                 |
| `DB` interface              | [`internal/store/pool.go`](../../Avid/ControlPlane/internal/store/pool.go)                             | One query works on a pool or a transaction.                                            |
| Functional options          | [`internal/service/tenant.go`](../../Avid/ControlPlane/internal/service/tenant.go)                     | `service.New(pool, service.WithNotifier(...))` avoids a long constructor.              |
| `//go:embed`                | [`internal/store/pool.go`](../../Avid/ControlPlane/internal/store/pool.go)                             | Schema ships inside the binary; one boot path.                                         |
| Custom timestamp            | [`internal/domain/timestamp.go`](../../Avid/ControlPlane/internal/domain/timestamp.go)                 | The wire format is fixed at milliseconds + `Z`, not Go's default.                      |
| Closed vocabulary           | [`internal/domain/statemachine.go`](../../Avid/ControlPlane/internal/domain/statemachine.go)           | `ParseTaskStatus` checks the map before lookup; unknown status must not become rank 0. |
| Table tests                 | [`internal/domain/statemachine_test.go`](../../Avid/ControlPlane/internal/domain/statemachine_test.go) | State transitions are cheap to test exhaustively.                                      |

**Say this in the interview:** "The domain package has no I/O. That is deliberate: the state machines
and error vocabulary are testable without Postgres or RabbitMQ, and the adapters map them to HTTP and
AMQP. The service layer owns transaction boundaries; the store takes a `DB` interface so it does not
care whether it is in a transaction."

---

## 8. End-of-day checkpoint

Write these from memory:

- [ ] A slice is `{ptr, len, cap}`; `append` may share or allocate.
- [ ] Nil map read is fine; nil map write panics.
- [ ] Pointer receiver methods are only in `*T`'s method set.
- [ ] A typed nil in an interface is not a nil interface.
- [ ] `%w` preserves identity; `errors.Is` for values, `errors.As` for types.
- [ ] `defer` is LIFO; named returns are mutable by defers.
- [ ] Table tests + `t.Run` + `-race` are the default.

**If you only remember five things:** zero values, slice aliasing, pointer method sets, nil-interface
trap, `errors.Is/As`.

**Next:** [`day-2-concurrency.md`](day-2-concurrency.md).
