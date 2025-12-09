package main

import "fmt"

// Define custom types for temperature
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

// String methods for nice printing
func (c Celsius) String() string {
	return fmt.Sprintf("%.1f°C", float64(c))
}

func (f Fahrenheit) String() string {
	return fmt.Sprintf("%.1f°F", float64(f))
}

func main() {
	fmt.Println("=== Custom Types with Methods ===\n")

	var boiling Celsius = 100
	fmt.Printf("Water boils at %s\n", boiling)
	fmt.Printf("Which is %s\n\n", boiling.ToFahrenheit())

	var freezing Fahrenheit = 32
	fmt.Printf("Water freezes at %s\n", freezing)
	fmt.Printf("Which is %s\n\n", freezing.ToCelsius())

	// Room temperature example
	var room Celsius = 22
	fmt.Printf("Room temperature: %s = %s\n", room, room.ToFahrenheit())
}

