# Day 1a — Go core for a first-time Go reader

**Audience:** You can build software in another language, but Go's defaults, copying rules, and interface model are new. This is the on-ramp to [`day-1-go-core.md`](day-1-go-core.md), not a replacement for it.

**Read time:** ~50 minutes total, about 2–3 minutes per lesson. Split it into two or three sittings if that is what fits.

**In scope:** The same Day 1 concepts, in the same order: zero values, slices, maps, strings, pointers, embedding, interfaces, method sets, the nil-interface trap, assertions, generics, errors, `defer`, panic, testing, tooling, and the ControlPlane tie-in.

**Out of scope:** Concurrency, HTTP, Postgres, messaging, and the demo. Those are Days 2–6. This file also skips deep runtime and compiler internals.

**Source:** [`day-1-go-core.md`](day-1-go-core.md).

**Lesson IDs:** L1–L19, in source order. The source's grill keeps its Q1–Q12 labels; the map below links each grill question to the lesson that prepares it.

**Flow:** Each lesson is one short explanation: what is happening, one concrete example, why it matters, what to focus on and ignore, three Q&As, and one line to remember. Code blocks are short sketches, not complete programs.

> **Schedule note (Sat 14 Nov 2026):** If Day 1 was harder than expected, do not double up. Use this file as today's core, then run the Day 1 drills. Shift Day 2 to Sun 15 Nov, then run Day 3 Mon 16 and Day 4 Tue 17. Use Wed 18 for a compressed Day 5 rehearsal plus the Day 6 mock. The 19/20 Nov interview dates do not move.

---

## How to use this file

Read one lesson at a time. After each lesson, close the file and say the **Remember** line aloud. If you can say it in your own words, open the matching section of Day 1 and read the denser version. If you cannot, reread the example and move on; do not spend more than 10 minutes on one lesson.

Do not read this file and Day 1 cover to cover in the same sitting. This file gives you the shape; Day 1 gives you the interview-grade detail. Use the Day 1 drills after L2, L13–L14, and L17.

**Suggested 60-minute pass:** 5 minutes on the map and the five things below, 45 minutes on the lessons, 5 minutes on the recall sheet, then 5 minutes to pick one drill. The second hour is for the drill and the Day 1 grill.

**Minimum pass (20 minutes):** L1, L2, L6, L9, L10, L13, and L14. These are the five things plus the two ideas they depend on.

**If you only remember five things:** zero values, slice aliasing, pointer method sets, the nil-interface trap, and `errors.Is` versus `errors.As`.

---

## Question map

| ID  | Lesson                       | Day 1 section         | Grill |
| --- | ---------------------------- | --------------------- | ----- |
| L1  | Zero values                  | §1 Types, zero values | Q1    |
| L2  | Slices are a header          | §1 Slices             | Q3    |
| L3  | Nil vs empty slices, copy    | §1 Slices             | Q2    |
| L4  | Maps                         | §1 Maps               | Q4    |
| L5  | Strings, bytes, runes        | §1 Strings            | —     |
| L6  | Pointers and value semantics | §1 Pointers           | Q5    |
| L7  | Embedding                    | §2 Embedding          | —     |
| L8  | Interfaces                   | §2 Interfaces         | Q12   |
| L9  | Method sets                  | §2 Method sets        | Q5    |
| L10 | Nil-interface trap           | §2 Nil interface      | Q6    |
| L11 | Type assertions and switches | §2 Assertions         | Q7    |
| L12 | Generics                     | §2 Generics           | Q11   |
| L13 | Errors as values             | §3 Errors             | Q8    |
| L14 | `errors.Is` vs `errors.As`   | §3 Errors             | Q7    |
| L15 | `defer`                      | §3 `defer`            | Q10   |
| L16 | Panic and recover            | §3 Panic              | Q9    |
| L17 | Table-driven tests           | §4 Testing            | —     |
| L18 | Modules, commands, embed     | §4 Tooling            | —     |
| L19 | ControlPlane tie-in          | §7 Tie-in             | Q12   |

**The one idea behind Day 1.** Go is a small language with a few sharp edges. Most of Day 1 is one question: what gets copied, and who owns the original? Slices copy headers, pointers copy addresses, interfaces copy a type and a value, errors wrap other errors, and `defer` runs cleanup on the way out. Once you see the copies, the syntax stops being arbitrary.

---

## L1. Zero values — Go always gives you a usable default

In Go, a variable is never `undefined`. The moment you declare it, the language assigns a **zero value** that depends on its type. Numbers become `0`, booleans become `false`, strings become `""`, and pointers, slices, maps, channels, functions, and interfaces become `nil`. A struct gets the zero value of each field, and an array gets the zero value of each element. There is no separate "uninitialized" state to check.

