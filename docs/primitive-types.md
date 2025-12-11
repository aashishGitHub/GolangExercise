# Primitive Data Types in Go

## Overview

The statement "all primitive data types in Go are numeric" refers to the fact that **at the binary level**, all data is represented as numbers (bits). However, Go provides semantic types that give meaning to these numbers.

## Go's Primitive Types

### 1. Numeric Types (Truly Numeric)

#### Integers (Signed)
```go
int     // Platform dependent (32 or 64 bit)
int8    // -128 to 127
int16   // -32,768 to 32,767
int32   // -2,147,483,648 to 2,147,483,647
int64   // -9,223,372,036,854,775,808 to 9,223,372,036,854,775,807
```

#### Integers (Unsigned)
```go
uint    // Platform dependent (32 or 64 bit)
uint8   // 0 to 255 (alias: byte)
uint16  // 0 to 65,535
uint32  // 0 to 4,294,967,295
uint64  // 0 to 18,446,744,073,709,551,615
```

#### Floating Point
```go
float32 // ~1.18e-38 to ~3.4e38
float64 // ~2.23e-308 to ~1.8e308
```

#### Complex Numbers
```go
complex64  // Real and imaginary parts are float32
complex128 // Real and imaginary parts are float64
```

#### Special Numeric Types
```go
byte    // Alias for uint8 (represents raw bytes)
rune    // Alias for int32 (represents Unicode code points)
uintptr // Unsigned integer large enough to store a pointer
```

### 2. Boolean Type

```go
bool    // true or false
```

**Internally**: Represented as 1 (true) or 0 (false), so it's numeric at the binary level!

### 3. String Type

```go
string  // Sequence of bytes (UTF-8 encoded)
```

**Internally**: Strings are stored as a pointer to a byte array plus a length (both numeric values). Each character is a numeric byte value!

## Why "All Primitive Types Are Numeric"?

### At the Binary Level

Everything in computer memory is ultimately **bits** (0s and 1s), which are numeric:

```go
var b bool = true        // Stored as: 1
var c rune = 'A'         // Stored as: 65 (Unicode code point)
var s string = "Hi"      // Stored as: [72, 105] (byte values)
var i int = 42           // Stored as: 101010 (binary)
var f float64 = 3.14     // Stored as: IEEE 754 bit pattern
```

### Example: String is Numeric Under the Hood

```go
package main

import "fmt"

func main() {
    text := "Hello"
    
    // String is internally a sequence of numeric bytes
    bytes := []byte(text)
    fmt.Printf("String '%s' as bytes: %v\n", text, bytes)
    // Output: String 'Hello' as bytes: [72 101 108 108 111]
    
    // Each byte is a number!
    for i, b := range bytes {
        fmt.Printf("bytes[%d] = %d (binary: %08b)\n", i, b, b)
    }
}
```

### Example: Boolean is Numeric Under the Hood

```go
package main

import (
    "fmt"
    "unsafe"
)

func main() {
    var t bool = true
    var f bool = false
    
    // Booleans are stored as numeric values
    fmt.Printf("true = %t, size = %d bytes\n", t, unsafe.Sizeof(t))
    fmt.Printf("false = %t, size = %d bytes\n", f, unsafe.Sizeof(f))
    
    // Can see numeric representation through unsafe operations
    // (not recommended in production code!)
}
```

## Type as a Convention

### What This Means

When we declare a type in Go, we're telling the compiler **how to interpret** the underlying bits:

```go
var age int = 65        // Interpret as: decimal number 65
var grade byte = 65     // Interpret as: byte value 65 (also can be ASCII 'A')
var char rune = 65      // Interpret as: Unicode code point 65 ('A')
var isValid bool = true // Interpret as: boolean true (stored as 1)
```

**Same binary value, different interpretations!**

### Example: Same Data, Different Types

```go
package main

import "fmt"

func main() {
    // All represent the same underlying value (65)
    // but have different semantic meanings
    
    var num int = 65
    var letter rune = 65
    var byteVal byte = 65
    
    fmt.Printf("int: %d\n", num)           // 65
    fmt.Printf("rune: %c\n", letter)       // A
    fmt.Printf("byte: %d\n", byteVal)      // 65
    fmt.Printf("byte as char: %c\n", byteVal) // A
    
    // Same bits: 01000001
    // Different interpretations based on type!
}
```

