package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

func main() {
	fmt.Println("=== User Input Validation Examples ===\n")

	// Simulate various user inputs
	validateAge("25")
	validateAge("abc")
	validateAge("999")
	validateAge("-5")

	fmt.Println()

	validatePrice("19.99")
	validatePrice("not a number")
	validatePrice("-10.50")

	fmt.Println()

	validateEmail("user@example.com")
	validateEmail("invalid-email")
	validateEmail("")

	fmt.Println()

	validateUsername("john_doe")
	validateUsername("ab") // too short
	validateUsername("this_is_way_too_long_for_a_username")

	fmt.Println()

	validateUTF8("Hello World")
	validateUTF8("Hello 世界")
	invalidUTF8 := string([]byte{0xFF, 0xFE, 0xFD})
	validateUTF8(invalidUTF8)
}

// Validate age (must be positive integer between 0-150)
func validateAge(input string) {
	fmt.Printf("Validating age: %q\n", input)

	// Convert string to integer
	age, err := strconv.Atoi(input)

	if err != nil {
		fmt.Printf("   ❌ Invalid: not a valid number\n")
		return
	}

	if age < 0 {
		fmt.Printf("   ❌ Invalid: age cannot be negative\n")
		return
	}

	if age > 150 {
		fmt.Printf("   ❌ Invalid: age %d is unrealistic\n", age)
		return
	}

	fmt.Printf("   ✅ Valid age: %d\n", age)
}

// Validate price (must be positive float)
func validatePrice(input string) {
	fmt.Printf("Validating price: %q\n", input)

	// Convert string to float
	price, err := strconv.ParseFloat(input, 64)

	if err != nil {
		fmt.Printf("   ❌ Invalid: not a valid number\n")
		return
	}

	if price < 0 {
		fmt.Printf("   ❌ Invalid: price cannot be negative\n")
		return
	}

	if price == 0 {
		fmt.Printf("   ⚠️  Warning: price is zero\n")
		return
	}

	fmt.Printf("   ✅ Valid price: $%.2f\n", price)
}

// Validate email (simple check)
func validateEmail(input string) {
	fmt.Printf("Validating email: %q\n", input)

	if input == "" {
		fmt.Printf("   ❌ Invalid: email cannot be empty\n")
		return
	}

	if !strings.Contains(input, "@") {
		fmt.Printf("   ❌ Invalid: email must contain @\n")
		return
	}

	parts := strings.Split(input, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		fmt.Printf("   ❌ Invalid: email format is incorrect\n")
		return
	}

	if !strings.Contains(parts[1], ".") {
		fmt.Printf("   ❌ Invalid: domain must contain a dot\n")
		return
	}

	fmt.Printf("   ✅ Valid email: %s\n", input)
}

// Validate username (must be 3-20 characters, alphanumeric + underscore)
func validateUsername(input string) {
	fmt.Printf("Validating username: %q\n", input)

	if len(input) < 3 {
		fmt.Printf("   ❌ Invalid: username must be at least 3 characters\n")
		return
	}

	if len(input) > 20 {
		fmt.Printf("   ❌ Invalid: username must be at most 20 characters\n")
		return
	}

	// Check for valid characters
	for _, char := range input {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_') {
			fmt.Printf("   ❌ Invalid: username contains invalid character '%c'\n", char)
			return
		}
	}

	fmt.Printf("   ✅ Valid username: %s\n", input)
}

// Validate UTF-8 encoding
func validateUTF8(input string) {
	fmt.Printf("Validating UTF-8: %q\n", input)

	if !utf8.ValidString(input) {
		fmt.Printf("   ❌ Invalid: contains invalid UTF-8 sequences\n")
		fmt.Printf("   Bytes: %v\n", []byte(input))
		return
	}

	fmt.Printf("   ✅ Valid UTF-8 string\n")
	fmt.Printf("   Length in bytes: %d\n", len(input))
	fmt.Printf("   Length in runes: %d\n", utf8.RuneCountInString(input))
}