```go
var count int      // 0
var name string    // ""
var ready bool     // false
var mu sync.Mutex  // unlocked and ready to use
```

This is why `var mu sync.Mutex` works without a constructor: the zero value of a mutex is a valid unlocked mutex. The same is true for `bytes.Buffer` and many well-designed config structs. Go programmers try to design types whose zero value is useful, because it removes a constructor and a nil check.

**Why it matters.** A zero value is not a missing value; it is a valid default. That makes Go code smaller, but it creates an ambiguity at API boundaries: JSON cannot tell `0` from "not provided," or `""` from "explicitly empty." When that distinction matters, use a pointer or a separate presence flag.

**Focus on:** every type has a defined zero value; design types so the zero value is usable.

**Ignore for now:** escape analysis, struct padding, and the difference between stack and heap allocation.

**Q: Is `nil` the same as a zero value?** `nil` is the zero value for pointer, slice, map, channel, function, and interface types. Numbers, strings, and booleans have different zero values.

**Q: Why is `var mu sync.Mutex` safe?** Its zero value is an unlocked mutex, so it is ready to lock without initialization.

**Q: Does a zero struct really have zero fields?** No; it has fields, and each field holds its own zero value.

**Remember:** in Go, "declared" already means "initialized to a usable default."

---

## L2. Slices are a three-word header, not the data

A slice feels like a list, but it is actually a small descriptor with three words: a pointer to a backing array, a length, and a capacity. The length is how many elements you can see; the capacity is how many elements fit before `append` has to allocate a new array. Copying a slice copies the descriptor, not the elements, so two slices can point at the same array.

```go
a := []int{1, 2, 3}
b := a[:2]        // same array; len=2, cap=3
b = append(b, 99) // spare capacity: writes a[2]
fmt.Println(a)    // [1 2 99]
```

Nothing was copied when `b` was created. `append` saw that `b` had room, wrote `99` into the shared array, and returned a new header. If `b` had been full, `append` would have allocated a fresh array and the aliasing would stop. This is why you always write `s = append(s, x)`: the returned header may point somewhere new.

Analogy: a slice is a window onto a row of lockers. The window can show two lockers or three, but it is still the same row until the row is full and a new row is built.

**Why it matters.** A function that receives a slice can modify the caller's data without returning anything, and an `append` can either share or allocate depending on capacity. That is a common source of surprising bugs and a favorite interview question.

**Focus on:** pointer, length, and capacity; `append` may share or allocate; always assign the result.

**Ignore for now:** the exact growth factor, `unsafe.Slice`, and allocator internals.

**Q: Does `b := a` copy the elements?** No; it copies the header, so both slices point at the same array.

**Q: When does `append` allocate?** When the slice has no spare capacity, meaning `len == cap`.

**Q$: Why does `s = append(s, x)` matter?** `append` may return a header pointing at a new array; ignoring it leaves `s` pointing at the old one.

**Remember:** a slice is a view, and `append` can write through the view.

---

## L3. Nil slice, empty slice, and copying

A nil slice (`var s []int`) has no backing array. An empty slice (`s := []int{}`) is non-nil and has length zero. Both have `len(s) == 0`, both work with `range`, and both accept `append`; the difference appears when you serialize them or compare them to `nil`. A nil slice marshals to JSON `null`, while an empty slice marshals to `[]`.

```go
var nilSlice []int
emptySlice := []int{}
fmt.Println(nilSlice == nil, emptySlice == nil) // true false

b, _ := json.Marshal(nilSlice)   // null
c, _ := json.Marshal(emptySlice) // []
```

`copy(dst, src)` is the explicit way to isolate data: it copies `min(len(dst), len(src))` elements and does not grow the destination. `s[:0]` is a different trick: it keeps the same backing array but sets the length to zero, which is useful for reusing a buffer. It also keeps references to the old elements, so clear them if they are large.

**Why it matters.** An API contract that promises an array should return `[]T{}`, not `null`, or clients will have to special-case the null. When you need two independent slices, `copy` is clearer and safer than hoping `append` allocates.

**Focus on:** both are length zero; nil marshals to `null`, empty to `[]`; `copy` copies the minimum; `s[:0]` reuses the array.

**Ignore for now:** the `slices`/`maps` helpers (`Clone`, `Contains`, `Sort`), zeroing large elements, and memory-retention profiling.

**Q: Can you append to a nil slice?** Yes; `append` allocates a backing array for you.

