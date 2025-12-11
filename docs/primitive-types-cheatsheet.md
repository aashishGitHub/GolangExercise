# Primitive Data Types Cheat Sheet

Quick reference for Go's primitive data types.

## 📋 All Primitive Types

### Signed Integers

| Type | Size | Range | Use Case |
|------|------|-------|----------|
| `int8` | 1 byte | -128 to 127 | Small counters, flags |
| `int16` | 2 bytes | -32,768 to 32,767 | Port numbers, small values |
| `int32` | 4 bytes | -2.1B to 2.1B | Most integers, timestamps |
| `int64` | 8 bytes | -9.2E18 to 9.2E18 | Large numbers, IDs |
| `int` | Platform | 32 or 64 bit | General purpose (use this!) |

### Unsigned Integers

| Type | Size | Range | Use Case |
|------|------|-------|----------|
| `uint8` | 1 byte | 0 to 255 | Bytes, small positive values |
| `uint16` | 2 bytes | 0 to 65,535 | Network protocols |
| `uint32` | 4 bytes | 0 to 4.3B | IP addresses, hashes |
| `uint64` | 8 bytes | 0 to 1.8E19 | Large positive values |
| `uint` | Platform | 32 or 64 bit | Unsigned general purpose |
| `uintptr` | Platform | Pointer-sized | Pointer arithmetic (unsafe) |

### Floating Point

| Type | Size | Precision | Use Case |
|------|------|-----------|----------|
| `float32` | 4 bytes | ~6 decimal digits | Graphics, less precision OK |
| `float64` | 8 bytes | ~15 decimal digits | Financial, scientific (default) |

### Complex Numbers

| Type | Size | Components | Use Case |
|------|------|------------|----------|
| `complex64` | 8 bytes | 2 × float32 | DSP, signal processing |
| `complex128` | 16 bytes | 2 × float64 | Scientific computing |

### Boolean

| Type | Size | Values | Use Case |
|------|------|--------|----------|
| `bool` | 1 byte | `true`, `false` | Flags, conditions |

### String

| Type | Size | Content | Use Case |
|------|------|---------|----------|
| `string` | Variable | UTF-8 bytes | Text, immutable |

### Special Types

| Type | Alias For | Use Case |
|------|-----------|----------|
| `byte` | `uint8` | Raw bytes, ASCII |
| `rune` | `int32` | Unicode code points |

## 🔢 Why Everything is Numeric

At the **binary level**, all data is stored as numbers:

```go
var b bool = true       // Stored as: 1
var r rune = 'A'        // Stored as: 65
var s string = "Hi"     // Stored as: [72, 105]
```

## 💡 Quick Examples

### All Integer Types
```go
var i8 int8 = -128
var i16 int16 = 32767
var i32 int32 = 2147483647
var i64 int64 = 9223372036854775807
var i int = 42  // ✅ Use this for most cases!
```

### Unsigned vs Signed
```go
var signed int8 = -42    // ✅ Can be negative
var unsigned uint8 = 42  // ✅ Only positive
var wrong uint8 = -1     // ❌ Wraps to 255!
```

### Float Precision
```go
var f32 float32 = 3.14159265  // Loses precision
var f64 float64 = 3.14159265  // More precise
// Use float64 by default!
```

### Boolean as Number
```go
var t bool = true   // Internally: 1
var f bool = false  // Internally: 0
```

### String as Bytes
```go
text := "Hi"
bytes := []byte(text)  // [72, 105]
// Each character is a numeric byte!
```

### Byte vs Rune
```go
var b byte = 'A'  // 65 (8-bit)
var r rune = 'A'  // 65 (32-bit, handles Unicode)
var emoji rune = '😀'  // 128512 (needs 32 bits!)
```

## ⚠️ Common Pitfalls

### 1. Integer Overflow
```go
var small int8 = 127
small++  // Wraps to -128! ⚠️
```

### 2. Precision Loss
```go
var f float32 = 1.23456789
// Stored as: 1.2345679 (lost precision)
```

### 3. Float to Int Truncation
```go
var pi float64 = 3.14
var n int = int(pi)  // 3 (not 4!)
```

### 4. Unsigned Underflow
```go
var u uint = 0
u--  // Wraps to max uint! ⚠️
```

### 5. Invalid UTF-8
```go
invalid := []byte{0xFF, 0xFE}
text := string(invalid)  // Invalid UTF-8! ⚠️
```

## ✅ Best Practices

### Use Default Types
```go
// ✅ GOOD - Use defaults
var i int = 42
var f float64 = 3.14
var s string = "hello"

// ❌ AVOID - Unless you have a reason
var tiny int8 = 42
var imprecise float32 = 3.14
```

### Choose Based on Needs
```go
// Counters, indices
var count int

// Money (use decimal library!)
var price float64  // Or better: decimal.Decimal

// Flags
var isActive bool

// Text
var name string

// Raw data
var data []byte

// Unicode
var char rune
```

### Validate User Input
```go
// Always validate!
age, err := strconv.Atoi(input)
if err != nil {
    // Handle error
}
if age < 0 || age > 150 {
    // Invalid age
}
```

### Check for Overflow
```go
// Before conversion
var big int64 = 300
if big > math.MaxInt8 {
    // Would overflow!
}
```

## 📊 Zero Values

All types have a zero value when declared without initialization:

```go
var i int       // 0
var f float64   // 0.0
var b bool      // false
var s string    // ""
var p *int      // nil
```

## 🎯 Type Selection Guide

**Need whole numbers?** → `int`

**Need decimals?** → `float64`

**Need true/false?** → `bool`

**Need text?** → `string`

**Need raw bytes?** → `[]byte`

**Need Unicode character?** → `rune`

**Need to save memory AND know the range?** → `int8`, `int16`, etc.

**Need unsigned only?** → `uint`, `uint8`, etc.

**Need complex numbers?** → `complex128`

## 🔗 Related Documentation

- [Full Primitive Types Guide](primitive-types.md)
- [Type Conversions](type-conversions.md)
- [Examples Directory](../examples/)

---

**Remember:** At the binary level, everything is numeric. Types give semantic meaning to the bits! 🎯



