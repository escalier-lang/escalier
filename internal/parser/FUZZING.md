# Fuzz Testing for Escalier Parser

This directory contains fuzz tests to ensure the Escalier parser handles arbitrary input without panicking or crashing.

## Overview

Fuzz testing is a software testing technique that provides invalid, unexpected, or random data as input to discover bugs, crashes, and security vulnerabilities. The parser fuzz tests verify that the parser gracefully handles all kinds of input, including malformed or invalid syntax.

## Available Fuzz Tests

### FuzzParseScript
Tests the `ParseScript()` function which parses complete Escalier scripts/programs.

**Usage:**
```bash
go test -fuzz=FuzzParseScript -fuzztime=30s ./internal/parser
```

### FuzzParseLibFiles
Tests the `ParseLibFiles()` function which parses library/module files.

**Usage:**
```bash
go test -fuzz=FuzzParseLibFiles -fuzztime=30s ./internal/parser
```

### FuzzParseTypeAnn
Tests the `ParseTypeAnn()` function which parses type annotations.

**Usage:**
```bash
go test -fuzz=FuzzParseTypeAnn -fuzztime=30s ./internal/parser
```

### FuzzParseCombination
Tests parsing with various combinations of valid and invalid syntax.

**Usage:**
```bash
go test -fuzz=FuzzParseCombination -fuzztime=30s ./internal/parser
```

## Running Fuzz Tests

### Quick Test (5 seconds each)
```bash
go test -fuzz=FuzzParseScript -fuzztime=5s ./internal/parser
go test -fuzz=FuzzParseLibFiles -fuzztime=5s ./internal/parser
go test -fuzz=FuzzParseTypeAnn -fuzztime=5s ./internal/parser
go test -fuzz=FuzzParseCombination -fuzztime=5s ./internal/parser
```

### Continuous Fuzzing (recommended for finding edge cases)
```bash
# Run for 5 minutes
go test -fuzz=FuzzParseScript -fuzztime=5m ./internal/parser
```

### Run All Fuzz Tests in Seed Mode
To verify all fuzz test seeds pass without actually fuzzing:
```bash
go test ./internal/parser
```

## What the Tests Verify

The fuzz tests ensure that:

1. **No Panics**: The parser never panics on any input, no matter how malformed
2. **Timeout Handling**: The parser respects context timeouts and doesn't run forever
3. **Graceful Error Handling**: Invalid input produces errors but doesn't crash
4. **Memory Safety**: No out-of-bounds access or memory leaks

## Test Coverage

The seed corpus includes:

- **Valid syntax**: Complete, valid Escalier programs
- **Edge cases**: Empty input, single tokens, incomplete statements
- **Invalid syntax**: Unclosed delimiters, missing tokens, malformed expressions
- **Complex structures**: Deeply nested expressions, long input strings
- **Special characters**: Unicode, null bytes, escape sequences
- **All language features**: Functions, classes, enums, interfaces, pattern matching, etc.

## Fixing Issues Found by Fuzzing

When a fuzz test finds a crash or panic:

1. The failing input is saved to `testdata/fuzz/<TestName>/<hash>`
2. Re-run the specific test case:
   ```bash
   go test -run=FuzzParseScript/<hash>
   ```
3. Fix the parser to handle the case gracefully (return error instead of panic)
4. Verify the fix:
   ```bash
   go test -run=FuzzParseScript/<hash>
   go test -fuzz=FuzzParseScript -fuzztime=30s ./internal/parser
   ```

## Parser Changes for Fuzz Testing

The following improvements were made to support fuzz testing:

### Added Context Timeout Checks
Context timeout checks were added to prevent infinite loops in:
- `parser.go`: `decls()` function
- `stmt.go`: `stmts()` function  
- `combinators.go`: `parseDelimSeq()` function

These checks ensure the parser respects the context timeout and returns gracefully when time runs out.

### Removed Panics
Replaced `panic()` calls with graceful error handling:
- `type_ann.go`: `typeAnn()` function - now returns `nil` on error
- `type_ann.go`: `objTypeAnnElem()` function - reports error instead of panicking

The parser now consistently returns `nil` and reports errors instead of panicking on invalid input.

## Best Practices

1. **Run fuzz tests regularly** during development to catch regressions
2. **Add new seeds** when implementing new language features
3. **Investigate all crashes** - they indicate bugs in error handling
4. **Keep timeouts reasonable** (50ms) to prevent resource exhaustion
5. **Review generated corpus** in `testdata/fuzz/` to understand what patterns trigger issues

## Continuous Integration

Fuzz tests run in seed mode (no actual fuzzing) as part of the regular test suite:
```bash
go test ./internal/parser
```

For dedicated fuzzing, run the tests with `-fuzz` flag for extended periods to discover edge cases.

## References

- [Go Fuzzing Documentation](https://go.dev/doc/fuzz/)
- [Tutorial: Getting started with fuzzing](https://go.dev/doc/tutorial/fuzz)