**Q: Which should a JSON API return?** An empty slice when the field is always an array; nil only when `null` has a real meaning.

**Q: Does `copy` grow the destination?** No; it copies only up to the destination's length.

**Remember:** nil means "no array"; empty means "an array with nothing in it."

---

## L4. Maps — readable when nil, writable only when initialized

A map is a hash table from keys to values. The zero value of a map is `nil`: reading from it is fine and returns the value type's zero value, ranging over it is fine and does nothing, but writing to it panics. You initialize a map with `make` or a literal before you write. The comma-ok form, `v, ok := m[key]`, is how you tell "missing" from "present with a zero value."

```go
m := make(map[string]int)
m["a"]++              // missing key reads 0, then writes 1
v, ok := m["missing"] // 0, false
delete(m, "a")
```

Iteration order is deliberately randomized, so code that depends on map order is already broken. Maps are also not safe for concurrent writes: concurrent writes are a data race and can crash the process. Use a mutex, `sync.Map` only for specific access patterns, or shard the map.

**Why it matters.** The nil-map write is a runtime panic that often appears only in the one code path where the map was not initialized. The concurrent-write crash is worse because it can happen under load, long after the code looked correct in a single-threaded test.

**Focus on:** nil map reads vs writes; comma-ok; randomized iteration; concurrent writes need synchronization.

**Ignore for now:** hash-table internals, Swiss-table details, and `sync.Map` internals.

**Q: What does `m["x"]` return when the key is missing?** The zero value of the map's value type.

**Q: Why use `v, ok`?** To distinguish a missing key from a key that stores the zero value.

**Q: Can two goroutines write a map safely?** No; use a mutex or another synchronization mechanism.

**Remember:** a nil map is readable and unwritable.

---

## L5. Strings are bytes; runes are characters

A Go string is an immutable sequence of bytes, usually UTF-8. That means `len(s)` counts bytes, not characters. A **rune** is one Unicode code point, stored as an `int32`. A `for range` loop over a string decodes UTF-8 and gives you the byte index of each rune and the rune itself; `s[i]` gives you a single byte.

```go
s := "héllo"
fmt.Println(len(s))                    // 6 bytes
fmt.Println(utf8.RuneCountInString(s)) // 5 runes
for i, r := range s {
    fmt.Println(i, r) // byte index, rune
}
```

Converting between `string` and `[]byte` copies the data. Building a string with `+=` in a loop also allocates repeatedly, so use `strings.Builder` when you are joining many pieces. Truncating a string by byte count can split a multi-byte character and produce invalid UTF-8.

**Why it matters.** API limits, database columns, and UI counters often disagree about whether they mean bytes or characters. If you assume `len` is a character count, a name with an accent or an emoji can exceed a limit you thought you checked, or a truncation can produce broken text.

**Focus on:** `len` counts bytes; `range` decodes runes; conversions copy; use `strings.Builder` for joins.

**Ignore for now:** UTF-8 encoding tables, Unicode normalization, and `utf8.Valid` internals.

**Q: Why is `len("héllo")` 6?** The `é` takes two bytes in UTF-8.

**Q: What does `s[0]` return?** A byte, not a character.

**Q: How do you iterate characters?** `for _, r := range s`.

**Remember:** strings are bytes; runes are characters; `range` is the decoder.

---

## L6. Pointers and value semantics

Go passes everything by value. When you pass a struct, the function gets a copy; when you pass a pointer, the function gets a copy of the address, and both copies point at the same object. Use a pointer when you need to mutate the caller's value, when `nil` means "absent," or when copying a large struct is wasteful. Use a value when the type is small, immutable-ish, or should not be shared.

```go
type Counter struct{ n int }

func (c *Counter) Inc()      { c.n++ }
func (c Counter) Value() int { return c.n }

c := &Counter{}
c.Inc()
fmt.Println(c.Value()) // 1
```

You do not need to return a pointer to a local variable just to keep it alive; Go's escape analysis moves it to the heap when needed. A nil pointer receiver can be valid, but only if the method checks for nil before using the receiver.

**Why it matters.** The choice between value and pointer changes whether a method mutates, whether a type satisfies an interface, and whether a nil check is part of the contract. In an API, `*T` can mean "optional" but also "may be nil"; be explicit about which one you intend.

**Focus on:** pass-by-value; pointer for mutation or absence; escape analysis handles lifetime.

**Ignore for now:** stack vs heap details, garbage-collector internals, and `unsafe.Pointer`.

**Q: Does passing a struct copy it?** Yes.

