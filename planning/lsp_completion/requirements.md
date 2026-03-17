# textDocument/completion - Requirements

## Overview

Add support for `textDocument/completion` in the Escalier LSP server. This
involves three areas of work: parser recovery, error-tolerant type inference,
and the completion handler itself.

---

## 1. Parser Recovery

### 1.1 Consistent Recovery Strategy

**Problem:** Recovery behavior varies across the parser. Function declarations
(`decl.go`) have bespoke recovery logic (e.g. creating empty identifiers for
missing names, skipping missing parens). Other constructs use different
strategies (backtracking via `saveState`/`restoreState`, returning `nil`,
consuming unexpected tokens). This inconsistency makes it hard to reason about
what the AST looks like after a parse error.

**Requirements:**

- R1.1.1: Define a single, documented recovery strategy that all production
  rules follow when they encounter an unexpected token.
- R1.1.2: The recovery strategy must produce a well-formed AST even when the
  source contains syntax errors. Every node in the error-recovery AST must have
  a valid `Span`.
- R1.1.3: Recovery must not silently drop tokens. Skipped tokens should be
  recorded so that diagnostics can report them.

### 1.2 Trailing Dot Recovery

**Problem:** When a user types `foo.` (with no property name yet), the parser
needs to produce an AST that the type checker and completion handler can work
with.

**Requirements:**

- R1.2.1: When the parser encounters a `.` or `?.` followed by a token that
  cannot be an identifier (including EOF and newline), it must produce a
  `MemberExpr` with an empty-string `Ident` as the property. (This is the
  current behavior and should be preserved.)
- R1.2.2: The `MemberExpr` produced by R1.2.1 must set its `Span` to cover the
  object expression through the `.` token, so that the completion handler can
  locate it.
- R1.2.3: The parser must report a diagnostic for the missing identifier but
  must not stop parsing the rest of the file.

### 1.3 General Expression Recovery

**Problem:** Beyond the trailing-dot case, there are other situations where an
expression is incomplete or malformed (e.g. a missing operand in `a + `, an
unclosed parenthesized expression `(a + b`, a missing argument in a call
`foo(a, )`, or an incomplete conditional `if x then`). Currently these cases are
handled inconsistently — some produce `EmptyExpr`, some cause the parser to
bail out of the entire statement.

**Requirements:**

- R1.3.1: When parsing an expression fails partway through, the parser must
  produce an `ErrorExpr` node for the invalid portion and continue parsing the
  rest of the expression or statement. The successfully parsed parts of the
  expression must be preserved in the AST.
- R1.3.2: Missing right-hand operands in binary expressions (e.g. `a + `,
  `x && `) must produce a binary expression with an `ErrorExpr` on the right
  side, rather than discarding the entire expression.
- R1.3.3: Incomplete call expressions (e.g. `foo(a, )` or `foo(a, b`) must
  produce a `CallExpr` with the successfully parsed arguments preserved. Missing
  or malformed arguments should be represented as `ErrorExpr`.
- R1.3.4: Incomplete index expressions (e.g. `arr[`) must produce an
  `IndexExpr` with an `ErrorExpr` for the missing index.
- R1.3.5: The parser must not abandon an entire statement or declaration because
  of a single malformed subexpression. Recovery should happen at the expression
  level whenever possible, falling back to statement-level recovery only when
  expression-level recovery is not feasible.

### 1.4 Rename EmptyExpr to ErrorExpr

**Problem:** `EmptyExpr` is used when the parser cannot produce a valid
expression, but the name suggests "absence" rather than "error." Renaming it to
`ErrorExpr` makes its purpose clearer and mirrors the proposed `ErrorStmt` node.

**Requirements:**

- R1.4.1: Rename `EmptyExpr` to `ErrorExpr` throughout the codebase (AST
  definition, parser, type checker, code generator, tests).
- R1.4.2: `ErrorExpr` should be usable for any expression-level syntax error,
  not only missing operands.
- R1.4.3: `ErrorExpr` must contain a `Span` covering the error site. It does
  not need to store skipped tokens or a message — the parser's error list
  (populated via `reportError`) is the authoritative record of what went wrong.
  Similarly, `ErrorStmt` must contain a `Span` covering the skipped region.

### 1.5 Statement-Level Recovery

**Requirements:**