## Invalid Data for a Given Type

### What This Means

Even though data is stored as numbers, it can be **semantically invalid** for the type:

### 1. Invalid UTF-8 in Strings

```go
package main

import "fmt"

func main() {
    // Create invalid UTF-8 sequence
    invalidBytes := []byte{0xFF, 0xFE, 0xFD}
    invalidString := string(invalidBytes)
    
    // The string contains "valid" bytes (numbers)
    // but they don't form valid UTF-8!
    fmt.Printf("String: %q\n", invalidString)
    fmt.Printf("Bytes: %v\n", []byte(invalidString))
    
    // This is valid at binary level but invalid semantically
}
```

### 2. Overflow/Underflow in Numeric Types

```go
package main

import "fmt"

func main() {
    var small int8 = 127  // Max value for int8
    
    // If we somehow set it to 128, it would overflow
    // (wraps around to -128)
    result := int8(128)   // Overflow!
    fmt.Printf("int8(128) = %d (overflow!)\n", result)
    
    // The binary representation is "valid"
    // but the value doesn't fit the type's range
}
```

### 3. Uninitialized Values (Zero Values)

```go
package main

import "fmt"

func main() {
    var i int       // 0
    var f float64   // 0.0
    var b bool      // false
    var s string    // ""
    
    fmt.Printf("int: %d\n", i)
    fmt.Printf("float64: %f\n", f)
    fmt.Printf("bool: %t\n", b)
    fmt.Printf("string: %q\n", s)
    
    // All have valid zero values
    // But may be "invalid" for your business logic!
}
```

### 4. Type Punning (Unsafe Operations)

```go
package main

import (
    "fmt"
    "unsafe"
)

func main() {
    // Reading bits as a different type
    var f float32 = 3.14
    
    // Reinterpret float32 bits as uint32
    ptr := unsafe.Pointer(&f)
    bits := *(*uint32)(ptr)
    
    fmt.Printf("float32: %f\n", f)
    fmt.Printf("As uint32 bits: %d (binary: %032b)\n", bits, bits)
    
    // The bits are the same, but interpretation differs!
    // This can lead to "invalid" data if misused
}
```

## Summary Table

| Type | Semantic Use | Internal Representation | Example "Invalid" Data |
|------|--------------|------------------------|------------------------|
| `int` | Integer numbers | Signed binary number | Overflow (exceeding range) |
| `uint` | Non-negative integers | Unsigned binary number | Underflow (negative value) |
| `float64` | Decimal numbers | IEEE 754 binary | NaN, Infinity |
| `bool` | True/false logic | 1 or 0 | Any value other than 0/1 (in unsafe code) |
| `string` | Text | Byte sequence with length | Invalid UTF-8 sequences |
| `rune` | Unicode character | 32-bit integer (code point) | Invalid Unicode code point |
| `byte` | Raw byte data | 8-bit unsigned integer | None (all values valid) |

## Key Takeaways

### 1. All Data is Numeric at Binary Level
Every piece of data in memory is stored as bits (numbers). Types give semantic meaning to these numbers.

### 2. Types are Conventions
Types tell the compiler and programmer how to interpret the underlying bits:
- `int`: interpret as signed integer
- `bool`: interpret as true/false
- `string`: interpret as UTF-8 text
- `rune`: interpret as Unicode character

### 3. Data Can Be Semantically Invalid
Even if the binary representation is valid, the data might not be valid for the type's intended use:
- Invalid UTF-8 in strings
- Out-of-range values after conversion
- NaN/Infinity in floating point
- Uninitialized pointers

### 4. User Input Requires Validation
When working with user input or binary data:
- Always validate that data fits the expected range
- Check for invalid UTF-8 in strings
- Handle overflow/underflow in conversions
- Validate that the data makes sense for your domain

## Practical Examples

See the examples folder for practical demonstrations:
- `07-primitive-types.go` - All primitive types
- `08-binary-representation.go` - How types are stored
- `09-invalid-data.go` - Invalid data examples
- `10-user-input-validation.go` - Validating user input

## Resources

- [Go Language Specification - Types](https://go.dev/ref/spec#Types)
- [Go by Example - Values](https://gobyexample.com/values)
- [Effective Go - Data](https://go.dev/doc/effective_go#data)



