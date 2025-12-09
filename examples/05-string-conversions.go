package main

import (
	"fmt"
	"strconv"
)

func main() {
	fmt.Println("=== String Conversions ===\n")

	// ASCII/Rune to string
	fmt.Println("1. Rune/ASCII to String:")
	var code int = 65
	var char string = string(code)
	fmt.Printf("   ASCII %d → string '%s'\n\n", code, char)

	// Multiple runes
	runes := []rune{72, 101, 108, 108, 111}
	text := string(runes)
	fmt.Printf("   Runes %v → '%s'\n\n", runes, text)

	// Number to string - THE RIGHT WAY
	fmt.Println("2. Number to String (using strconv):")
	var num int = 65
	var str string = strconv.Itoa(num)
	fmt.Printf("   int %d → string '%s'\n\n", num, str)

	// String to bytes
	fmt.Println("3. String to Bytes:")
	var message string = "Hello"
	var bytes []byte = []byte(message)
	fmt.Printf("   string '%s' → bytes %v\n\n", message, bytes)

	// Bytes to string
	fmt.Println("4. Bytes to String:")
	var data []byte = []byte{72, 101, 108, 108, 111}
	var word string = string(data)
	fmt.Printf("   bytes %v → string '%s'\n\n", data, word)

	// Common mistake demonstration
	fmt.Println("5. Common Mistake:")
	var number int = 123
	wrongWay := string(number)     // Interprets as Unicode codepoint
	rightWay := strconv.Itoa(number) // Converts to decimal string
	fmt.Printf("   string(%d) = '%s' (Unicode char) ❌ WRONG\n", number, wrongWay)
	fmt.Printf("   strconv.Itoa(%d) = '%s' ✅ CORRECT\n", number, rightWay)
}