**Q: Does passing a pointer copy the struct?** No; it copies the address.

**Q: When is a nil receiver okay?** When the method explicitly handles nil; otherwise it panics.

**Remember:** Go copies values; pointers let two copies share one object.

---

## L7. Embedding is composition, not inheritance

Embedding puts one type inside another without giving it a field name. The outer type gets the embedded type's exported fields and methods **promoted**, so `u.Describe()` works even though `Describe` was defined on `Base`. This looks like inheritance, but it is not: `User` is not a `Base`, you cannot pass a `User` where a `Base` is expected, and there is no virtual dispatch.

```go
type Base struct{ ID string }
func (b Base) Describe() string { return b.ID }

type User struct {
    Base
    Name string
}

u := User{Base: Base{ID: "u1"}, Name: "Ada"}
fmt.Println(u.Describe()) // u1
```

Use a named field when the relationship should be explicit, such as `Base Base` or `Meta Metadata`. Embed when the outer type genuinely wants the full surface of the inner type, not just one field. If two embedded types have the same method name, the outer type has an ambiguity you must resolve.

**Why it matters.** Embedding is convenient, but it exposes behavior you may not have intended. It can also make a type look like a subtype in code review when the design is really composition.

**Focus on:** promotion; composition rather than inheritance; named fields when the relationship is "has a."

**Ignore for now:** method-ambiguity resolution rules, pointer embedding, and interface embedding.

**Q: Is `User` a subtype of `Base`?** No; it contains a `Base`.

**Q: What gets promoted?** Exported fields and methods of the embedded type.

**Q: When should you use a named field instead?** When the relationship is "has a" and you want to control the exposed surface.

**Remember:** embedding shares behavior; it does not create a family tree.

---

## L8. Interfaces are implicit, small, and consumer-owned

An interface is a set of method signatures. A type satisfies it by having all those methods; there is no `implements` keyword and no declaration at the type.

Small interfaces compose: `io.Reader` plus `io.Writer` is `io.ReadWriter`. The consumer should define the interface it needs, because the consumer knows what it is going to call. Return concrete types from constructors; accept interfaces at the boundaries where you need to swap implementations.

```go
type Reader interface{ Read(p []byte) (int, error) }
type Writer interface{ Write(p []byte) (int, error) }

type ReadWriter interface {
    Reader
    Writer
}
```

Do not create an interface only to mock it. Create one when there is a real seam: a database, a message broker, an HTTP client, or more than one implementation. A large interface is hard to satisfy and hard to fake, and it couples every implementation to methods it may not need.

**Why it matters.** Interfaces are the main tool for decoupling in Go, but overusing them adds indirection and makes the code harder to follow. The best interfaces are the ones a caller can describe in one sentence.

**Focus on:** implicit satisfaction; define at the consumer; keep interfaces small.

**Ignore for now:** interface value layout, `itab`, and the cost of interface conversions.

**Q: How does a type implement an interface?** By having all the required methods; no explicit declaration.

**Q: Where should the interface live?** In the package that consumes it.

**Q: Why not create an interface for every type?** It adds indirection without a real seam or a second implementation.

**Remember:** interfaces describe what a caller needs, not what a type is.

---

## L9$. Method sets and the pointer-receiver rule

$ A method with receiver `T` belongs to both `T` and `*T`. A method with receiver `*T` belongs only to `*T`.

That one sentence explains a common compile error: if `Inc` has a pointer receiver, then `Counter` does not satisfy an interface that requires `Inc`, but `&Counter{}` does. If a method mutates the receiver, use a pointer receiver. If one method needs a pointer, be consistent for that type.

```go
type Counter struct{ n int }

func (c *Counter) Inc() { c.n++ }

var _ interface{ Inc() } = &Counter{} // OK
// var _ interface{ Inc() } = Counter{} // compile error
```

You can still call `c.Inc()` on an addressable `Counter` variable; Go automatically takes the address for you. The method-set rule matters when the value is stored in an interface or returned from a constructor, because the automatic address-taking does not happen there.

**Why it matters.** This is a favorite interview question because it connects three things at once: method sets, interface satisfaction, and mutation. It also explains why a constructor that returns `T` may not satisfy an interface that expects `*T`.

**Focus on:** `T` versus `*T`; pointer receivers for mutation; pass `&v` when the interface needs a pointer method.

**Ignore for now:** addressability rules in detail, compiler-generated wrappers, and interface conversion cost.

**Q: Why does `Counter{}` not satisfy `Inc()`?** `Inc` is only in `*Counter`'s method set.

**Q: Can you call `c.Inc()` on a `Counter` variable?** Yes, if it is addressable; Go takes the address automatically.

