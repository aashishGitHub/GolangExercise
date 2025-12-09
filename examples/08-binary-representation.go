package main

import "fmt"

func main() {
	fmt.Println("=== Binary Representation: Everything is Numeric ===\n")

	// 1. INTEGERS - Obviously numeric
	fmt.Println("1. Integers (clearly numeric):")
	var num int = 42
	fmt.Printf("   Decimal: %d\n", num)
	fmt.Printf("   Binary:  %08b\n", num)
	fmt.Printf("   Hex:     0x%X\n\n", num)

	// 2. BOOLEANS - Numeric underneath!
	fmt.Println("2. Booleans (numeric underneath):")
	var t bool = true
	var f bool = false
	fmt.Printf("   true:  %t (stored as 1)\n", t)
	fmt.Printf("   false: %t (stored as 0)\n\n", f)

	// 3. CHARACTERS - Just numbers!
	fmt.Println("3. Characters (Unicode code points = numbers):")
	var char rune = 'A'
	fmt.Printf("   Character: %c\n", char)
	fmt.Printf("   Code point (decimal): %d\n", char)
	fmt.Printf("   Code point (binary):  %08b\n", char)
	fmt.Printf("   Code point (hex):     0x%X\n\n", char)

	// 4. STRINGS - Sequence of numeric bytes!
	fmt.Println("4. Strings (sequences of byte values):")
	var text string = "Hi"
	bytes := []byte(text)
	
	fmt.Printf("   String: %q\n", text)
	fmt.Printf("   Bytes (decimal): %v\n", bytes)
	
	fmt.Println("   Byte-by-byte breakdown:")
	for i, b := range bytes {
		fmt.Printf("      [%d] = %d (binary: %08b, char: %c)\n", i, b, b, b)
	}
	fmt.Println()

	// 5. SAME BITS, DIFFERENT INTERPRETATIONS
	fmt.Println("5. Same Binary Value, Different Interpretations:")
	var value uint8 = 65
	
	fmt.Printf("   Binary: %08b\n", value)
	fmt.Printf("   As uint8: %d\n", value)
	fmt.Printf("   As byte: %d\n", byte(value))
	fmt.Printf("   As rune/char: %c\n", rune(value))
	fmt.Printf("   As ASCII: '%s'\n\n", string(value))

	// 6. EMOJI - Still just numbers!
	fmt.Println("6. Even Emoji are Numbers:")
	var emoji rune = '😀'
	fmt.Printf("   Emoji: %c\n", emoji)
	fmt.Printf("   Unicode code point (decimal): %d\n", emoji)
	fmt.Printf("   Unicode code point (hex): U+%X\n", emoji)
	fmt.Printf("   Binary: %032b\n\n", emoji)

	// 7. MULTIPLE BYTES FOR ONE CHARACTER
	fmt.Println("7. Multi-byte Characters:")
	var chinese string = "中"
	fmt.Printf("   Character: %s\n", chinese)
	fmt.Printf("   UTF-8 bytes: %v\n", []byte(chinese))
	fmt.Printf("   Number of bytes: %d\n", len(chinese))
	
	// Get the rune (code point)
	for _, r := range chinese {
		fmt.Printf("   Unicode code point: %d (U+%X)\n", r, r)
	}
}

