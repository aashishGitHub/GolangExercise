package main

import "fmt"

// Define custom types
type UserId int
type Speed float64


func main() {
	fmt.Println("=== Basic Type Conversions ===\n")

	// Creating custom type instances
	userId := UserId(5)
	speed := Speed(88.3)

	fmt.Printf("UserId: %v (type: %T)\n", userId, userId)
	fmt.Printf("Speed: %v (type: %T)\n\n", speed, speed)

	// Converting back to base types
	regularInt := int(userId)
	regularFloat := float64(speed)

	fmt.Printf("Converted to int: %d (type: %T)\n", regularInt, regularInt)
	fmt.Printf("Converted to float64: %.1f (type: %T)\n", regularFloat, regularFloat)
}



