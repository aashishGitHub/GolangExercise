# Type Conversions Cheat Sheet

Quick reference for common type conversions in Go.

## 📋 Quick Reference

### Custom Type Definition

```go
type UserID int
type Speed float64
type Email string
```

### Basic Conversion Syntax

```go
Type(value)  // General syntax
```

## 🔢 Numeric Conversions

| From | To | Code | Result |
|------|-----|------|--------|
| `int` | `float64` | `float64(42)` | `42.0` |
| `float64` | `int` | `int(3.14)` | `3` ⚠️ truncates |
| `int` | `int64` | `int64(42)` | `42` |
| `int64` | `int` | `int(num)` | ⚠️ may overflow |
| `int` | `uint` | `uint(42)` | `42` |

### Example

```go
var i int = 42
var f float64 = float64(i)    // 42.0
var x float64 = 3.14
var y int = int(x)            // 3 (truncated)
```

## 📝 String Conversions

| From | To | Code | Result |
|------|-----|------|--------|
| `int` | ASCII char | `string(65)` | `"A"` |
| `int` | decimal string | `strconv.Itoa(65)` | `"65"` ✅ |
| `string` | `[]byte` | `[]byte("hi")` | `[104 105]` |
| `[]byte` | `string` | `string(bytes)` | `"hi"` |
| `string` | `int` | `strconv.Atoi("42")` | `42, nil` |
| `float64` | `string` | `strconv.FormatFloat(f, 'f', 2, 64)` | `"3.14"` |

### Example

```go
// ❌ WRONG - gives ASCII character
str := string(65)  // "A"

// ✅ CORRECT - gives decimal string
str := strconv.Itoa(65)  // "65"
```

## 🏷️ Custom Type Conversions

```go
type UserID int
type ProductID int

var user UserID = 123
var num int = int(user)        // UserID → int
var user2 UserID = UserID(456) // int → UserID

// Different custom types (same underlying type)
var prod ProductID = ProductID(user)  // Allowed but unsafe
```

## 🔒 Type Safety Example

```go
type UserID int
type ProductID int

func getUser(id UserID) { }

var user UserID = 123
var prod ProductID = 456

getUser(user)              // ✅ OK
getUser(prod)              // ❌ Compile error
getUser(UserID(prod))      // ✅ OK with explicit conversion
```

## 💡 Common Patterns

### Temperature Conversion

```go
type Celsius float64
type Fahrenheit float64

func (c Celsius) ToFahrenheit() Fahrenheit {
    return Fahrenheit(c*9/5 + 32)
}

var temp Celsius = 100
fmt.Println(temp.ToFahrenheit())  // 212
```

### Currency Conversion

```go
type USD float64
type EUR float64

func (u USD) ToEUR() EUR {
    return EUR(float64(u) * 0.85)
}

var price USD = 100
fmt.Println(price.ToEUR())  // 85
```

## ⚠️ Common Pitfalls

### 1. String Conversion Mistake

```go
// ❌ WRONG
num := 123
str := string(num)  // Gives Unicode char, not "123"

// ✅ CORRECT
str := strconv.Itoa(num)  // "123"
```

### 2. Float to Int Truncation

```go
var pi float64 = 3.14
var n int = int(pi)  // 3 (not 4!) - no rounding
```

### 3. Integer Overflow

```go
var big int64 = 300
var small int8 = int8(big)  // Overflow! Result: 44
```

### 4. Type Assertion vs Conversion

```go
// Type Conversion (for compatible types)
var i int = 42
var f float64 = float64(i)

// Type Assertion (for interfaces)
var x interface{} = "hello"
s := x.(string)  // Different syntax!
```

## 📦 strconv Package Quick Reference

```go
import "strconv"

// String to Number
i, err := strconv.Atoi("42")          // string → int
i64, err := strconv.ParseInt("42", 10, 64)  // string → int64
f, err := strconv.ParseFloat("3.14", 64)    // string → float64
b, err := strconv.ParseBool("true")   // string → bool

// Number to String
s := strconv.Itoa(42)                 // int → string
s := strconv.FormatInt(42, 10)        // int64 → string (base 10)
s := strconv.FormatFloat(3.14, 'f', 2, 64)  // float64 → string
s := strconv.FormatBool(true)         // bool → string
```

## 🎯 Best Practices

✅ **DO:**
- Always use explicit conversions
- Use custom types for domain concepts
- Use `strconv` for number↔string conversions
- Check for errors when parsing strings

❌ **DON'T:**
- Expect implicit conversions
- Use `string(num)` for number→string
- Ignore truncation when converting float→int
- Mix different custom types without conversion

## 🔗 Related Topics

- [Full Documentation](type-conversions.md)
- [Runnable Examples](../examples/)
- [Go strconv Package](https://pkg.go.dev/strconv)

---

**Remember:** Type conversions in Go are explicit and checked at compile time, which helps prevent bugs! 🐛✨