**Q: When use a value receiver?** For small, read-only methods where copying is fine.

**Remember:** pointer-receiver methods belong to the pointer type.

---

## L10. The nil-interface trap

An interface value has two parts: the dynamic type of the value stored inside it, and the value itself. The interface is nil only when both parts are nil.

If you assign a typed nil pointer to an interface, the interface stores the type and a nil value, so the interface is not nil. This is why returning a typed nil pointer as an `error` is a bug.

```go
type MyError struct{}
func (e *MyError) Error() string { return "my error" }

var p *MyError    // nil pointer
var err error = p // interface holds type *MyError, value nil
fmt.Println(err == nil) // false
```

Analogy: a labeled box. The box has "MyError" printed on the outside even though it is empty inside; the interface checks the label, not just the contents.

**Why it matters.** A function can return "no error" as a non-nil error, so the caller's `if err != nil` branch runs and the program reports a failure that did not happen. The fix is to return an untyped `nil` explicitly when there is no error, not a typed nil variable.

**Focus on:** interface = type + value; a typed nil is not a nil interface; return explicit `nil`.

**Ignore for now:** the in-memory interface representation, `reflect.Value.IsNil`, and typed nils in generics.

**Q: Why is `err == nil` false?** The interface has a non-nil dynamic type even though the pointer is nil.

**Q: How do you fix it?** Return `nil` directly, not a typed nil pointer.

**Q: Does this affect non-error interfaces?** Yes, any interface holding a typed nil.

**Remember:** a nil pointer inside an interface is still a non-nil interface.

---

## L11. Type assertions and type switches

A type assertion extracts the concrete type from an interface. The safe form, `s, ok := v.(string)`, returns `ok == false` when the type is wrong. The unsafe form, `s := v.(string)`, panics. A type switch checks several concrete types in order and is the readable choice when one value can arrive in several shapes.

```go
func describe(v any) string {
    switch x := v.(type) {
    case string:
        return "string: " + x
    case int:
        return fmt.Sprintf("int: %d", x)
    default:
        return "other"
    }
}
```

For errors, prefer `errors.As` over a type assertion. A type assertion only looks at the outermost error; `errors.As` walks the wrap chain and finds the typed error inside. That is the difference between "this error is not a `*domain.Error`" and "this error wraps a `*domain.Error`."

**Why it matters.** Interfaces erase the concrete type, and assertions bring it back. The safe form keeps a bad input from crashing the process; the type switch keeps the handling readable when there are several possibilities.

**Focus on:** the comma-ok assertion; type switches; `errors.As` for errors.

**Ignore for now:** reflection, `unsafe`, and interface internals.

**Q: What happens if you assert the wrong type without `ok`?** It panics.

**Q: When use a type switch?** When several concrete types need different handling.

**Q: Why not assert an error type directly?** Wrapping hides the concrete type; `errors.As` walks the chain.

**Remember:** assert with `ok`; switch when there are several shapes.

---

## L12. Generics — same algorithm, different types

Generics let one function or type work over several types while keeping compile-time checks. The classic example is `Map`: the loop is identical for every element type, so the type parameter removes real duplication. Good uses are collections, type-safe pools, and result/option types. Bad uses are replacing a clear interface or inventing type-level abstractions the codebase does not need.

```go
func Map[T, U any](in []T, f func(T) U) []U {
    out := make([]U, 0, len(in))
    for _, v := range in {
        out = append(out, f(v))
    }
    return out
}
```

Go favors concrete code. A generic function adds a type parameter that every reader must understand, so use it when the algorithm is genuinely the same and the duplication is real. If only one concrete type exists, a plain function is simpler. If the point is behavior rather than type, an interface is clearer.

**Why it matters.** Generics remove duplication of code, not duplication of behavior. Used well, they make collections safer; used badly, they make simple code look like a type-system exercise.

**Focus on:** identical algorithm across types; constraints; the readability cost of type parameters.

**Ignore for now:** type sets, union constraints, and the limitations of generic methods.

**Q: When should you use generics?** When the algorithm is identical and only the element type changes.

**Q: When should you not?** When an interface expresses the behavior better, or there is only one concrete type.

**Q: Do generics replace interfaces?** No; they solve different problems.

**Remember:** generics remove duplication of code, not duplication of behavior.

---

## L13. Errors are values

In Go, an error is an ordinary value that implements `Error() string`. By convention, a function returns it as its last result.

