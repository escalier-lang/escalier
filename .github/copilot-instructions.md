# Escalier Programming Language - Development Instructions

## Project Overview

Escalier is a programming language compiler with strong TypeScript interoperability. The project consists of:
- **Go backend**: Core compiler, type checker, parser, and LSP server (`internal/`, `cmd/`)
- **TypeScript/JavaScript packages**: CLI wrapper, runtime, and VS Code extension (`packages/`)
- **Playground**: Web-based IDE for experimenting with Escalier (`playground/`)

## Repository Structure

### Core Go Components
- `cmd/escalier/` - Main compiler binary
- `cmd/lsp-server/` - Language Server Protocol implementation
- `internal/ast/` - Abstract Syntax Tree definitions
- `internal/checker/` - Type checking and validation
- `internal/parser/` - Source code parsing
- `internal/codegen/` - Code generation to JavaScript
- `internal/compiler/` - Main compilation orchestration
- `internal/type_system/` - Type system implementation
- `internal/graphql/` - GraphQL-specific functionality

### TypeScript/JavaScript Packages
- `packages/cli/` - Command-line interface wrapper
- `packages/runtime/` - Runtime support library
- `packages/vscode-escalier/` - VS Code extension

### Documentation & Testing
- `docs/` - Language feature documentation
- `fixtures/` - Test fixtures for various language features
- `planning/` - Design documents for planned features

## Development Setup

### Prerequisites
- **Go 1.24.0** or later
- **Node.js 18.0.0** or later
- **pnpm 10.22.0** (managed via packageManager field)

### Initial Setup
```bash
# Install JavaScript dependencies
pnpm install

# Build Go binaries
make all
# This creates:
# - bin/escalier (compiler)
# - bin/lsp-server (language server)
```

## Common Development Tasks

### Building

**Go binaries:**
```bash
make escalier          # Build compiler
make lsp-server        # Build LSP server
make all              # Build both
make escalier-wasm    # Build WASM version of compiler
make lsp-server-wasm  # Build WASM version of LSP server
```

**TypeScript/JavaScript:**
```bash
pnpm build      # Build all TS packages
pnpm compile    # Build LSP server + TS packages
pnpm watch      # Watch mode for TypeScript
```

### Testing

**Go tests:**
```bash
go test ./...                             # Run all Go tests
UPDATE_SNAPS=true go test ./...           # Update test snapshots
go test ./cmd/...                         # Run fixture tests
UPDATE_FIXTURES=true go test ./cmd/...    # Update fixture tests
```

**JavaScript tests:**
```bash
pnpm test           # Run tests
pnpm test:ui        # Run with UI
pnpm coverage       # Run with coverage report
```

### Code Quality

**Linting and Formatting:**
```bash
pnpm lint       # Lint code
pnpm format     # Format code
pnpm check      # Check formatting + linting
pnpm typecheck  # Type check TypeScript
```

The project uses **Biome** for JavaScript/TypeScript linting and formatting.

### Development Workflow

1. **Making changes to Go code:**
   - Edit files in `internal/`, `cmd/`
   - Run `go test ./...` to ensure tests pass
   - Update snapshots if needed: `UPDATE_SNAPS=true go test ./...`
   - Rebuild binaries: `make all`

2. **Making changes to TypeScript code:**
   - Edit files in `packages/`, `playground/`
   - Run `pnpm typecheck` to check types
   - Run `pnpm test` to run tests
   - Run `pnpm check` to verify formatting/linting

3. **Adding new language features:**
   - Add test fixtures in `fixtures/[feature-name]/`
   - Update parser in `internal/parser/`
   - Update type checker in `internal/checker/`
   - Update code generator in `internal/codegen/`
   - Add documentation in `docs/`

4. **Working with the LSP:**
   - Make changes in `cmd/lsp-server/`
   - Rebuild: `make lsp-server`
   - Test in VS Code extension or playground

## Testing Strategy

### Go Tests
- Unit tests live alongside source files (`*_test.go`)
- Integration tests use fixtures in `fixtures/` directory
- Snapshot testing with `github.com/gkampitakis/go-snaps`
- Update snapshots when output intentionally changes
- Fixture tests run via `go test ./cmd/...` and validate compiler output
- Update fixture tests with `UPDATE_FIXTURES=true go test ./cmd/...` when compiler output changes

### JavaScript Tests
- Vitest for unit testing
- Coverage reports generated in `coverage/` directory
- Focus on LSP client and package functionality

## Key Language Features

Escalier extends TypeScript with:
- **Pattern matching** (see `docs/04_pattern_matching.md`)
- **Enums** (see `docs/05_enums.md`)
- **Error handling** (see `docs/06_error_handling.md`)
- **Exact types** (see `docs/07_exact_types.md`)
- **Mutability controls** (see `docs/08_mutability.md`)
- **Enhanced destructuring** (see `docs/01_destructuring.md`)

## Architecture Notes

### Compilation Pipeline
1. **Parse** - Source → AST (`internal/parser/`)
2. **Type Check** - AST → Typed AST with diagnostics (`internal/checker/`)
3. **Code Generation** - Typed AST → JavaScript + .d.ts files (`internal/codegen/`)

### Type System
- Implements TypeScript-compatible type system
- Additional types converted to TS equivalents in .d.ts output
- Uses TSDoc comments to preserve Escalier-specific type information

### LSP Integration
- Provides IDE features via Language Server Protocol
- WebAssembly build available for browser environments
- Powers VS Code extension and web playground

## Pull Request Guidelines

1. Ensure all tests pass (`go test ./...` and `pnpm test`)
2. Update snapshots if output changes intentionally
3. Add test fixtures for new language features
4. Update documentation in `docs/` for user-facing changes
5. Run `pnpm check` to ensure code style compliance
6. Keep Go code formatted with `go fmt`

## Debugging Tips

### Go Debugging
- Use `go test -v` for verbose test output
- Use `go test -run TestName` to run specific tests
- Add print statements or use delve debugger

### TypeScript Debugging
- Use `pnpm test:ui` for interactive test debugging
- Check `coverage/` for coverage reports
- Use VS Code's built-in debugger for LSP/extension work

## Useful Commands Reference

```bash
# Clean build artifacts
make clean

# Build everything
make all && pnpm build

# Run all tests
go test ./... && pnpm test

# Run fixture tests
go test ./cmd/...

# Full quality check
go test ./... && pnpm check && pnpm typecheck

# Update Go snapshots and fixture tests
UPDATE_SNAPS=true go test ./...
UPDATE_FIXTURES=true go test ./cmd/...
```

## Getting Help

- Check documentation in `docs/` directory
- Review test fixtures in `fixtures/` for examples
- Examine `planning/` for design discussions
- See README.md for basic project information

## CI/CD

The project uses:
- **Codecov** for coverage tracking (config in `codecov.yaml`)
- GitHub Actions (configuration in `.github/workflows/` if present)

Coverage badge: [![codecov](https://codecov.io/github/escalier-lang/escalier/graph/badge.svg?token=AMM20YA614)](https://codecov.io/github/escalier-lang/escalier)