- R1.5.1: When a statement cannot be parsed, the parser must skip to the next
  statement boundary (`;`, newline at the same or lower indentation level, or
  EOF) and continue parsing. This ensures that a syntax error in one statement
  does not prevent analysis of the rest of the file.
- R1.5.2: Skipped regions should be represented in the AST as an `ErrorStmt`
  node so that downstream passes can distinguish between "parsed successfully"
  and "recovered from error."

### 1.6 Declaration-Level Recovery

**Requirements:**

- R1.6.1: Simplify function declaration recovery to follow the same strategy as
  R1.1.1 rather than having its own ad-hoc recovery paths.
- R1.6.2: Other declaration types (type aliases, variable declarations, etc.)
  must also follow the common recovery strategy.

### 1.7 Type Annotation Recovery

**Problem:** The type annotation parser (`typeAnn()` / `primaryTypeAnn()` in
`type_ann.go`) uses the same Pratt-style precedence climbing algorithm as the
expression parser, but lacks the recovery guarantees that the expression parser
has. Specifically:

- `typeAnn()` returns `nil` on failure, unlike `expr()` which wraps
  `exprWithoutErrorCheck()` and guarantees non-nil by substituting `ErrorExpr`.
- There is no `ErrorTypeAnn` AST node, so callers of `typeAnn()` must handle
  `nil` returns, leading to inconsistent recovery across the codebase.
- The stack invariant check at the end of `typeAnn()` returns `nil` instead of
  a recovery node, mirroring the bug that was fixed in `expr.go`.
- `primaryTypeAnn()` returns `nil` on many error paths (missing operand after
  `keyof`, missing return type in function type, etc.), which propagates upward
  and can abort parsing of enclosing constructs.

**Requirements:**

- R1.7.1: Introduce an `ErrorTypeAnn` AST node analogous to `ErrorExpr`. It
  must contain only a `Span` field and implement the `TypeAnn` interface.
- R1.7.2: Create a wrapper function (e.g. `typeAnnRequired()`) analogous to
  `expr()` that calls `typeAnn()` and substitutes `ErrorTypeAnn` when the
  result is `nil`. Callers that require a non-nil type annotation after
  consuming a delimiter (e.g. after `|`, `&`, `:`, `->`) should use this
  wrapper. Prefix operators like `keyof` and `typeof` that call
  `primaryTypeAnn()` directly (to preserve precedence) should perform their
  own nil recovery instead — checking the result and substituting
  `ErrorTypeAnn` when nil.
- R1.7.3: The stack invariant check at the end of `typeAnn()` must return an
  `ErrorTypeAnn` instead of `nil`, matching the expression parser's behavior.
- R1.7.4: `ErrorTypeAnn` must be inferred as `ErrorType` by the type checker,
  consistent with how `ErrorExpr` is handled.
- R1.7.5: Downstream passes (checker, codegen, printer) must handle
  `ErrorTypeAnn` without panicking.

---

## 2. Error-Tolerant Type Inference

### 2.1 Introduce an Error Type

**Problem:** When the parser produces an incomplete or erroneous expression
(e.g. `ErrorExpr`, `MemberExpr` with an empty property), the type checker
currently assigns `NeverType`. Because `NeverType` is a bottom type, `never` is
assignable to everything (but nothing is assignable to `never`). This means an
expression with type `never` can flow into any context without triggering a type
error, causing false-positive silence — the type checker accepts code that would
otherwise be flagged.  Likewise, we don't want to use `UnknownType` either for
similar reasons.

**Requirements:**

- R2.1.1: Introduce a new type, `ErrorType`, in the type system. `ErrorType`
  must be distinct from `NeverType`, `AnyType`, `UnknownType`, and
  `WildcardType`.
- R2.1.2: `ErrorType` must unify with any other type without producing errors.
  This prevents cascading diagnostics from a single syntax error. The result of
  unification is simply success (no error) — the non-error type is used wherever
  the result matters.
- R2.1.3: `ErrorType` must not propagate through operations. The type checker
  must infer the result type as if the `ErrorType` operand were a valid type.
  For example, if one operand of `+` is `ErrorType` and the other is `number`,
  the result is `number`. Accessing a member of `ErrorType` returns `ErrorType`
  only because there is no valid type to infer (the object type is unknown).
  Calling an `ErrorType` returns `ErrorType` for the same reason.
