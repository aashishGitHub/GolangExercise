package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println("=== Invalid Data Examples ===\n")

	// 1. OVERFLOW IN INTEGER CONVERSION
	fmt.Println("1. Integer Overflow:")
	var bigNum int64 = 300
	var tinyNum int8 = int8(bigNum) // Overflow!
	
	fmt.Printf("   Original (int64): %d\n", bigNum)
	fmt.Printf("   Converted (int8): %d (OVERFLOW!)\n", tinyNum)
	fmt.Printf("   Explanation: 300 doesn't fit in int8 range (-128 to 127)\n\n")

	// 2. UNDERFLOW IN UNSIGNED CONVERSION
	fmt.Println("2. Unsigned Underflow:")
	var negative int = -1
	var unsigned uint = uint(negative) // Underflow/wrap around!
	
	fmt.Printf("   Original (int): %d\n", negative)
	fmt.Printf("   Converted (uint): %d (WRAPPED AROUND!)\n", unsigned)
	fmt.Printf("   Explanation: -1 wraps to max uint value\n\n")

	// 3. FLOAT TO INT TRUNCATION
	fmt.Println("3. Float to Int Truncation:")
	var pi float64 = 3.14159
	var truncated int = int(pi)
	
	fmt.Printf("   Original (float64): %.5f\n", pi)
	fmt.Printf("   Converted (int): %d (TRUNCATED!)\n", truncated)
	fmt.Printf("   Explanation: Decimal part is lost, not rounded\n\n")

	// 4. SPECIAL FLOAT VALUES
	fmt.Println("4. Special Float Values (semantically invalid):")
	var inf float64 = math.Inf(1)
	var negInf float64 = math.Inf(-1)
	var nan float64 = math.NaN()
	
	fmt.Printf("   Infinity: %v\n", inf)
	fmt.Printf("   -Infinity: %v\n", negInf)
	fmt.Printf("   NaN (Not a Number): %v\n", nan)
	fmt.Printf("   These are 'valid' floats but semantically unusual\n\n")

	// 5. INVALID UTF-8 SEQUENCES
	fmt.Println("5. Invalid UTF-8 in Strings:")
	invalidBytes := []byte{0xFF, 0xFE, 0xFD} // Invalid UTF-8
	invalidString := string(invalidBytes)
	
	fmt.Printf("   Bytes: %v\n", invalidBytes)
	fmt.Printf("   As string: %q\n", invalidString)
	fmt.Printf("   Explanation: Valid bytes, but not valid UTF-8!\n\n")

	// 6. ZERO VALUES (may be invalid for business logic)
	fmt.Println("6. Zero Values (technically valid, but may be logically invalid):")
	var age int        // 0
	var price float64  // 0.0
	var name string    // ""
	var active bool    // false
	
	fmt.Printf("   age: %d (is 0 a valid age?)\n", age)
	fmt.Printf("   price: %.2f (is $0.00 valid for this product?)\n", price)
	fmt.Printf("   name: %q (is empty string a valid name?)\n", name)
	fmt.Printf("   active: %t (should default be false?)\n", active)
	fmt.Printf("   Explanation: These need validation based on business rules\n\n")

	// 7. OUT OF RANGE RUNE VALUES
	fmt.Println("7. Out of Range Rune (Unicode) Values:")
	var invalidRune rune = 0x110000 // Beyond valid Unicode range
	
	fmt.Printf("   Rune value: %d (U+%X)\n", invalidRune, invalidRune)
	fmt.Printf("   As character: %c\n", invalidRune)
	fmt.Printf("   Explanation: Valid Unicode is U+0000 to U+10FFFF\n\n")

	// 8. DIVISION BY ZERO (compile or runtime error)
	fmt.Println("8. Division by Zero:")
	fmt.Println("   Attempting: 10 / divisor (where divisor = 0)")
	
	var divisor int = 0
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("   ✅ PANIC RECOVERED: %v\n", r)
			fmt.Println("   Explanation: Integer division by zero causes runtime panic\n")
		}
	}()
	
	var result int = 10 / divisor
	fmt.Println(result) // This won't execute
}

