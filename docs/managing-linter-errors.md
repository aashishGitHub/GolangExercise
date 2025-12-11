# Managing Linter Errors in Learning Code

This document explains how to manage linter errors in example and learning code.

## 🎯 Solutions Implemented

### 1. `.cursorignore` File

**Location**: Root directory

This file tells Cursor IDE to ignore linting for specific files/directories:

```
examples/*.go
go-workspace/*.go
```

**Effect**: Reduces linting warnings in example files

### 2. `.golangci.yml` File

**Location**: Root directory

Configures the Go linter to be more lenient with learning code:

```yaml
issues:
  exclude-rules:
    - path: examples/
      linters:
        - errcheck
        - gosec
        - gocritic
```

**Effect**: Disables strict linting rules for `examples/` and `go-workspace/` directories

### 3. `.editorconfig` File

**Location**: `examples/` directory

Provides editor-specific settings for example files.

## 🛠️ Additional Options

### Option 1: Add Comment to Disable Linting Per File

Add this comment at the top of any file:

```go
//nolint:all
package main

// Your code here
```

Or disable specific linters:

```go
//nolint:errcheck,unused
package main

// Your code here
```

### Option 2: Disable Linting for Specific Lines

```go
package main

func main() {
    _ = someFunction() //nolint:errcheck
    unusedVar := 42    //nolint:unused
}
```

### Option 3: VS Code/Cursor Settings

Add to your workspace settings (`.vscode/settings.json`):

```json
{
  "go.lintOnSave": "off",
  "go.lintTool": "golangci-lint",
  "go.lintFlags": [
    "--fast"
  ],
  "files.exclude": {
    "**/.git": true,
    "**/examples": false
  },
  "go.formatTool": "goimports"
}
```

### Option 4: Separate Go Modules

Keep examples as a separate module with its own `go.mod`:

```bash
cd examples
go mod init examples
```

This isolates linting for the examples folder.

## 📋 Comparison of Methods

| Method | Scope | Ease | Recommended For |
|--------|-------|------|-----------------|
| `.cursorignore` | IDE-wide | ✅ Easy | Quick solution |
| `.golangci.yml` | Project-wide | ⚠️ Medium | Best for teams |
| `//nolint` comments | Per-file/line | ✅ Easy | Specific cases |
| Workspace settings | IDE-wide | ⚠️ Medium | Personal preference |
| Separate module | Directory | ⚠️ Medium | Clean separation |

## ✅ Recommended Approach

For learning/example code, use a combination:

1. **`.cursorignore`** - Quick IDE-level ignore
2. **`.golangci.yml`** - Project-level configuration
3. **`//nolint:all`** comment - For specific files with intentional "bad" examples

## 🎓 For Learning vs Production Code

### Learning Code (`examples/`, `go-workspace/`)
- ✅ Less strict linting
- ✅ Allow unused variables
- ✅ Allow unchecked errors (for demonstration)
- ✅ Focus on concepts, not best practices

### Production Code (future `src/`, `pkg/` directories)
- ❌ Strict linting
- ❌ No unused code
- ❌ All errors must be handled
- ❌ Follow best practices

## 🔧 Testing Your Setup

After applying these configurations, restart Cursor IDE:

1. Close Cursor
2. Reopen the workspace
3. Open any file in `examples/`
4. Check if linting warnings are reduced

## 🚀 Quick Commands

### Disable linting temporarily
```bash
# Run without linting
go run -exec echo examples/01-basic-conversions.go

# Or just run directly
go run examples/01-basic-conversions.go
```

### Check if golangci-lint is installed
```bash
golangci-lint --version
```

### Run linter manually (when needed)
```bash
# Lint specific file
golangci-lint run examples/01-basic-conversions.go

# Lint entire project
golangci-lint run
```

## 💡 Pro Tips

1. **Keep learning code separate** from production code
2. **Use comments** to explain why code might not follow best practices
3. **Gradually enable linters** as you learn more
4. **Review warnings** occasionally to learn what not to do

## 📚 Example File Header

For learning files, consider adding a header:

```go
// Example: Basic Type Conversions
// Purpose: Learning and demonstration
// Note: This code prioritizes clarity over production best practices
//nolint:all
package main

import "fmt"

func main() {
    // Example code here
}
```

## 🔗 Related Resources

- [golangci-lint documentation](https://golangci-lint.run/)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [Effective Go](https://go.dev/doc/effective_go)

---

**Remember**: Linter errors are learning opportunities! Review them occasionally to understand Go best practices. 🎯