- R2.1.4: The type checker must never introduce `ErrorType` when the source has
  no syntax errors. `ErrorType` is strictly for representing the types of
  syntactically invalid constructs.
- R2.1.5: `ErrorType` must have a human-readable string representation (e.g.
  `"<error>"`) for use in diagnostics and hover information.

### 2.2 Assign ErrorType to Error Nodes

**Requirements:**

- R2.2.1: `ErrorExpr` nodes must be inferred as `ErrorType`.
- R2.2.2: `MemberExpr` nodes with an empty property identifier must have their
  *property access* inferred as `ErrorType`, but the *object* subexpression must
  still be inferred normally. This is critical: `foo.` must still infer the type
  of `foo` so that completions can be offered.
- R2.2.3: Any other AST node produced by error recovery must be inferred as
  `ErrorType`.

### 2.3 Suppress Cascading Diagnostics

**Requirements:**

- R2.3.1: The type checker must not report type errors that involve `ErrorType`.
  For example, if `x` has type `ErrorType` and is used in `x + 1`, the result
  must be inferred as `number` with no error emitted — the checker treats
  `ErrorType` as compatible with the expected operand type.
- R2.3.2: The type checker must still report errors for well-formed expressions
  that have genuine type errors, even if they appear in the same file as syntax
  errors.

---

## 3. Completion Handler

### 3.1 Handler Registration

**Requirements:**

- R3.1.1: Register a `TextDocumentCompletion` handler in the LSP server.
- R3.1.2: Advertise completion capability in `ServerCapabilities` during
  `initialize`, including setting `.` as a trigger character. Do not set
  individual identifier characters as trigger characters — the client handles
  identifier-based triggering automatically when `CompletionOptions` is
  registered.
- R3.1.3: The handler must return `[]protocol.CompletionItem` (or
  `*protocol.CompletionList`).

### 3.2 Trigger Context

**Requirements:**

- R3.2.1: The handler must support `CompletionTriggerKind` values:
  - `Invoked` (manual trigger, e.g. Ctrl+Space)
  - `TriggerCharacter` (the user typed `.`)
  - `TriggerForIncompleteCompletions` (filtering a previous result)
- R3.2.2: When triggered by `.`, the handler must locate the `MemberExpr` whose
  dot position matches the trigger location and provide member completions.
- R3.2.3: When triggered by `Invoked` or while the user is typing an
  identifier, the handler must determine the context:
  - If the identifier is the property of a `MemberExpr` (e.g. `foo.ba|`),
    provide member completions filtered by the typed property name.
  - Otherwise (e.g. a bare identifier like `con|`), provide scope-based
    completions filtered by the typed text.

### 3.3 Finding the Relevant Node

**Requirements:**

- R3.3.1: Use the existing `findNodeInScript` visitor to locate the AST node at
  the cursor position. The visitor must also track the parent node of the found
  node (the current implementation does not do this and must be extended), so
  the completion handler can distinguish between a standalone `IdentExpr` and
  one that is the property of a `MemberExpr`.
- R3.3.2: If the node at the cursor is a `MemberExpr` with an empty property
  (or a property that is a prefix of what the user has typed), use the
  `InferredType` of the object subexpression as the basis for member completions.
- R3.3.3: If the node at the cursor is an `IdentExpr` whose parent node is a
  `MemberExpr`, use the `InferredType` of that `MemberExpr`'s object as the
  basis and filter by the identifier text.
- R3.3.4: If the node at the cursor is a standalone `IdentExpr` (not part of a
  `MemberExpr`), or the cursor is at a position where an expression is expected,
  provide scope-based completions filtered by the identifier text typed so far.

### 3.4 Resolving Completion Items from Types

**Prerequisites:**

Before resolving completions from a type, call `Prune` on the type to resolve
any `TypeVarType` to its bound value. If the result is still a `TypeVarType`
(i.e. the variable is unresolved), treat it the same as `AnyType` and return an
empty completion list.

**Requirements:**

- R3.4.1: For `ObjectType`, return `PropertyElem`, `MethodElem`, `GetterElem`,
  and `SetterElem` members as completion items. Set `CompletionItemKind`
  appropriately (`Field`, `Method`, `Property`). `CallableElem`,
  `ConstructorElem`, `MappedElem`, and `RestSpreadElem` do not have named
  members and should be skipped.
- R3.4.2: For `NamespaceType`, return all values, types, and child namespaces
  as completion items.
