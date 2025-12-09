# Type Conversions in Go

## Overview

Converting between types in Go can be done with parentheses using the syntax: `TargetType(value)`

Go requires **explicit type conversion** - there is no automatic/implicit type conversion.

## Custom Type Definitions

You can create custom types based on existing types:

```go
type UserId int
type Speed float64
type Temperature float64
type Email string
```

### Benefits of Custom Types:
- ✅ **Type safety**: Prevents mixing up values
- ✅ **Clarity**: Makes code self-documenting
- ✅ **Method attachment**: Can add methods to custom types
- ✅ **Compile-time checks**: Catches errors early

## Basic Type Conversion Syntax

```go
// Syntax: TargetType(value)
userId := UserId(5)        // int → UserId
speed := Speed(88.3)       // float64 → Speed
```

## Common Type Conversions

### 1. Numeric Conversions

```go
// Integer to Float
var i int = 42
var f float64 = float64(i)     // 42.0

// Float to Integer (truncates decimal)
var x float64 = 3.14
var y int = int(x)             // 3 (decimal part lost)

// Between integer types
var a int = 100
var b int64 = int64(a)         // int → int64
var c uint = uint(a)           // int → uint
```

### 2. String Conversions

```go
// Integer to String (ASCII/Unicode)
var num int = 65
var char string = string(num)  // "A" (ASCII 65)

// String to Byte Slice
var text string = "hello"
var bytes []byte = []byte(text)

// Byte Slice to String
var data []byte = []byte{72, 101, 108, 108, 111}
var str string = string(data)  // "Hello"
```

⚠️ **Note**: `string(65)` gives "A", NOT "65". Use `strconv` package for number→string conversions:

```go
import "strconv"

var num int = 65
var str string = strconv.Itoa(num)  // "65"
```

### 3. Between Custom Types

```go
type Celsius float64
type Fahrenheit float64

var c Celsius = 100
var f Fahrenheit = Fahrenheit(c)  // Allowed (same underlying type)
```

## Complete Examples

### Example 1: Basic Type Conversions

```go
package main

import "fmt"

type UserId int
type Speed float64

func main() {
    // Creating custom type instances
    userId := UserId(5)
    speed := Speed(88.3)
    
    fmt.Println("UserId:", userId)    // 5
    fmt.Println("Speed:", speed)      // 88.3
    
    // Converting back to base types
    regularInt := int(userId)
    regularFloat := float64(speed)
    
    fmt.Printf("Regular int: %d\n", regularInt)       // 5
    fmt.Printf("Regular float: %.1f\n", regularFloat) // 88.3
}
```

### Example 2: Type Safety in Action

```go
package main

import "fmt"

type UserId int
type ProductId int

func getUserName(id UserId) string {
    return fmt.Sprintf("User_%d", id)
}

func main() {
    var user UserId = 123
    var product ProductId = 456
    
    fmt.Println(getUserName(user))  // ✅ Works
    
    // This would cause a compile error:
    // fmt.Println(getUserName(product))  // ❌ Error: cannot use product (type ProductId) as type UserId
    
    // Must explicitly convert:
    fmt.Println(getUserName(UserId(product)))  // ✅ Works with conversion
}
```

### Example 3: Custom Types with Methods

```go
package main

import "fmt"

type Celsius float64
type Fahrenheit float64

// Add method to convert Celsius to Fahrenheit
func (c Celsius) ToFahrenheit() Fahrenheit {
    return Fahrenheit(c*9/5 + 32)
}

// Add method to convert Fahrenheit to Celsius
func (f Fahrenheit) ToCelsius() Celsius {
    return Celsius((f - 32) * 5 / 9)
}

func main() {
    var temp Celsius = 100
    fmt.Printf("%.1f°C = %.1f°F\n", temp, temp.ToFahrenheit())
    
    var temp2 Fahrenheit = 212
    fmt.Printf("%.1f°F = %.1f°C\n", temp2, temp2.ToCelsius())
}
```