There are three common shapes: a **sentinel** such as `ErrNotFound`, where identity matters; a **typed error** such as `*domain.Error`, which carries fields; and an **opaque error**, where the caller only logs it. Wrap with `%w` to preserve the original error in the chain; `%v` formats the message and throws the identity away.

```go
var ErrNotFound = errors.New("not found")

func find(id string) (Item, error) {
    if id == "" {
        return Item{}, fmt.Errorf("find %q: %w", id, ErrNotFound)
    }
    return Item{}, nil
}
```

Add context at each boundary: `fmt.Errorf("service: create tenant: %w", err)`. Do not log and return the same error at every layer; log once at the edge. Compare sentinel errors with `errors.Is`, never with `==` after wrapping, because the wrapped error is a different value.

**Why it matters.** An error carries two things: a message for humans and an identity for code. Wrapping adds context without losing the cause. If you use `%v`, the caller can no longer ask "is this not found?" and has to parse a string.

**Focus on:** sentinel vs typed vs opaque; `%w`; wrap at boundaries; log once.

**Ignore for now:** `errors.Join` details, custom `Is`/`As` methods, and stack traces.

**Q: Why not compare `err == ErrNotFound`?** Wrapping creates a new error value; `errors.Is` walks the chain.

**Q: What does `%w` do?** It wraps the original error and preserves it for `Is` and `As`.

**Q: Where should you log?** Once, at the edge of the request or process.

**Remember:** an error is data; wrap it to add context, not to hide it.

---

## L14. `errors.Is` versus `errors.As`

`errors.Is(err, target)` walks the wrap chain and compares each error with the target. Use it when identity matters, such as `ErrNotFound`.

`errors.As(err, &target)` walks the same chain looking for a specific type and assigns the first match to `target`. Use it when you need the fields inside a typed error. `errors.As` needs a pointer to the target variable, and the target must implement `error` or be an interface.

```go
var derr *domain.Error
if errors.As(err, &derr) {
    fmt.Println(derr.Code)
}
```

The difference is the question you are asking. `Is` asks "is this the same error?" `As` asks "can I get this type out of the chain?" A bare type assertion asks neither of those correctly, because it stops at the outermost error and ignores everything wrapped inside.

**Why it matters.** These two functions are the difference between "something failed" and "this exact thing failed, and here is its data." They also make wrapping useful instead of noisy: every layer can add context, and the boundary can still recover the cause and the typed details.

**Focus on:** `Is` for values, `As` for types; pass a pointer to the target; both walk the chain.

**Ignore for now:** custom `Unwrap() []error`, `errors.Join` trees, and `As` panic cases.

**Q: What does `errors.Is` compare?** The error and everything it wraps, against the target.

**Q: Why pass `&derr`?** `errors.As` needs a pointer so it can assign the found value.

**Q: Can `errors.As` find a wrapped type?** Yes; it walks the chain.

**Remember:** `Is` asks "is this the same?"; `As` asks "can I get that type out?"

---

## L15. `defer` — cleanup on the way out

`defer` schedules a function call to run when the surrounding function returns, including when it panics. Defers run in last-in, first-out order: the last one deferred runs first. The arguments to a deferred call are evaluated immediately, but the call itself happens later. Use it for cleanup that must run on every path: closing a response body, rolling back a transaction, or canceling a context.

```go
func f() (n int) {
    defer func() { n++ }()
    return 1
}
// f() returns 2
```

Named return values are variables in the function's scope. A deferred closure runs after the return values are assigned but before the function actually returns, so it can change them. That is useful for turning a panic into an error, but it can be surprising if you did not intend it.

**Why it matters.** Cleanup written with `defer` cannot be skipped by an early return or a panic. That is exactly what you want for files, locks, transactions, and contexts. The cost is that deferred calls accumulate until the function returns, so do not defer inside a long loop unless the function ends soon.

**Focus on:** LIFO order; runs on panic; arguments evaluated immediately; named returns are mutable.

**Ignore for now:** open-coded defers, defer overhead, and stack unwinding.

**Q: In what order do defers run?** Last in, first out.

**Q: Are deferred arguments evaluated now or later?** Now; only the call is delayed.

**Q: Why defer `Rollback` after `Commit`?** It is a safe no-op after a successful commit and guarantees rollback on an early return.

**Remember:** `defer` is cleanup that runs on the way out, in reverse order.

---

## L16. Panic and recover — for bugs, not expected failures

`panic` stops normal execution and unwinds the stack, running deferred functions along the way. `recover`, called inside a deferred function, can stop the panic and turn it into an error. Use panic for programmer bugs or truly unrecoverable state, not for expected failures such as "not found," validation, or a bad request. Recover only at a boundary, such as HTTP middleware or a worker delivery handler, so one bad request or message does not kill the process.

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