- R3.4.3: For `PrimType` and `LitType` (e.g. `"hello".`), resolve the wrapper
  type (`String`, `Number`, `Boolean`) by looking up the corresponding
  `TypeAlias` in scope, expand it to its underlying `ObjectType`, and return its
  members.
- R3.4.4: For `TupleType`, resolve to the `Array` interface and return its
  members (plus numeric indices if appropriate).
- R3.4.5: For `FuncType`, resolve to the `Function` interface and return its
  members.
- R3.4.6: For `UnionType`, return only members that are common to all non-null,
  non-undefined variants of the union. This matches the existing
  `getUnionAccess` behavior.
- R3.4.7: For `IntersectionType`, return the merged set of members from all
  parts, consistent with `getIntersectionAccess`.
- R3.4.8: For `TypeRefType`, expand the reference to its underlying type and
  recurse.
- R3.4.9: For `AnyType`, return an empty completion list (there is no useful set
  of members to suggest).
- R3.4.10: For `ErrorType` and `NeverType`, return an empty completion list.

### 3.5 Scope-Based Completions

**Problem:** When the user invokes completion outside of a member expression
(e.g. typing a partial identifier on a blank line, or pressing Ctrl+Space at an
expression position), the handler should suggest all in-scope identifiers. This
includes local variables, function parameters, outer-scope bindings, and globals
from the prelude.

**Requirements:**

- R3.5.1: When scope-based completion is triggered, walk the scope chain
  (starting from the innermost scope at the cursor position) and collect all
  bindings (values from `Scope.Values`). This includes local scopes, function
  scopes, and the module/script scope (top-level declarations). Identifiers
  introduced by import statements are only available in the source file scope,
  not the general module scope. In scripts, the file scope and script scope
  are the same; in modules, they are distinct.

  Within local and function scopes, only include bindings whose declaration
  appears before the cursor position. Variables declared after the cursor must
  not be suggested. The same applies to the top-level script scope, since
  scripts process statements in source order. This does not apply to the
  top-level module scope, where declarations are processed based on the
  dependency graph rather than source order.

  To determine the innermost scope at the cursor position, the type checker
  must record a mapping from AST nodes (or spans) to their enclosing `Scope`
  during inference. The completion handler uses this mapping together with the
  node found by `findNodeInScript` to look up the correct scope. (The current
  checker does not store this mapping and must be extended.)
- R3.5.2: Include globals and prelude bindings (e.g. `console`, `Math`,
  `parseInt`, etc.) that are in scope beyond the module/script scope.
- R3.5.3: Include type aliases from `Scope.Types` with `CompletionItemKind`
  set based on the underlying `ObjectType`: `Class` if `Nominal` is true,
  `Interface` if `Interface` is true, `Struct` otherwise.
- R3.5.4: Include child namespaces from `Scope.Namespaces` with
  `CompletionItemKind` set to `Module`.
- R3.5.5: If the user has typed a partial identifier, filter the results by
  that prefix (case-insensitive).
- R3.5.6: Do not include shadowed bindings. If a name appears in both an inner
  and outer scope, only the inner binding should be returned.

### 3.6 Result Limiting

**Problem:** Scope-based completions can produce a very large number of items
(especially when globals and prelude bindings are included). Returning all of
them can be slow and overwhelming in the UI.

**Requirements:**

- R3.6.1: The completion handler must limit the number of items returned. Use
  `CompletionList` with `IsIncomplete: true` when the result set is truncated,
  so the client knows to re-request as the user types more characters.
- R3.6.2: The default limit should be configurable but start at a reasonable
  value (e.g. 100 items).
- R3.6.3: When the result is truncated, prefer items that match the typed prefix
  more closely (e.g. exact case match before case-insensitive match).
- R3.6.4: Member completions (from `.` or `?.`) are typically small enough that
  they do not need truncation, but the limit must still apply as a safety
  measure.

### 3.7 Completion Item Details

**Requirements:**

- R3.7.1: Each completion item must include:
  - `Label`: the member name
  - `Kind`: the appropriate `CompletionItemKind`
  - `Detail`: the type of the member as a string (e.g. `"(a: number) => string"`)
- R3.7.2: Optional properties must be visually distinguished (e.g. by appending
  `?` to the label or including it in the detail).
- R3.7.3: Deprecated members (if tracked) should set `Deprecated: true` or use
  `Tags: [CompletionItemTagDeprecated]`.

