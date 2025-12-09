package main

import "fmt"

// Define custom types for different currencies
type USD float64
type EUR float64
type GBP float64

// Exchange rates (example values)
const (
	USD_TO_EUR = 0.85
	USD_TO_GBP = 0.73
	EUR_TO_USD = 1.18
	EUR_TO_GBP = 0.86
	GBP_TO_USD = 1.37
	GBP_TO_EUR = 1.16
)

// USD conversion methods
func (u USD) ToEUR() EUR {
	return EUR(float64(u) * USD_TO_EUR)
}

func (u USD) ToGBP() GBP {
	return GBP(float64(u) * USD_TO_GBP)
}

// EUR conversion methods
func (e EUR) ToUSD() USD {
	return USD(float64(e) * EUR_TO_USD)
}

func (e EUR) ToGBP() GBP {
	return GBP(float64(e) * EUR_TO_GBP)
}

// GBP conversion methods
func (g GBP) ToUSD() USD {
	return USD(float64(g) * GBP_TO_USD)
}

func (g GBP) ToEUR() EUR {
	return EUR(float64(g) * GBP_TO_EUR)
}

// String methods for pretty printing
func (u USD) String() string {
	return fmt.Sprintf("$%.2f", float64(u))
}

func (e EUR) String() string {
	return fmt.Sprintf("€%.2f", float64(e))
}

func (g GBP) String() string {
	return fmt.Sprintf("£%.2f", float64(g))
}

func main() {
	fmt.Println("=== Currency Conversion Example ===\n")

	var price USD = 100.00
	fmt.Printf("Product Price: %s\n", price)
	fmt.Printf("In EUR: %s\n", price.ToEUR())
	fmt.Printf("In GBP: %s\n\n", price.ToGBP())

	var euroPrice EUR = 85.00
	fmt.Printf("European Price: %s\n", euroPrice)
	fmt.Printf("In USD: %s\n", euroPrice.ToUSD())
	fmt.Printf("In GBP: %s\n\n", euroPrice.ToGBP())

	var britishPrice GBP = 73.00
	fmt.Printf("British Price: %s\n", britishPrice)
	fmt.Printf("In USD: %s\n", britishPrice.ToUSD())
	fmt.Printf("In EUR: %s\n", britishPrice.ToEUR())
}