A panic in any goroutine that is not recovered kills the whole process. That is why a boundary recover is not optional in a server: a single nil dereference in a background goroutine can take down every request, not just the one that caused it.

**Why it matters.** Panic is a blunt instrument. Used for ordinary errors, it makes control flow hard to read and hard to test. Used at the right boundary, recover converts an unexpected crash into a 500 response or a nack/requeue, which is survivable and observable.

**Focus on:** panic for bugs; recover at boundaries; an unrecovered goroutine panic kills the process.

**Ignore for now:** `runtime.Goexit`, panic values, and stack-trace formatting.

**Q: Should validation errors panic?** No; return an error.

**Q: Where should recover live?** At the outermost boundary of a request or message handler.

**Q: Does recover work in a different goroutine?** No; each goroutine needs its own recover.

**Remember:** panic is for broken programs; recover keeps one broken request from killing the server.

---

## L17. Table-driven tests — the default shape

A table-driven test puts the cases in a slice of structs, then loops over them with `t.Run` to create named subtests. Adding a case becomes one line, and a failure names the case instead of a line number inside a loop. `t.Parallel` is safe only when the cases do not share mutable state. `t.Cleanup` registers teardown that runs even if the test fails.

```go
func TestTaskUpdateAllowed(t *testing.T) {
    tests := []struct {
        name          string
        current, next domain.TaskStatus
        want          bool
    }{
        {"accepted to in_progress", domain.TaskAccepted, domain.TaskInProgress, true},
        {"late in_progress", domain.TaskDone, domain.TaskInProgress, false},
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

Use `httptest` to test HTTP without a real port. Use real Postgres or RabbitMQ in integration tests when the behavior you are proving lives in the database or broker; do not mock the thing you are trying to prove.

**Why it matters.** The table documents the behavior and makes the edge cases visible. `t.Run` isolates failures, and the standard commands (`gofmt`, `go vet`, `go test -race`) catch formatting, suspicious code, and data races before a reviewer sees them.

**Focus on:** table plus `t.Run`; `-race`; integration tests at real boundaries.

**Ignore for now:** fuzzing, benchmarks, and mocking frameworks.

**Q: Why `t.Run`?** It creates a named subtest and isolates the failure output.

**Q: When is `t.Parallel` unsafe?** When cases share mutable state.

**Q: Why `-count=1`?** It disables the test cache so the tests actually run.

**Remember:** a table of cases plus named subtests is the default Go test shape.

---

## L18. Modules, commands, and `//go:embed`

`go.mod` pins the module path, the Go version, and the dependencies. `go mod tidy` adds missing requirements and removes unused ones. The commands a reviewer runs are short: `gofmt -l .` should print nothing, `go vet ./...` should print nothing, `go test ./... -count=1` disables the test cache, and `go test -race ./...` looks for data races (it needs a C toolchain on Windows). To run one test, use `go test ./internal/domain -run TestName -v`; to see coverage, add `-cover`. `internal/` prevents packages outside the module from importing a package.

```go
import _ "embed"

//go:embed schema.sql
var schemaSQL string
```

`//go:embed` copies a file into the binary at build time. The blank import of `embed` is what makes the directive legal when you are only embedding into a variable. In ControlPlane, this is how the schema ships inside the binary, so there is one boot path and no missing file at runtime.

**Why it matters.** These are the commands and boundaries that make a Go repository reproducible and reviewable. `go.mod` makes builds repeatable, `internal/` limits the public surface, and `embed` removes a class of deployment errors.

**Focus on:** `go.mod` and `go mod tidy`; `gofmt`, `go vet`, `go test`, `-race`; `internal/`; `//go:embed`.

**Ignore for now:** workspaces, vendoring, build tags, and module proxies.

**Q: What does `go mod tidy` do?** Adds missing dependencies and removes unused ones.

**Q: Why `internal/`?** It stops external modules from importing those packages.

**Q: What does `//go:embed` do?** It copies a file into the binary at build time.

**Remember:** the toolchain is part of code review, not an afterthought.

---

## L19. Where Day 1 shows up in ControlPlane

The Day 1 concepts are not abstract; they explain why ControlPlane is shaped the way it is. `*domain.Error` is a typed error carrying a stable code, and `errors.As` at the HTTP boundary maps that code to a status.

`store.DB` is an interface satisfied by both `*pgxpool.Pool` and `pgx.Tx`, so a store function can run standalone for a read or inside a caller-owned transaction. Functional options keep constructors readable, `//go:embed` ships the schema inside the binary, and the custom timestamp type fixes the wire format at milliseconds plus `Z`.

