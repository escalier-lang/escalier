# Escalier

Escalier is a programming language with a Go-based compiler. The compiler pipeline lives under [internal/](internal/) and the CLI entry points under [cmd/](cmd/).

## Repo layout

- [internal/lexer_util/](internal/lexer_util/), [internal/parser/](internal/parser/) — lexing and parsing
- [internal/ast/](internal/ast/) — AST definitions and visitor
- [internal/type_system/](internal/type_system/) — type representations and visitor
- [internal/checker/](internal/checker/) — type inference and checking
- [internal/codegen/](internal/codegen/), [internal/printer/](internal/printer/) — output
- [internal/resolver/](internal/resolver/), [internal/dep_graph/](internal/dep_graph/) — module/name resolution
- [internal/set/](internal/set/) — `Set` ADT (use this instead of `map[T]struct{}`)
- [cmd/escalier/](cmd/escalier/), [cmd/lsp-server/](cmd/lsp-server/) — CLI entry points

## Build & test

- Build: `make` (or `make escalier` / `make lsp-server`)
- Run all tests: `go test ./...`
- Run fixture tests only: `go test ./cmd/...`
- Update snapshots: `UPDATE_SNAPS=true go test ./...` (uses `github.com/gkampitakis/go-snaps`)
- Update fixtures: `UPDATE_FIXTURES=true go test ./cmd/...`
- After changing checker/codegen/printer output, re-run with both vars set so snapshots and fixtures stay in sync.
- Don't invoke the CLI directly with `go run ./cmd/escalier check <file>` to try out source. Add a fixture under `fixtures/<name>/lib/index.esc` (with a `package.json`) and run `go test ./cmd/...` — the fixture harness exercises the same pipeline and keeps the test reproducible.

## Code conventions

- When traversing tree-like structures, use the existing visitor for that tree — see [internal/ast/visitor.go](internal/ast/visitor.go) for AST and [internal/type_system/visitor.go](internal/type_system/visitor.go) for types. Don't hand-roll a new traversal.
- Use the `Set` ADT from [internal/set/](internal/set/) (`set.NewSet[T]()`, `set.FromSlice(...)`) instead of `map[T]struct{}` or `map[T]bool`.

## Writing tests

- Assert the full error message, not a substring.
- Write test inputs as Escalier source. Assert inferred types using Escalier type-annotation syntax in strings. See [internal/checker/tests/class_test.go](internal/checker/tests/class_test.go) for the canonical pattern.
- Prefer table-driven tests.

## Ephemeral scripts

When a one-off task needs more logic than a short shell pipeline (parsing JSON, multi-step text processing, scanning many files, ad-hoc analysis), write a Node script in `/tmp/<name>.mjs` and run it with `node`. Prefer this over inline bash with awk/sed/jq chains or a Python script — Node scripts are easier for the user to read and audit.

- Use ES modules (`.mjs`), top-level `await`, and the Node standard library (`node:fs`, `node:path`, `node:os`, etc.).
- Don't add npm dependencies for ephemeral work.
- Reach for bash only for genuinely shell-shaped tasks (one-line pipelines, file globs, process control).

## GitHub issues

- When creating issues with `gh`, do not escape strings or backticks in the Markdown body — `gh` passes the body through as-is, and escaping produces literal backslashes in the rendered issue.