### 3.8 Filtering

**Requirements:**

- R3.8.1: When the user has typed characters after the `.` (e.g. `foo.ba|`),
  the completion list must be filtered to items whose label starts with the
  typed prefix.
- R3.8.2: Filtering should be case-insensitive to improve discoverability.
- R3.8.3: The `FilterText` field on each `CompletionItem` must be set so that
  the client can perform additional filtering on its side.

### 3.9 Optional Chaining

**Requirements:**

- R3.9.1: Completions triggered after `?.` must behave identically to `.`
  completions, but on the non-nullable variant of the object type (strip `null`
  and `undefined` from unions before resolving members). Note that `?.` does
  not need its own trigger character — the client fires a `.` trigger when the
  user types the `.` in `?.`, and the re-parsed AST will contain a `MemberExpr`
  with `OptChain: true`, which the handler uses to detect this case.

---

## 4. Re-validation on Edit

### 4.1 Incremental Re-parse and Re-check

**Requirements:**

- R4.1.1: When the document changes (`textDocument/didChange`), re-parse and
  re-check the entire document. (Full re-parse is acceptable for now; incremental
  parsing is a future optimization.)
- R4.1.2: When a document is opened (`textDocument/didOpen`), re-parse and
  re-check it so that completions are available immediately without requiring
  the user to make an edit first.
- R4.1.3: The updated AST, inferred types, and scope mapping must be available
  before responding to any subsequent completion request. Access to the stored
  AST and type-checking results must be synchronized (e.g. via a mutex) to
  prevent data races between `didChange`/`didOpen` (which write) and
  `completion` (which reads).

---

## 5. Testing

### 5.1 Parser Recovery Tests

**Requirements:**

- R5.1.1: Add tests for parsing `expr.`, `expr?.`, `expr.pa` (partial
  identifier), and `expr.\n` (dot at end of line).
- R5.1.2: Add tests for expression-level recovery: ensure that an incomplete
  subexpression does not prevent the surrounding expression from being parsed.
  For example, `a. + b` must still parse as a `BinaryExpr` with an incomplete
  `MemberExpr` on the left side. Similarly, `foo(a, ) + 1` must parse as a
  `BinaryExpr` with a `CallExpr` (containing an `ErrorExpr` argument) on the
  left.
- R5.1.3: Add tests for recovery within statements: ensure that a syntax error
  in one statement does not prevent subsequent statements from being parsed.
- R5.1.4: Add tests for recovery within declarations: ensure that a syntax
  error in a function body does not prevent subsequent declarations from being
  parsed.

### 5.2 Type Inference Tests

**Requirements:**

- R5.2.1: Add tests that `ErrorType` is assigned to `ErrorExpr` nodes.
- R5.2.2: Add tests that `ErrorType` does not cause cascading type errors.
- R5.2.3: Add tests that the object in `foo.` is still correctly inferred even
  when the property is missing.

### 5.3 Completion Tests

**Requirements:**

- R5.3.1: Test completions for object types (`obj.` where obj is `{a: number, b: string}`).
- R5.3.2: Test completions for primitive wrapper types (`"hello".`, `(42).`).
- R5.3.3: Test completions for namespace types.
- R5.3.4: Test completions for union types (only common members).
- R5.3.5: Test completions with partial identifier filtering (`obj.to` should
  include `toString`, `toFixed`, etc.).
- R5.3.6: Test completions with optional chaining (`obj?.`).
- R5.3.7: Test that completions return empty for `ErrorType` and `NeverType`.
- R5.3.8: Test scope-based completions: locals, parameters, outer-scope
  bindings, and globals are all included.
- R5.3.9: Test that shadowed bindings are excluded from scope-based completions.
- R5.3.10: Test result limiting: when the candidate set exceeds the limit,
  `IsIncomplete` is `true` and only the top results are returned.
- R5.3.11: Test that scope-based completions filter by typed prefix.

---

## 6. Non-Requirements (Out of Scope)

- **Incremental parsing**: Full re-parse on every edit is acceptable.
- **Signature help** (`textDocument/signatureHelp`): Not part of this work.
- **Auto-import**: Not part of this work.
- **Snippet completions**: Not part of this work (e.g. inserting `()` after
  selecting a method).
- **Documentation in completion items**: Not required unless doc comments are
  already tracked in the type system.