The domain package has no I/O, so its state machines and error vocabulary are testable without Postgres or RabbitMQ; `ParseTaskStatus` checks the map before lookup so an unknown status cannot silently become rank 0.

**Say this in the interview:**

> "The domain package has no I/O. That is deliberate: the state machines and error vocabulary are testable without Postgres or RabbitMQ, and the adapters map them to HTTP and AMQP. The service layer owns transaction boundaries; the store takes a `DB` interface so it does not care whether it is in a transaction."

**Why it matters.** The interviewer is not asking you to recite syntax. They are asking why the code is shaped that way. Connecting a language feature to a design decision is the difference between "I know Go" and "I can build with Go."

**Focus on:** domain has no I/O; typed errors map at the boundary; `DB` keeps transaction ownership in the service.

**Ignore for now:** memorizing every file path; the exact line numbers can wait.

**Q: Why does `store.DB` exist?** So one query works on a pool or a transaction.

**Q: Where does `errors.As` belong?** At the boundary that translates domain errors to HTTP or AMQP.

**Q: Why is the domain package pure?** So it can be tested without infrastructure.

**Remember:** every Go feature in the notebook exists to protect a design boundary.

---

## Coverage check

Every source section is accounted for; nothing is silently dropped.

| Source                          | Where it is covered | Status                  |
| ------------------------------- | ------------------- | ----------------------- |
| §1 Zero values                  | L1                  | Covered                 |
| §1 Slices                       | L2, L3              | Covered                 |
| §1 Maps                         | L4                  | Covered                 |
| §1 Strings, bytes, runes        | L5                  | Covered                 |
| §1 Pointers and value semantics | L6                  | Covered                 |
| §2 Embedding                    | L7                  | Covered                 |
| §2 Interfaces                   | L8                  | Covered                 |
| §2 Method sets                  | L9                  | Covered                 |
| §2 Nil-interface trap           | L10                 | Covered                 |
| §2 Type assertions and switches | L11                 | Covered                 |
| §2 Generics                     | L12                 | Covered                 |
| §3 Errors as values             | L13, L14            | Covered                 |
| §3 `defer`                      | L15                 | Covered                 |
| §3 Panic and recover            | L16                 | Covered                 |
| §4 Testing                      | L17                 | Covered                 |
| §4 Modules and tooling          | L18                 | Covered                 |
| §5 Active practice              | Day 1 drills        | Practice, not re-taught |
| §6 Grill Q1–Q12                 | Table below         | Covered                 |
| §7 ControlPlane tie-in          | L19                 | Covered                 |
| §8 End-of-day checkpoint        | Recall sheet below  | Covered                 |

**Source grill coverage**

| Grill | Question                                   | Lesson   |
| ----- | ------------------------------------------ | -------- |
| Q1    | Zero value and API design                  | L1       |
| Q2    | Nil slice vs empty slice                   | L3       |
| Q3    | Why `append` can mutate the caller's slice | L2       |
| Q4    | When a map write panics                    | L4       |
| Q5    | Value vs pointer receiver                  | L6, L9   |
| Q6    | Typed nil pointer vs nil error             | L10      |
| Q7    | `errors.Is` vs `errors.As`                 | L11, L14 |
| Q8    | `%w` vs `%v`                               | L13      |
| Q9    | When panic is acceptable                   | L16      |
| Q10   | Deferred function and named returns        | L15      |
| Q11   | When not to use generics                   | L12      |
| Q12   | Why `store.DB` instead of `*pgxpool.Pool`  | L8, L19  |

---

## One-line recall sheet

- Zero value: every type has a usable default; Go has no `undefined`.
- Slice: pointer, length, capacity; `append` may share the array or allocate.
- Nil slice reads and appends; nil map reads but panics on write.
- String length is bytes; `for range` gives runes; conversions copy.
- Pointer receivers live in `*T`'s method set, not `T`'s.
- A typed nil pointer inside an interface is not a nil interface.
- `%w` preserves error identity; `Is` compares values, `As` extracts types.
- `defer` is LIFO, runs on panic, and can change named returns.
- Panic is for bugs; recover at the request or message boundary.
- Table tests with `t.Run` and `-race` are the default.
- ControlPlane: the domain has no I/O, the store takes `DB`, and the service owns transactions.

**Next:** open [`day-1-go-core.md`](day-1-go-core.md) for the full drills and grill, then run the slice-aliasing drill first. It is the fastest way to see the copying rules with your own eyes.
