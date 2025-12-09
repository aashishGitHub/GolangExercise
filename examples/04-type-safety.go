package main

import "fmt"

// Define different ID types for type safety
type UserId int
type ProductId int
type OrderId int

// Functions that only accept specific types
func getUserName(id UserId) string {
	return fmt.Sprintf("User_%d", id)
}

func getProductName(id ProductId) string {
	return fmt.Sprintf("Product_%d", id)
}

func getOrderNumber(id OrderId) string {
	return fmt.Sprintf("Order_%d", id)
}

func main() {
	fmt.Println("=== Type Safety Example ===\n")

	var user UserId = 123
	var product ProductId = 456
	var order OrderId = 789

	// Correct usage - types match
	fmt.Println("✅", getUserName(user))
	fmt.Println("✅", getProductName(product))
	fmt.Println("✅", getOrderNumber(order))

	// The following would cause compile errors (uncomment to see):
	// fmt.Println(getUserName(product))  // ❌ Error: cannot use product (type ProductId) as type UserId
	// fmt.Println(getProductName(user))  // ❌ Error: cannot use user (type UserId) as type ProductId

	// Must explicitly convert if you really want to:
	fmt.Println("\nExplicit conversion (not recommended but possible):")
	fmt.Println("⚠️ ", getUserName(UserId(product)))

	fmt.Println("\nType safety prevents bugs by catching mismatched types at compile time!")
}

