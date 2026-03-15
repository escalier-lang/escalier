# Parser Error Recovery Strategy

This document describes the error recovery strategy used by the Escalier parser.
The goal is to produce a usable AST even when source code contains syntax errors,
enabling downstream tools (type checker, LSP completion) to work with partial
results.

## Core Principles

1. **Always produce an AST node.** Parsing functions should return partial AST
   nodes with placeholders rather than returning nil when encountering errors.
2. **Report errors via the parser's error list.** The `errors` slice on the
   `Parser` struct is the authoritative record of all syntax errors. AST error
   nodes (`ErrorExpr`, `ErrorStmt`) carry only a `Span` — no message or skipped
   token storage.
3. **Recover at the narrowest scope possible.** Expression-level recovery is
   preferred over statement-level recovery, which is preferred over abandoning
   an entire declaration.

## Recovery Levels

### Expression-Level Recovery

**Entry point:** `expr()` in `expr.go`

The `expr()` function wraps `exprWithoutErrorCheck()` and guarantees a non-nil
return. If `exprWithoutErrorCheck()` returns nil (no valid expression found),
`expr()` reports an error and returns an `ErrorExpr` node.

This means any caller of `expr()` always receives a valid AST node, preventing
cascading nil-check failures up the call chain.

Specific recovery points:

- **Missing binary RHS** (`a + `): After consuming an operator, if `primaryExpr()`
  returns nil, an `ErrorExpr` is substituted for the right operand. The
  `BinaryExpr` is still constructed with the valid LHS.
- **Incomplete call arguments** (`foo(a,`): The `parseDelimSeq` combinator calls
  `expr()` for each argument, which returns `ErrorExpr` on failure. Missing close
  parens are detected and reported after the argument list.
- **Incomplete index expressions** (`arr[`): `expr()` returns `ErrorExpr` for the
  missing index. Missing `]` is detected and reported.
- **Trailing dot** (`obj.`): A `MemberExpr` is produced with an empty-string
  identifier as the property name. This is critical for LSP completion — the
  object's type is still inferred so completions can be offered.
- **Stack invariant guard**: The Pratt parser's stack invariant check in
  `exprWithoutErrorCheck()` returns an `ErrorExpr` instead of panicking, ensuring
  the parser never crashes during recovery.

### Statement-Level Recovery

**Entry point:** `stmts()` in `stmt.go`

When `stmt()` returns nil, the `stmts()` loop wraps the failed region in an
`ErrorStmt` node. Two sub-cases:

- **Tokens were consumed** (e.g. `primaryExpr()` consumed unknown tokens
  internally): An `ErrorStmt` is created covering the consumed span.
- **No tokens were consumed** (e.g. stray `)` or `]`): The
  `skipToNextStatement()` method advances past tokens until reaching a statement
  boundary, then an `ErrorStmt` is created.

Statement boundaries recognized by `skipToNextStatement()`:
- EOF or the block's stop token (e.g. `}`)
- Newline (next token on a different line)
- Statement-initiating keywords: `val`, `var`, `fn`, `type`, `interface`,
  `enum`, `class`, `return`, `throw`, `for`, `if`, `import`, `export`,
  `declare`, `async`

The `isStatementInitiator()` helper exposes the same keyword set for use by
declaration-level recovery (e.g. deciding whether to attempt expression
parsing after a missing `=`).

### Declaration-Level Recovery

**Entry points:** `varDecl()`, `fnDecl()`, `typeDecl()`, `interfaceDecl()`,
`enumDecl()`, `classDecl()` in `decl.go`

All declaration parsers produce partial AST nodes with placeholders instead of
returning nil:

- **Missing identifier**: An empty-string `Ident` is used (e.g.
  `ast.NewIdentifier("")`). This matches the pattern already used by `fnDecl`.
- **Missing `=` sign** (`val x 5`): If the next token is not a statement
  initiator and is on the same line, the parser attempts to parse an expression
  as the initializer (recovering the user's intent). If the next token is a
  statement initiator or on a new line (`val x\nval y = 10`), an `ErrorExpr`
  is used as the initializer to avoid consuming the next statement.
- **Missing return type** (`fn foo() ->`): A `FuncDecl` is produced without a
  return type annotation.
- **Missing body `{`**: Declarations are produced with nil/empty bodies
  (`ClassDecl`, `EnumDecl`, `InterfaceDecl`).
- **Missing extends type** (`class Foo extends {}`): If `{` follows `extends`
  directly, the parser skips the `typeAnn()` call to avoid consuming the class
  body as an object type. The `{` is preserved for class body parsing.
- **Invalid modifiers** (`async val`): The error is reported but parsing of
  the declaration continues.

## Error Node Types

| Node        | Level       | Fields     | Downstream Handling                    |
|-------------|-------------|------------|----------------------------------------|
| `ErrorExpr` | Expression  | `span`     | Inferred as `ErrorType` (no cascading) |
| `ErrorStmt` | Statement   | `span`     | Checker: no-op. Codegen: skip.         |

## Type Checker Integration

`ErrorExpr` nodes are inferred as `ErrorType`. The unifier treats `ErrorType` as
compatible with any type (returns nil error), which suppresses cascading type
errors from syntax-error regions. Direct operations on `ErrorType` values
(member access, indexing, calls) also return `ErrorType` silently.

Genuine type errors in well-formed code are still reported, even when the same
file contains syntax errors elsewhere.
