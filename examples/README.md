# Type Conversion Examples

This folder contains practical examples demonstrating type conversions in Go.

## Running the Examples

Navigate to this folder and run any example:

```bash
cd examples
go run 01-basic-conversions.go
```

Or run directly from the root:

```bash
go run examples/01-basic-conversions.go
```

## Examples Overview

| File | Description |
|------|-------------|
| `01-basic-conversions.go` | Introduction to custom types and basic conversions |
| `02-numeric-conversions.go` | Converting between different numeric types (int, float, etc.) |
| `03-custom-types-methods.go` | Custom types with methods (Temperature conversion) |
| `04-type-safety.go` | Demonstrates how custom types prevent bugs |
| `05-string-conversions.go` | String ↔ bytes, numbers ↔ strings |
| `06-practical-money.go` | Real-world example: Currency conversion |

## Quick Start

Try running all examples in sequence:

```bash
for file in *.go; do
    echo "Running $file..."
    go run "$file"
    echo ""
done
```

## Learning Path

1. Start with `01-basic-conversions.go` to understand the fundamentals
2. Move to `02-numeric-conversions.go` for numeric type handling
3. Study `03-custom-types-methods.go` to learn about methods
4. Review `04-type-safety.go` to see the benefits
5. Practice with `05-string-conversions.go` for string handling
6. Apply knowledge with `06-practical-money.go` real-world scenario

## Notes

- All examples are standalone and can be run independently
- Each example includes comments explaining the concepts
- Examples demonstrate both correct and incorrect approaches
- See the main documentation in `/docs/type-conversions.md` for detailed explanations

