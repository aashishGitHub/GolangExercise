package main

import "fmt"

func main() {
	fmt.Println("=== All Primitive Data Types in Go ===\n")

	// 1. SIGNED INTEGERS
	fmt.Println("1. Signed Integers:")
	var i8 int8 = -128
	var i16 int16 = -32768
	var i32 int32 = -2147483648
	var i64 int64 = -9223372036854775808
	var i int = -42

	fmt.Printf("   int8:  %d (size: 1 byte)\n", i8)
	fmt.Printf("   int16: %d (size: 2 bytes)\n", i16)
	fmt.Printf("   int32: %d (size: 4 bytes)\n", i32)
	fmt.Printf("   int64: %d (size: 8 bytes)\n", i64)
	fmt.Printf("   int:   %d (platform dependent)\n\n", i)

	// 2. UNSIGNED INTEGERS
	fmt.Println("2. Unsigned Integers:")
	var u8 uint8 = 255
	var u16 uint16 = 65535
	var u32 uint32 = 4294967295
	var u64 uint64 = 18446744073709551615
	var u uint = 42

	fmt.Printf("   uint8:  %d (size: 1 byte)\n", u8)
	fmt.Printf("   uint16: %d (size: 2 bytes)\n", u16)
	fmt.Printf("   uint32: %d (size: 4 bytes)\n", u32)
	fmt.Printf("   uint64: %d (size: 8 bytes)\n", u64)
	fmt.Printf("   uint:   %d (platform dependent)\n\n", u)

	// 3. FLOATING POINT
	fmt.Println("3. Floating Point:")
	var f32 float32 = 3.14159
	var f64 float64 = 2.718281828459045

	fmt.Printf("   float32: %.5f (size: 4 bytes)\n", f32)
	fmt.Printf("   float64: %.15f (size: 8 bytes)\n\n", f64)

	// 4. COMPLEX NUMBERS
	fmt.Println("4. Complex Numbers:")
	var c64 complex64 = 3 + 4i
	var c128 complex128 = 5 + 12i

	fmt.Printf("   complex64:  %v\n", c64)
	fmt.Printf("   complex128: %v\n\n", c128)

	// 5. BOOLEAN
	fmt.Println("5. Boolean:")
	var b1 bool = true
	var b2 bool = false

	fmt.Printf("   bool: %t or %t\n", b1, b2)
	fmt.Printf("   (stored as 1 or 0 internally)\n\n")

	// 6. STRING
	fmt.Println("6. String:")
	var s string = "Hello, 世界"

	fmt.Printf("   string: %q\n", s)
	fmt.Printf("   length: %d bytes\n", len(s))
	fmt.Printf("   (internally: sequence of byte values)\n\n")

	// 7. SPECIAL TYPES
	fmt.Println("7. Special Types:")
	var b byte = 65        // alias for uint8
	var r rune = 'A'       // alias for int32 (Unicode code point)

	fmt.Printf("   byte: %d (also displays as: %c)\n", b, b)
	fmt.Printf("   rune: %d (also displays as: %c)\n", r, r)
	fmt.Printf("   Both represent the same character 'A'!\n")
}



