package main

import "fmt"

func main() {
	fmt.Println("=== Numeric Type Conversions ===\n")

	// Integer to Float
	var i int = 42
	var f float64 = float64(i)
	fmt.Printf("int %d → float64 %.2f\n", i, f)

	// Float to Integer (truncation happens!)
	var pi float64 = 3.14159
	var truncated int = int(pi)
	fmt.Printf("float64 %.5f → int %d (decimal truncated!)\n\n", pi, truncated)

	// Between integer types
	var small int8 = 127
	var large int64 = int64(small)
	fmt.Printf("int8 %d → int64 %d\n", small, large)

	// Unsigned to signed
	var unsigned uint = 42
	var signed int = int(unsigned)
	fmt.Printf("uint %d → int %d\n\n", unsigned, signed)

	// Demonstrating overflow (be careful!)
	var bigNum int64 = 300
	var tinyNum int8 = int8(bigNum) // Overflow!
	fmt.Printf("int64 %d → int8 %d (overflow occurred!)\n", bigNum, tinyNum)
}