### Example 4: Practical Use Case - Money

```go
package main

import "fmt"

type USD float64
type EUR float64

const exchangeRate = 0.85  // 1 USD = 0.85 EUR

func (u USD) ToEUR() EUR {
    return EUR(float64(u) * exchangeRate)
}

func (e EUR) ToUSD() USD {
    return USD(float64(e) / exchangeRate)
}

func (u USD) String() string {
    return fmt.Sprintf("$%.2f", float64(u))
}

func (e EUR) String() string {
    return fmt.Sprintf("€%.2f", float64(e))
}

func main() {
    var price USD = 100.00
    fmt.Printf("Price in USD: %s\n", price)
    fmt.Printf("Price in EUR: %s\n", price.ToEUR())
    
    var euroPrice EUR = 85.00
    fmt.Printf("Price in EUR: %s\n", euroPrice)
    fmt.Printf("Price in USD: %s\n", euroPrice.ToUSD())
}
```

### Example 5: Numeric Type Conversions

```go
package main

import "fmt"

func main() {
    // Integer to Float
    var i int = 42
    var f float64 = float64(i)
    fmt.Printf("int %d → float64 %.2f\n", i, f)
    
    // Float to Integer (truncation)
    var pi float64 = 3.14159
    var truncated int = int(pi)
    fmt.Printf("float64 %.5f → int %d (truncated)\n", pi, truncated)
    
    // Between integer types
    var small int8 = 127
    var large int64 = int64(small)
    fmt.Printf("int8 %d → int64 %d\n", small, large)
    
    // Unsigned to signed
    var unsigned uint = 42
    var signed int = int(unsigned)
    fmt.Printf("uint %d → int %d\n", unsigned, signed)
}
```

### Example 6: String and Rune Conversions

```go
package main

import (
    "fmt"
    "strconv"
)

func main() {
    // Rune to string (character)
    var code int = 65
    var char string = string(code)
    fmt.Printf("ASCII %d → string '%s'\n", code, char)
    
    // Number to string (proper way)
    var num int = 65
    var str string = strconv.Itoa(num)
    fmt.Printf("int %d → string '%s' (using strconv)\n", num, str)
    
    // String to bytes
    var text string = "Hello"
    var bytes []byte = []byte(text)
    fmt.Printf("string '%s' → bytes %v\n", text, bytes)
    
    // Bytes to string
    var data []byte = []byte{72, 101, 108, 108, 111}
    var word string = string(data)
    fmt.Printf("bytes %v → string '%s'\n", data, word)
}
```

## Important Rules

### ✅ DO:
- Always use explicit type conversions
- Use custom types for domain concepts (UserId, Email, etc.)
- Use `strconv` package for number↔string conversions
- Consider the underlying type when converting

### ❌ DON'T:
- Expect implicit/automatic conversions
- Use `string(number)` to convert numbers to strings (use `strconv.Itoa`)
- Mix custom types without explicit conversion
- Forget that float→int truncates decimals

## Type Conversion vs Type Assertion

**Type Conversion**: Between compatible types
```go
var i int = 42
var f float64 = float64(i)  // Conversion
```

**Type Assertion**: For interfaces
```go
var i interface{} = "hello"
s := i.(string)  // Assertion
```

## Summary

| From | To | Syntax | Notes |
|------|-----|--------|-------|
| int | float64 | `float64(i)` | Safe conversion |
| float64 | int | `int(f)` | Truncates decimal |
| int | string | `strconv.Itoa(i)` | NOT `string(i)` |
| string | []byte | `[]byte(s)` | Common for I/O |
| []byte | string | `string(b)` | UTF-8 encoded |
| Custom | Base | `BaseType(c)` | Must match underlying type |

## Resources

- [Go by Example - Type Conversions](https://gobyexample.com/)
- [Go Documentation](https://go.dev/doc/)
- [Go Tour - Type Conversions](https://go.dev/tour/basics/13)

