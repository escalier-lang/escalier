# Iterator Protocol Implementation Plan

This document outlines the implementation strategy for adding iterator protocol support to Escalier, based on the requirements in [requirements.md](requirements.md).

## Current State Analysis

### Already Implemented
- **Spread syntax**: `RestSpreadExpr` fully implemented in parser, type checker, and codegen. `RestSpreadExpr` now implements both `Expr` and `ObjExprElem` interfaces, enabling spread in both array and object contexts.
- **Array spread with iterable check**: The parser supports `...expr` inside array literals (`[1, 2, ...arr]`). The type checker validates that the spread operand implements `Iterable<T>` and extracts the element type. Non-iterable types produce a clear error.
- **Async functions**: `async fn` with `await` expressions fully supported
- **Tokens**: `Yield`, `From`, `In`, `Async`, `Await` tokens already defined in lexer
- **Standard library**: TypeScript lib.es2015.d.ts and iterable definitions loaded. Verified: `Iterator<T, TReturn, TNext>`, `Iterable<T, TReturn, TNext>`, `IterableIterator<T, TReturn, TNext>`, `Generator<T, TReturn, TNext>`, `IteratorResult<T, TReturn>` all load correctly.
- **Symbol.iterator property lookup**: The checker resolves `[Symbol.iterator]` for index access on `ObjectType`, `TypeRefType` (including `Array`), `PrimType` (e.g. `string`), and `LitType`. Implemented in `expand_type.go:getObjectAccess`.
- **`GetIterableElementType` helper**: `internal/checker/iterable.go` provides `GetIterableElementType(ctx, type)` which extracts element type T from any `Iterable<T>` by looking up `[Symbol.iterator]()` and extracting T from the returned Iterator type. Handles `TupleType` directly (union of element types), `TypeRefType` (first type arg), and `ObjectType` (via `next()` method).

### Not Yet Loaded
- **AsyncGenerator**: Requires ES2018+ lib files which are not currently loaded (target is `"es2015"`).

### Implemented (Phases 1–4)
- `for...in` loop statement (parsing + type checking)
- `for await...in` loop statement (parsing + type checking)
- `yield` and `yield from` expressions (parsing + type checking)
- Generator detection (functions containing `yield` are generators)
- Async generators (async functions containing `yield`)
- `yield`/`yield from` rejected at module scope (outside functions)
- Generator return type annotation mismatch detection

### Not Yet Implemented
- Code generation for `for...in`, `yield`, and generator functions (Phase 5)
- Comprehensive end-to-end testing (Phase 6)

---

## Phase 0: Verification and Foundation ✅ COMPLETED

Before implementing new iterator features, verify foundational pieces and derisk the implementation by testing critical assumptions.

### 0.1 Verify Standard Library Types Load Correctly ✅

**Status**: Completed. Tests in `internal/checker/tests/iterator_test.go:TestStdLibIteratorTypesLoaded`.

**Findings**:
- `Iterator<T, TReturn, TNext>` — 3 type params ✅
- `Iterable<T, TReturn, TNext>` — 3 type params (not 1 as originally assumed; TypeScript's definition includes TReturn and TNext with defaults)
- `IterableIterator<T, TReturn, TNext>` — 3 type params ✅
- `Generator<T, TReturn, TNext>` — 3 type params ✅
- `IteratorResult<T, TReturn>` — 2 type params ✅
- `SymbolConstructor` has `iterator` property with `UniqueSymbolType` ✅
- `AsyncGenerator` — NOT loaded (requires ES2018+ lib files; current target is `"es2015"`)

### 0.2 Verify Symbol.iterator Property Lookup ✅

**Status**: Completed. Required code changes + tests in `internal/checker/tests/iterator_test.go:TestSymbolIteratorLookup`.

**Changes made**:
- **`internal/checker/expand_type.go`** — Added `isSymbolIndexKey(key)` helper that checks if a `MemberAccessKey` is an `IndexKey` with a `UniqueSymbolType` (e.g. `Symbol.iterator`). Used in three places to avoid duplicated unwrap-and-check logic.
- **`internal/checker/expand_type.go`** — `getObjectAccess` `IndexKey` case: added handling for `UniqueSymbolType` keys to match against `SymObjTypeKeyKind` object elements (previously only `StrLit` keys were handled)
- **`internal/checker/expand_type.go`** — `getMemberType` `TypeRefType` case: Array numeric index access now uses `!isSymbolIndexKey(key)` to skip symbol keys so they fall through to type alias expansion (previously `arr[Symbol.iterator]` was incorrectly treated as numeric indexing)
- **`internal/checker/expand_type.go`** — `PrimType` and `LitType` cases: now delegate to wrapper types when the key is a `PropertyKey` or a symbol index key, using `isSymbolIndexKey(key)` (e.g. `string[Symbol.iterator]` delegates to `String`)

### 0.3 Implement GetIterableElementType ✅

**Status**: Completed. Implementation in `internal/checker/iterable.go`, tests in `internal/checker/tests/iterator_test.go:TestGetIterableElementType`.

**Implementation** (`internal/checker/iterable.go`):
- `getSymbolIteratorID()` — retrieves the Symbol.iterator unique symbol ID from `SymbolConstructor` in the global scope
- `GetIterableElementType(ctx, type)` — looks up `[Symbol.iterator]` on the type via `getMemberType`, gets the return type of the function, then extracts T (first type arg) from the Iterator-like return type
- `extractIteratorElementType(ctx, type)` — extracts T from `TypeRefType` (first type arg, after verifying the type name contains "Iterator" or equals "Generator") or `ObjectType` (via `next()` method returning `IteratorResult<T, TReturn>`)
- Direct `TupleType` handling — returns union of element types (tuples are always iterable)

**Tested types**:
- `Array<number>` → `number` ✅
- `Array<string>` → `string` ✅
- `string` → `string` ✅
- `number` → `nil` (not iterable) ✅
- `boolean` → `nil` (not iterable) ✅

### 0.4 Array Spread Iterable Check ✅

**Status**: Completed. Required parser + checker changes + tests in `internal/checker/tests/iterator_test.go:TestArraySpreadRequiresIterable`.

**Changes made**:
- **`internal/ast/expr.go`** — `RestSpreadExpr` now implements the `Expr` interface (added `isExpr()`, `InferredType()`, `SetInferredType()`, and `inferredType` field) so it can appear in `TupleExpr.Elems`. `TupleExpr.Accept` handles `RestSpreadExpr` elements specially, visiting them via `EnterExpr`/`ExitExpr` (not `EnterObjExprElem`/`ExitObjExprElem`) so visitors in array context see spread elements correctly.
- **`internal/parser/expr.go`** — Added `arrayElem()` method that handles `...expr` inside array literals; array literal parsing now uses `parseDelimSeq(p, CloseBracket, Comma, p.arrayElem)` instead of `p.expr`
- **`internal/checker/infer_expr.go`** — `TupleExpr` case now checks for `*ast.RestSpreadExpr` elements, calls `GetIterableElementType` to validate iterability, and wraps the result as `RestSpreadType(Array<elementType>)`

**Test cases**:
- `[1, 2, ...[3, 4]]` — valid (tuple spread) ✅
- `[1, 2, ...nums]` where `nums: Array<number>` — valid ✅
- `[..."hello"]` — valid (string is iterable) ✅
- `[...5]` — error: "Type '5' is not iterable" ✅
- `[...{a: 1}]` — error: non-iterable object ✅

**Note**: Object spread (`{...obj}`) does NOT require `Iterable<T>` — it copies enumerable own properties. This is unchanged.

### 0.5 Refactor Await Throw Collection to Use Context Pointers

**Why this matters**: The generator implementation (Phase 4) relies on a pattern where type information is collected during inference via shared context pointers, avoiding a second AST traversal. The existing `await` implementation already uses a similar two-pass approach that can be refactored to validate this pattern before building generators on top of it.

**Current approach** (two passes in `internal/checker/infer_func.go`):
1. During inference: `AwaitExpr` case in `infer_expr.go` unwraps `Promise<T, E>` and stores `Throws` on the `AwaitExpr` node
2. Post-inference: `AwaitVisitor` walks the AST a second time to collect `Throws` from all `AwaitExpr` nodes in `findThrowTypes`

**Proposed approach** (single pass, matching the yield pattern):
1. Add `AwaitThrowTypes *[]type_system.Type` to `Context` (pointer, like `YieldedTypes`)
2. In `inferFuncBodyWithFuncSigType`, allocate a fresh pointer when `isAsync` is true
3. In the `AwaitExpr` case in `infer_expr.go`, append the throws type to `*ctx.AwaitThrowTypes` during inference (replacing `expr.Throws = ...`)
4. In `findThrowTypes`, use the collected `*ctx.AwaitThrowTypes` instead of running `AwaitVisitor`
5. Remove `AwaitVisitor` and the `Throws` field from `AwaitExpr`

**Changes**:

**File**: `internal/checker/checker.go`
```go
type Context struct {
    // ... existing fields ...

    // Async tracking - pointer so block scopes share state within a function
    AwaitThrowTypes *[]type_system.Type  // Throw types from await expressions
}
```

Update `WithNewScope`, `WithNewScopeAndNamespace`, and `WithScope` to propagate `AwaitThrowTypes`.

**File**: `internal/checker/infer_func.go`
```go
func (c *Checker) inferFuncBodyWithFuncSigType(
    ctx Context,
    funcSigType *type_system.FuncType,
    paramBindings map[string]*type_system.Binding,
    body *ast.Block,
    isAsync bool,
) []Error {
    errors := []Error{}

    // Allocate fresh pointer for await throw tracking
    awaitThrowTypes := []type_system.Type{}

    bodyCtx := ctx.WithNewScope()
    bodyCtx.IsAsync = isAsync
    bodyCtx.AwaitThrowTypes = &awaitThrowTypes  // Fresh pointer for this function

    returnType, inferredThrowType, bodyErrors := c.inferFuncBody(bodyCtx, paramBindings, body)
    errors = slices.Concat(errors, bodyErrors)

    // Incorporate await throw types into the inferred throw type
    // (previously collected by AwaitVisitor in findThrowTypes)
    if isAsync && len(awaitThrowTypes) > 0 {
        inferredThrowType = type_system.NewUnionType(nil,
            append([]type_system.Type{inferredThrowType}, awaitThrowTypes...)...)
    }

    // ... rest of existing async/sync logic ...
}
```

**File**: `internal/checker/infer_expr.go`
```go
case *ast.AwaitExpr:
    if !ctx.IsAsync {
        // ... existing error handling ...
    } else {
        argType, argErrors := c.inferExpr(ctx, expr.Arg)
        errors = argErrors

        if promiseType, ok := argType.(*type_system.TypeRefType); ok &&
            type_system.QualIdentToString(promiseType.Name) == "Promise" {
            if len(promiseType.TypeArgs) >= 1 {
                resultType = promiseType.TypeArgs[0]
            }
            // Record throws type via context pointer (replaces expr.Throws)
            if len(promiseType.TypeArgs) >= 2 && ctx.AwaitThrowTypes != nil {
                *ctx.AwaitThrowTypes = append(*ctx.AwaitThrowTypes, promiseType.TypeArgs[1])
            }
        }
    }
```

**File**: `internal/checker/infer_func.go` — Remove `AwaitVisitor` struct and update `findThrowTypes` to no longer use it.

**File**: `internal/ast/expr.go` — Remove `Throws` field from `AwaitExpr` (no longer needed since throw types are collected via context).

**Benefits**:
- Validates the context-pointer pattern that generators will use (derisks Phase 4)
- Eliminates a redundant AST traversal
- Simplifies `AwaitExpr` by removing its `Throws` field
- Makes the async and generator patterns consistent

**Test plan**: All existing async function tests should continue to pass. Specifically verify that `Promise<T, E>` rejection types still propagate correctly to the function's throws type.

---

## Phase 1: AST Extensions ✅ COMPLETED

Implemented in commit `62a39ce` (Update parser to parse for-in and yield (#345)).

### 1.1 Add ForInStmt to Statement AST

**File**: `internal/ast/stmt.go`

Add a new statement type for for...in loops:

```go
type ForInStmt struct {
    Pattern  Pat       // Loop variable pattern (supports destructuring)
    Iterable Expr      // Expression being iterated
    Body     []Stmt    // Loop body statements
    IsAwait  bool      // true for `for await...in`
    span     Span
}

func NewForInStmt(pattern Pat, iterable Expr, body []Stmt, isAwait bool, span Span) *ForInStmt {
    return &ForInStmt{
        Pattern:  pattern,
        Iterable: iterable,
        Body:     body,
        IsAwait:  isAwait,
        span:     span,
    }
}

func (*ForInStmt) isStmt()       {}
func (s *ForInStmt) Span() Span  { return s.span }
func (s *ForInStmt) Accept(v Visitor) {
    if v.EnterStmt(s) {
        s.Pattern.Accept(v)
        s.Iterable.Accept(v)
        for _, stmt := range s.Body {
            stmt.Accept(v)
        }
    }
    v.ExitStmt(s)
}
```

### 1.2 Add YieldExpr to Expression AST

**File**: `internal/ast/expr.go`

Add yield expression:

```go
type YieldExpr struct {
    Value        Expr   // The yielded value (nil for bare `yield`)
    IsDelegate   bool   // true for `yield from` (compiles to yield*)
    span         Span
    inferredType Type
}

func NewYieldExpr(value Expr, isDelegate bool, span Span) *YieldExpr {
    return &YieldExpr{
        Value:      value,
        IsDelegate: isDelegate,
        span:       span,
    }
}

func (*YieldExpr) isExpr()                     {}
func (e *YieldExpr) Span() Span                { return e.span }
func (e *YieldExpr) InferredType() Type         { return e.inferredType }
func (e *YieldExpr) SetInferredType(t Type)     { e.inferredType = t }
func (e *YieldExpr) Accept(v Visitor) {
    if v.EnterExpr(e) {
        if e.Value != nil {
            e.Value.Accept(v)
        }
    }
    v.ExitExpr(e)
}
```

**Note**: No changes needed to `FuncSig` - generator functions are detected by the presence of `yield` expressions in the function body during type checking and code generation.

---

## Phase 2: Parser Extensions ✅ COMPLETED

Implemented in commit `62a39ce` (Update parser to parse for-in and yield (#345)).

### 2.1 Parse For-In Loops

**File**: `internal/parser/stmt.go`

Add parsing for `for...in` and `for await...in` loops:

```go
func (p *Parser) parseForInStmt() *ast.ForInStmt {
    startSpan := p.lexer.current().Span

    // Parse 'for' keyword
    p.lexer.consume() // consume 'for'

    // Check for 'await' keyword
    isAwait := false
    if p.lexer.current().Type == Await {
        isAwait = true
        p.lexer.consume()
    }

    // Parse pattern (loop variable)
    pattern := p.parsePattern()

    // Expect 'in' keyword
    p.expect(In)

    // Parse iterable expression
    iterable := p.expr()

    // Parse body block
    p.expect(LBrace)
    body := p.stmtList()
    endSpan := p.lexer.current().Span
    p.expect(RBrace)

    return ast.NewForInStmt(pattern, iterable, body, isAwait,
        ast.MergeSpans(startSpan, endSpan))
}
```

**Integration**: Add case in `stmt()` function:

```go
case For:
    return p.parseForInStmt()
```

### 2.2 Parse Yield Expressions

**File**: `internal/parser/expr.go`

Handle `yield` and `yield from` as prefix unary operators in the expression parser, with precedence `2`. This is lower than all binary operators (the lowest being `||` and `??` at precedence `3`), so the operand of `yield` is always a complete expression. For example, `yield 1 + 2` parses as `yield (1 + 2)`.

**Precedence table update**:

The `Precedence` table should be extended to include yield at precedence 2. This documents that yield binds less tightly than any binary operator, and is relevant if the precedence-climbing parser is later generalized to support prefix operators with custom precedence.

**Integration**: Add `yield` and `yield from` as cases in `primaryExpr()`, following the same pattern as `await` (line 416):

```go
// In primaryExpr(), add case alongside Await:
case Yield:
    p.lexer.consume() // consume 'yield'
    startSpan := token.Span

    // Check for 'from' keyword (yield from for delegation)
    isDelegate := false
    if p.lexer.peek().Type == From {
        isDelegate = true
        p.lexer.consume()
    }

    // Parse the yielded expression
    var value ast.Expr
    if isDelegate {
        // 'yield from' REQUIRES an expression (the iterable to delegate to)
        if p.isStatementTerminator() {
            p.reportError(startSpan, "'yield from' requires an iterable expression")
            return ast.NewYieldExpr(nil, true, startSpan)
        }
        value = p.expr()
    } else {
        // Regular 'yield' can optionally have an expression
        if !p.isStatementTerminator() {
            value = p.expr()
        }
    }

    endSpan := startSpan
    if value != nil {
        endSpan = value.Span()
    }

    expr = ast.NewYieldExpr(value, isDelegate, ast.MergeSpans(startSpan, endSpan))
```

**Why `primaryExpr()`?** Like `await`, `yield` is a keyword-prefixed unary operator. Since its precedence (2) is lower than all binary operators (3+), `p.expr()` correctly parses the full operand expression. Handling it in `primaryExpr()` matches the existing pattern for `await` and avoids a separate parsing function.

**Note**: No special parsing needed for generator functions - any function containing `yield` is automatically a generator. The type checker and code generator detect this by scanning the function body for yield expressions.

---

## Phase 3: Type System Extensions ✅ COMPLETED

Implemented in commits `cba3445` (implement type checking of for-in and generator functions) and `13eebdd` (initial revisions).

### 3.1 Generator Type Handling

**Status**: Completed. Generator and AsyncGenerator types are constructed inline using `type_system.NewTypeRefType` with the looked-up type alias (no separate helper functions needed). The `MakeGeneratorType`/`MakeAsyncGeneratorType` helpers were initially added then inlined since they were only used in one place.

### 3.2 Iterable Type Extraction

**Status**: Completed. All utilities implemented in `internal/checker/iterable.go`:

- **`getSymbolID(name string) (int, bool)`** — Shared helper that looks up a unique symbol ID from `SymbolConstructor` (e.g. `"iterator"` or `"asyncIterator"`).
- **`GetIterableElementType(ctx, t)`** — Already existed; extracts T from `Iterable<T>`. Handles `UnionType`, `TupleType` (including `RestSpreadType` elements), and general types via `[Symbol.iterator]()` lookup.
- **`GetAsyncIterableElementType(ctx, t)`** — New; extracts T from `AsyncIterable<T>` via `[Symbol.asyncIterator]()` lookup. Uses shared `getSymbolID` helper.
- **`GetIteratorReturnType(ctx, t)`** — New; extracts `TReturn` from an iterable's iterator. Handles `UnionType` (recurses per branch) and `TupleType` (returns `void`). Uses shared `unifyIteratorNextReturn` helper.
- **`unifyIteratorNextReturn(ctx, t)`** — Shared helper that looks up `next()` on an iterator type and unifies its return with `IteratorResult<freshT, freshTReturn>`. Returns both `(T, TReturn)`.
- **`extractIteratorElementType(ctx, t)`** — Refactored to delegate to `unifyIteratorNextReturn`, returning just the element type.

---

## Phase 4: Type Checker Extensions ✅ COMPLETED

Implemented in commits `cba3445` (implement type checking of for-in and generator functions) and `13eebdd` (initial revisions), with additional guards added in the current working tree.

### 4.1 Infer For-In Loop Types

**Status**: Completed in `internal/checker/infer_stmt.go:inferForInStmt`.

**File**: `internal/checker/infer_stmt.go`

Add type inference for for-in loops:

```go
func (c *Checker) inferForInStmt(ctx *Context, stmt *ast.ForInStmt) []Error {
    errors := []Error{}

    // Validate async context for 'for await'
    if stmt.IsAwait && !ctx.IsAsync {
        errors = append(errors, Error{
            Message: "'for await' is only allowed in async functions",
            Span:    stmt.Span(),
        })
    }

    // Infer the type of the iterable expression
    iterableType, errs := c.inferExpr(ctx, stmt.Iterable)
    errors = slices.Concat(errors, errs)

    // Extract element type from Iterable<T> or AsyncIterable<T>
    var elementType Type
    if stmt.IsAwait {
        // for await...in can iterate over BOTH async iterables and sync iterables.
        // In JS, `for await (x of syncIterable)` awaits each value from the sync iterator.
        // Try async iterable first, then fall back to sync iterable.
        elementType = c.getAsyncIterableElementType(iterableType)
        if elementType == nil {
            // Fallback: try sync iterable (values will be awaited)
            elementType = c.getIterableElementType(iterableType)
        }
        if elementType == nil {
            errors = append(errors, Error{
                Message: fmt.Sprintf("Type '%s' is not iterable", iterableType),
                Span:    stmt.Iterable.Span(),
            })
            elementType = AnyType{}
        }
    } else {
        elementType = c.getIterableElementType(iterableType)
        if elementType == nil {
            errors = append(errors, Error{
                Message: fmt.Sprintf("Type '%s' is not iterable", iterableType),
                Span:    stmt.Iterable.Span(),
            })
            elementType = AnyType{}
        }
    }

    // Create new scope for loop body
    loopCtx := ctx.WithNewScope()

    // Bind pattern variables with inferred element type.
    // Bindings should have Mutable: false (like `val` declarations),
    // making the loop variable non-reassignable.
    bindings, patErrors := c.inferPatternWithType(stmt.Pattern, elementType)
    errors = slices.Concat(errors, patErrors)
    for name, binding := range bindings {
        binding.Mutable = false  // Loop variables are immutable
        loopCtx.Scope.setValue(name, binding)
    }

    // Infer body statements
    for _, bodyStmt := range stmt.Body {
        errs := c.inferStmt(loopCtx, bodyStmt)
        errors = slices.Concat(errors, errs)
    }

    return errors
}
```

### 4.2 Infer Yield Expressions

**Status**: Completed in `internal/checker/infer_expr.go` (case `*ast.YieldExpr`). Includes:
- Guard rejecting `yield`/`yield from` at module scope (when `ctx.ContainsYield` is nil)
- Regular yield tracking via `ctx.AddYieldedType`
- `yield from` delegation with iterable validation and `TReturn` extraction
- Async generator support (`yield from` tries async iterable first, then sync)
- `TNext` defaults to `never` (documented in code comments)

**File**: `internal/checker/infer_expr.go`

Add yield expression inference:

```go
func (c *Checker) inferYieldExpr(ctx *Context, expr *ast.YieldExpr) (Type, []Error) {
    errors := []Error{}

    // Mark this function context as containing yield (makes it a generator)
    // Uses pointer dereference so all block scopes within the function see this
    if ctx.ContainsYield != nil {
        *ctx.ContainsYield = true
    }

    if expr.IsDelegate {
        // yield from: the value must be iterable
        if expr.Value == nil {
            // Parser should have already reported this error, but handle gracefully
            errors = append(errors, Error{
                Message: "'yield from' requires an iterable expression",
                Span:    expr.Span(),
            })
            return UnknownType{}, errors
        }

        valueType, errs := c.inferExpr(ctx, expr.Value)
        errors = slices.Concat(errors, errs)

        // In async generators, yield* can delegate to both async and sync iterables
        // (matching the for-await fallback behavior). Try async first, then sync.
        var elementType Type
        if ctx.IsAsync {
            elementType = c.getAsyncIterableElementType(valueType)
            if elementType == nil {
                elementType = c.getIterableElementType(valueType)
            }
        } else {
            elementType = c.getIterableElementType(valueType)
        }

        if elementType == nil {
            errors = append(errors, Error{
                Message: fmt.Sprintf("Type '%s' is not iterable", valueType),
                Span:    expr.Value.Span(),
            })
        }

        // Only record yielded type when non-nil to avoid nil entries in unionTypes()
        if elementType != nil {
            ctx.AddYieldedType(elementType)
        }

        // The yield* expression evaluates to TReturn of the delegated generator.
        // For simplicity, we can start with `unknown` and refine later if needed.
        // Most code doesn't use the return value of yield*.
        delegatedReturnType := c.getIteratorReturnType(valueType)
        if delegatedReturnType == nil {
            delegatedReturnType = UnknownType{}
        }
        return delegatedReturnType, errors
    }

    // Regular yield (with or without value)
    if expr.Value != nil {
        valueType, errs := c.inferExpr(ctx, expr.Value)
        errors = slices.Concat(errors, errs)

        // Track yielded types - contributes to T in Generator<T, TReturn, TNext>
        ctx.AddYieldedType(valueType)
    } else {
        // Bare `yield` yields undefined - record it so Generator<T,...> includes it
        ctx.AddYieldedType(type_system.NewUndefinedType(provenance))
    }

    // The yield expression evaluates to TNext (value passed to .next())
    // TNext defaults to `never` since most generators are consumed via
    // for...of loops rather than manual .next(value) calls. If code needs
    // to pass values, it can explicitly annotate the generator type.
    if ctx.GeneratorNextType == nil {
        return NeverType{}, errors
    }
    return ctx.GeneratorNextType, errors
}
```

### 4.3 Infer Generator Functions

**Status**: Completed in `internal/checker/infer_func.go:inferFuncBodyWithFuncSigType`. The inferred Generator/AsyncGenerator type is unified against any declared return annotation before assignment, producing a type error on mismatch (e.g. `fn g() -> number { yield 1 }`).

**File**: `internal/checker/infer_func.go`

Generator detection belongs in `inferFuncBodyWithFuncSigType`, which is the shared function called by all three sites that can produce generators:

- `inferFuncDecl` in `infer_stmt.go` (function declarations)
- `case *ast.FuncExpr` in `infer_expr.go` (function expressions / lambdas)
- `case *ast.MethodExpr` in `infer_expr.go` (object/class methods)

By placing the generator logic here, all three automatically gain generator support.

Modify `inferFuncBodyWithFuncSigType` to allocate fresh `ContainsYield`/`YieldedTypes` pointers, then check after body inference:

```go
func (c *Checker) inferFuncBodyWithFuncSigType(
    ctx Context,
    funcSigType *type_system.FuncType,
    paramBindings map[string]*type_system.Binding,
    body *ast.Block,
    isAsync bool,
) []Error {
    errors := []Error{}

    // Allocate fresh pointers for generator tracking - this function gets its own
    // tracking independent of any enclosing function
    containsYield := false
    yieldedTypes := []type_system.Type{}

    bodyCtx := ctx.WithNewScope()
    bodyCtx.IsAsync = isAsync
    bodyCtx.ContainsYield = &containsYield   // Fresh pointer for this function
    bodyCtx.YieldedTypes = &yieldedTypes     // Fresh pointer for this function

    returnType, inferredThrowType, bodyErrors := c.inferFuncBody(bodyCtx, paramBindings, body)
    errors = slices.Concat(errors, bodyErrors)

    // Check if this function is a generator (contains yield)
    if containsYield {
        yieldType := c.unionTypes(yieldedTypes)
        nextType := type_system.NewNeverType(nil)

        if isAsync {
            // async function* -> AsyncGenerator<T, TReturn, TNext>
            funcSigType.Return = MakeAsyncGeneratorType(yieldType, returnType, nextType)
        } else {
            // function* -> Generator<T, TReturn, TNext>
            funcSigType.Return = MakeGeneratorType(yieldType, returnType, nextType)
        }
        funcSigType.Throws = type_system.NewNeverType(nil)
        return errors
    }

    // ... rest of existing logic for regular (non-generator) functions ...
    if isAsync {
        // existing async Promise wrapping logic ...
    } else {
        unifyReturnErrors := c.Unify(ctx, returnType, funcSigType.Return)
        unifyThrowsErrors := c.Unify(ctx, inferredThrowType, funcSigType.Throws)
        errors = slices.Concat(errors, unifyReturnErrors, unifyThrowsErrors)
    }

    return errors
}
```

This approach means `inferFuncDecl`, `FuncExpr`, and `MethodExpr` all get generator support without any changes to their callsites.

### 4.4 Add Context Fields

**Status**: Completed in `internal/checker/checker.go`. All three fields added, `AddYieldedType` helper added, and all `WithNewScope`/`WithNewScopeAndNamespace`/`WithScope` methods updated to propagate the pointers.

**File**: `internal/checker/checker.go`

Extend the checker context with generator-related fields. These must be **pointers** so that block scopes within the same function share the same underlying values, while nested functions can allocate fresh values:

```go
type Context struct {
    // ... existing fields (Scope, IsAsync, IsPatMatch, etc.) ...

    // Generator tracking:
    // ContainsYield and YieldedTypes are pointers because they are accumulators
    // mutated during traversal — block scopes must share the same underlying
    // values so that yields inside if/while/etc. propagate to the enclosing
    // function. Nested functions allocate fresh pointers to isolate their state.
    // GeneratorNextType is a plain Type (not a pointer) because it is a read-only
    // per-function configuration value set once when entering a function and
    // simply copied by value into block scopes.
    ContainsYield     *bool             // Set to true when yield is encountered
    YieldedTypes      *[]Type           // Types of all yield expressions
    GeneratorNextType Type              // TNext type for this generator
}

// Helper method to add a yielded type (handles nil check and dereferencing)
func (ctx *Context) AddYieldedType(t Type) {
    if ctx.YieldedTypes != nil {
        *ctx.YieldedTypes = append(*ctx.YieldedTypes, t)
    }
}
```

**Update scope helpers**: The `WithNewScope`, `WithNewScopeAndNamespace`, and `WithScope` methods copy the pointers (sharing the underlying values for block scopes):

```go
func (ctx *Context) WithNewScope() Context {
    return Context{
        Scope:                  ctx.Scope.WithNewScope(),
        IsAsync:                ctx.IsAsync,
        IsPatMatch:             ctx.IsPatMatch,
        AllowUndefinedTypeRefs: ctx.AllowUndefinedTypeRefs,
        TypeRefsToUpdate:       ctx.TypeRefsToUpdate,
        FileScopes:             ctx.FileScopes,
        Module:                 ctx.Module,
        // Generator fields - copy pointers so block scopes share state
        ContainsYield:          ctx.ContainsYield,
        YieldedTypes:           ctx.YieldedTypes,
        GeneratorNextType:      ctx.GeneratorNextType,
    }
}
```

Similarly update `WithNewScopeAndNamespace` and `WithScope`.

**Why pointers?** When a `yield` appears inside an `if` block or loop body, it must mark the enclosing *function* as a generator. By sharing pointers, all block scopes within a function update the same underlying `ContainsYield` and `YieldedTypes`. Function scopes allocate fresh pointers (see sections 4.3 and 4.5) so nested functions get independent tracking.

**Note**: Loop variable immutability is handled by binding them as `val` (not `var`). The existing type system already prevents reassignment of `val` bindings, so no additional tracking is needed.

### 4.5 Yield Context Scoping

**Status**: Completed. Tested in `TestGeneratorFunctionDetection/NestedYieldDoesNotAffectOuter`.

**Important**: Each call to `inferFuncBodyWithFuncSigType` allocates fresh `ContainsYield`/`YieldedTypes` pointers (see section 4.3). This naturally creates a new generator-tracking scope for every function boundary. Since all three callsites flow through it:

- `inferFuncDecl` (in `infer_stmt.go`) → `inferFuncBodyWithFuncSigType`
- `case *ast.FuncExpr` (in `infer_expr.go`) → `inferFuncBodyWithFuncSigType`
- `case *ast.MethodExpr` (in `infer_expr.go`) → `inferFuncBodyWithFuncSigType`

...nested functions automatically get independent tracking. This ensures:

1. `yield` in a nested function only makes *that* function a generator
2. The outer function's generator status is unaffected by nested yields
3. Using `yield` inside a `.forEach()` callback creates a generator callback, not a generator outer function

Block scopes created by `WithNewScope` (for `if`/`while`/`for` bodies) copy the pointers, so yields anywhere in the function body share the same tracking state.

### 4.6 Async Context Validation

**Status**: Completed. `for await` in non-async context produces an error. `yield`/`yield from` outside a function produces an error.

The type checker must validate async context for iterator-related constructs:

1. **`for await` in non-async functions** (new check, added in 4.1):
   - Error: `'for await' is only allowed in async functions`

2. **`await` in non-async functions** (existing check, verify it works):
   - Error: `'await' expression is only allowed in async functions`
   - This should already be handled by existing `AwaitExpr` inference
   - Verify it works correctly in generators: `fn gen() { yield await x }` should error

3. **Async generators** (combination of async + yield):
   - `async fn` containing `yield` produces `AsyncGenerator<T, TReturn, TNext>`
   - Both `await` and `yield` are valid in async generators

**Verification**: Ensure existing `await` checking in `inferAwaitExpr` properly checks `ctx.IsAsync` and reports errors. The iterator implementation should not break this existing behavior.

---

## Phase 5: Code Generation

### 5.1 Generate For-In Loops

**File**: `internal/codegen/builder.go`

Transform Escalier for-in to JavaScript for-of:

```go
func (b *Builder) buildForInStmt(stmt *ast.ForInStmt) cg.Stmt {
    // Build pattern as JS destructuring pattern
    pattern := b.buildPattern(stmt.Pattern)

    // Build iterable expression
    iterable := b.buildExpr(stmt.Iterable)

    // Build body statements
    body := []cg.Stmt{}
    for _, s := range stmt.Body {
        body = append(body, b.buildStmt(s))
    }

    return cg.NewForOfStmt(pattern, iterable, body, stmt.IsAwait)
}
```

**File**: `internal/codegen/ast.go`

Add ForOfStmt to codegen AST:

```go
type ForOfStmt struct {
    Pattern  Pattern
    Iterable Expr
    Body     []Stmt
    IsAwait  bool
}
```

### 5.2 Generate Yield Expressions

**File**: `internal/codegen/builder.go`

```go
func (b *Builder) buildYieldExpr(expr *ast.YieldExpr) cg.Expr {
    var value cg.Expr
    if expr.Value != nil {
        value = b.buildExpr(expr.Value)
    }
    return cg.NewYieldExpr(value, expr.IsDelegate)
}
```

**File**: `internal/codegen/ast.go`

```go
type YieldExpr struct {
    Value      Expr
    IsDelegate bool  // yield* vs yield
}
```

### 5.3 Generate Generator Functions and Methods

**File**: `internal/codegen/builder.go`

Detect generators by checking if the function body contains yield expressions. This applies to both standalone functions and class methods (including `[Symbol.iterator]` methods):

```go
func (b *Builder) buildFuncDecl(decl *ast.FuncDecl) cg.Decl {
    // ... existing logic ...

    // Check if function contains yield (making it a generator)
    isGenerator := b.containsYield(decl.Body)

    return cg.NewFuncDecl(
        name,
        params,
        body,
        decl.Sig.Async,
        isGenerator,
    )
}

// containsYield walks the AST to check for yield expressions
// IMPORTANT: Must stop at function boundaries - nested functions are separate
// generators and their yields do not affect the outer function.
func (b *Builder) containsYield(stmts []ast.Stmt) bool {
    visitor := &yieldDetector{found: false}
    for _, stmt := range stmts {
        ast.Walk(visitor, stmt)
        if visitor.found {
            return true
        }
    }
    return false
}

type yieldDetector struct {
    found bool
}

func (d *yieldDetector) Visit(node ast.Node) ast.Visitor {
    switch node.(type) {
    case *ast.YieldExpr:
        d.found = true
        return nil  // Stop walking
    case *ast.FuncExpr, *ast.FuncDecl:
        return nil  // Don't descend into nested functions
    }
    return d
}
```

**Method generators**: The same `containsYield` logic applies to `MethodExpr` nodes in classes. When building class methods, check if the method body contains yield to generate `*methodName()` syntax:

```go
func (b *Builder) buildMethodExpr(method *ast.MethodExpr) cg.Method {
    isGenerator := b.containsYield(method.Body)
    return cg.NewMethod(
        method.Name,
        params,
        body,
        method.Async,
        isGenerator,
    )
}
```

### 5.4 Print JavaScript Output

**File**: `internal/codegen/printer.go`

Update function printing to include `*` for generators:

```go
func (p *Printer) printFuncDecl(f *FuncDecl) {
    if f.Async {
        p.print("async ")
    }
    p.print("function")
    if f.Gen {
        p.print("*")  // Generator marker
    }
    p.print(" ")
    // ... rest of function printing ...
}
```

Print for-of loops:

```go
func (p *Printer) printForOfStmt(stmt *ForOfStmt) {
    p.print("for ")
    if stmt.IsAwait {
        p.print("await ")
    }
    // Always use `const` - Escalier for-in loop variables are immutable (like `val`)
    p.print("(const ")
    p.printPattern(stmt.Pattern)
    p.print(" of ")
    p.printExpr(stmt.Iterable)
    p.print(") ")
    p.printBlock(stmt.Body)
}
```

Print yield expressions:

```go
func (p *Printer) printYieldExpr(expr *YieldExpr) {
    p.print("yield")
    if expr.IsDelegate {
        p.print("*")
    }
    if expr.Value != nil {
        p.print(" ")
        p.printExpr(expr.Value)
    }
}
```

---

## Phase 6: Testing

### 6.1 Parser Tests

Add test cases to the existing snapshot-based test tables. The project uses `go-snaps`
(`snaps.MatchSnapshot`) for parser tests — no expected output is specified inline; instead,
snapshots are automatically generated on first run and verified on subsequent runs.

**File**: `internal/parser/stmt_test.go`

Add these entries to the `TestParseStmtNoErrors` test table:

```go
"ForInBasic": {
    input: `for item in items { console.log(item) }`,
},
"ForInDestructuring": {
    input: `for [key, value] in map { }`,
},
"ForAwaitIn": {
    input: `for await item in asyncItems { }`,
},
"ForInWithValPattern": {
    input: `for val item in items { }`,
},
"GeneratorFuncDecl": {
    input: `fn count() { yield 1; yield 2; yield 3 }`,
},
"AsyncGeneratorFuncDecl": {
    input: `async fn fetch() { yield await x }`,
},
```

**File**: `internal/parser/expr_test.go`

Add these entries to the `TestParseExprNoErrors` test table:

```go
"YieldWithValue": {
    input: "yield 1",
},
"YieldFrom": {
    input: "yield from items",
},
"BareYield": {
    input: "yield",
},
"YieldInBinaryExpr": {
    input: "x + yield 1",
},
"YieldFromInBinaryExpr": {
    input: "x + yield from items",
},
```

### 6.2 Type Checker Tests

**File**: `internal/checker/tests/iterator_test.go`

```go
func TestForInLoop(t *testing.T) {
    tests := []struct {
        input    string
        expected string
        errors   []string
    }{
        {
            input: `
                val items: Array<number> = [1, 2, 3]
                for item in items {
                    val x: number = item
                }
            `,
            expected: "void",
            errors:   nil,
        },
        {
            input: `
                val obj = {a: 1}
                for item in obj { }
            `,
            errors: []string{"Type '{a: number}' is not iterable"},
        },
    }
}

func TestGeneratorFunction(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {
            input: `
                fn count() {
                    yield 1
                    yield 2
                }
            `,
            expected: "Generator<number, void, never>",
        },
    }
}
```

### 6.3 Error Case Tests

**File**: `internal/checker/tests/iterator_errors_test.go`

```go
func TestForAwaitOutsideAsync(t *testing.T) {
    input := `
        fn notAsync() {
            for await item in asyncIterable { }
        }
    `
    errors := check(input)
    assert.Contains(t, errors, "'for await' is only allowed in async functions")
}

func TestAwaitInNonAsyncGenerator(t *testing.T) {
    // From requirements 7.3: await in non-async function is an error
    input := `
        fn notAsync() {
            yield await fetch(url)
        }
    `
    errors := check(input)
    // Should error on 'await' - existing async checking should handle this
    assert.Contains(t, errors, "'await' expression is only allowed in async functions")
}

func TestLoopVariableReassignment(t *testing.T) {
    input := `
        val items = [1, 2, 3]
        for item in items {
            item = 10  // Error: cannot reassign val binding
        }
    `
    errors := check(input)
    // Loop variables are bound as `val` (Mutable: false), so reassignment
    // produces an error. The exact error message depends on existing
    // reassignment checking implementation.
    assert.NotEmpty(t, errors)
}

func TestYieldInNestedCallback(t *testing.T) {
    // This should NOT make the outer function a generator
    // The yield only affects the callback
    input := `
        fn outer() {
            val callback = fn() {
                yield 1
            }
            return callback
        }
    `
    funcType := inferType(input)
    // outer() returns () -> Generator<number, void, never>, not a generator itself
    assert.Equal(t, "() -> () -> Generator<number, void, never>", funcType)
}
```

### 6.4 Edge Case Tests

**File**: `internal/checker/tests/iterator_edge_cases_test.go`

```go
func TestEmptyIterable(t *testing.T) {
    input := `
        val empty: Array<number> = []
        for item in empty {
            console.log(item)
        }
        val spread = [...empty]
    `
    errors := check(input)
    assert.Empty(t, errors)
}

func TestInfiniteGenerator(t *testing.T) {
    input := `
        fn naturals() {
            var n: number = 0
            while true {
                yield n
                n = n + 1
            }
        }
    `
    funcType := inferType(input)
    assert.Equal(t, "Generator<number, void, never>", funcType)
}

func TestGeneratorWithFinally(t *testing.T) {
    input := `
        fn withCleanup() {
            try {
                yield 1
                yield 2
            } finally {
                console.log("cleanup")
            }
        }
    `
    // Should compile correctly with finally block preserved
    errors := check(input)
    assert.Empty(t, errors)
}

func TestIteratorCleanupOnEarlyBreak(t *testing.T) {
    // From requirements 9.3: break triggers iterator's return() method,
    // which in turn triggers finally blocks in generators
    input := `
        fn withCleanup() {
            try {
                yield 1
                yield 2
            } finally {
                console.log("cleanup")
            }
        }

        for n in withCleanup() {
            if n == 1 {
                break  // Should trigger finally block via iterator.return()
            }
        }
    `
    errors := check(input)
    assert.Empty(t, errors)
}

func TestBreakInForIn(t *testing.T) {
    input := `
        val items = [1, 2, 3, 4, 5]
        for item in items {
            if item > 3 {
                break
            }
        }
    `
    errors := check(input)
    assert.Empty(t, errors)
}

func TestContinueInForIn(t *testing.T) {
    input := `
        val items = [1, 2, 3, 4, 5]
        for item in items {
            if item < 3 {
                continue
            }
            console.log(item)
        }
    `
    errors := check(input)
    assert.Empty(t, errors)
}
```

### 6.5 Integration Tests (Fixtures)

**Directory**: `fixtures/`

Create test fixtures following the existing convention. Each fixture is a top-level directory
under `fixtures/` containing a `lib/` directory with `.esc` source files, a `build/` directory
for generated output, and a `package.json`:

```text
fixtures/
    for_in_basic/
        lib/
            for_in_basic.esc
        build/
        package.json
    for_in_destructuring/
        lib/
            for_in_destructuring.esc
        build/
        package.json
    for_await_in/
        lib/
            for_await_in.esc
        build/
        package.json
    generator_basic/
        lib/
            generator_basic.esc
        build/
        package.json
    generator_with_finally/
        lib/
            generator_with_finally.esc
        build/
        package.json
    async_generator/
        lib/
            async_generator.esc
        build/
        package.json
    yield_delegation/
        lib/
            yield_delegation.esc
        build/
        package.json
```

---

## Implementation Order

### Milestone 0: Verification and Foundation (Derisking)
1. Test that std lib types (Iterator, Iterable, Generator, AsyncGenerator) load correctly
2. Test that Symbol.iterator property lookup works on known types
3. Implement and test `GetIterableElementType` as a standalone spike
4. Verify array spread properly checks for `Iterable<T>` (or add this check)
5. Refactor `await` throw collection to use context pointers (Phase 0.5) — validates the single-pass context-pointer pattern that generators will use
6. Verify existing `await` context checking works correctly (all async tests still pass after refactor)

**Exit criteria**: All foundation tests pass, and the await refactor demonstrates the context-pointer pattern works end-to-end. If any fail, fix them before proceeding - these are blocking issues.

### Milestone 1: Basic For-In Loops
1. Add `ForInStmt` to AST
2. Parse `for...in` syntax
3. Infer loop variable types from iterables
4. Generate `for...of` JavaScript

### Milestone 2: Generator Functions
1. Add `YieldExpr` to AST
2. Parse `yield` expressions
3. Detect generators by presence of `yield` in function body
4. Infer generator return types from yielded values
5. Generate `function*` JavaScript

### Milestone 3: Yield Delegation
1. Parse `yield from` syntax (maps to `yield*`)
2. Type check delegation targets (must be iterable)
3. Generate `yield*` JavaScript

### Milestone 4: Async Iteration
1. Add `IsAwait` flag to `ForInStmt`
2. Parse `for await...in` syntax
3. Handle `AsyncIterable<T>` type extraction
4. Detect async generators (async functions containing `yield`)
5. Generate `async function*` JavaScript
6. Generate `for await...of` JavaScript

### Milestone 5: Edge Cases & Polish
1. Break/continue in for-in loops
2. Generator cleanup (finally blocks)
3. Comprehensive error messages
4. Documentation

---

## Files to Modify Summary

| Phase | File | Changes |
|-------|------|---------|
| Phase 0 | `internal/checker/infer_expr.go` | Verify array spread checks `Iterable<T>`; refactor `AwaitExpr` to collect throw types via context pointer |
| Phase 0 | `internal/checker/infer_func.go` | Refactor `inferFuncBodyWithFuncSigType` to allocate `AwaitThrowTypes` pointer; remove `AwaitVisitor` |
| Phase 0 | `internal/checker/checker.go` | Add `AwaitThrowTypes` field to Context; propagate in scope helpers |
| Phase 0 | `internal/ast/expr.go` | Remove `Throws` field from `AwaitExpr` |
| AST | `internal/ast/stmt.go` | Add `ForInStmt` |
| AST | `internal/ast/expr.go` | Add `YieldExpr` |
| Parser | `internal/parser/stmt.go` | Parse for-in loops |
| Parser | `internal/parser/expr.go` | Parse yield and yield from expressions |
| Types | `internal/type_system/types.go` | Helper functions for Generator types, GetIteratorReturnType |
| Checker | `internal/checker/checker.go` | Add `ContainsYield`, `YieldedTypes`, `GeneratorNextType` fields to Context; update `WithNewScope`, `WithNewScopeAndNamespace`, `WithScope` to propagate them |
| Checker | `internal/checker/infer_stmt.go` | Infer for-in loops, validate async context, bind loop vars (Mutable: false) |
| Checker | `internal/checker/infer_expr.go` | Infer yield expressions, set ContainsYield, verify await checks |
| Checker | `internal/checker/infer_func.go` | Detect generators via ContainsYield in `inferFuncBodyWithFuncSigType` (covers FuncDecl, FuncExpr, and MethodExpr) |
| Codegen | `internal/codegen/ast.go` | Add `ForOfStmt`, `YieldExpr`, `Method.IsGenerator` |
| Codegen | `internal/codegen/builder.go` | Transform for-in, yield; detect generators in functions and methods |
| Codegen | `internal/codegen/printer.go` | Print for-of, yield, yield*, function*, *method() |

---

## Dependencies & Risks

### Dependencies
- Standard library types (`Iterator`, `Iterable`, `Generator`) must be properly loaded from lib.es2015.d.ts
- Symbol.iterator support in computed property keys (already exists)

### Risks
1. **Generator return type inference complexity**: Inferring `Generator<T, TReturn, TNext>` requires tracking all yield expressions and return statements
2. **Async generator interactions**: Combining async and generator semantics adds complexity
3. **Break/continue semantics**: Must properly handle loop control flow in type checking

### Mitigations
- Start with simple cases (basic for-in, generators without TReturn/TNext)
- Add explicit type annotations as fallback
- Comprehensive test coverage for edge cases

---

## Future Considerations

### Object Iteration Syntax

The requirements document (section 10.2) proposes a future `for key, value in obj` syntax for iterating over object entries. This would:
- Use the `of` keyword (not currently a token)
- Provide cleaner syntax than `for [key, value] in Object.entries(obj)`
- Clearly distinguish object iteration (`of`) from iterable iteration (`in`)

**Implementation notes for future**:
- Would require adding `Of` token to the lexer
- Parser would need to distinguish `for x in y` from `for x, y of z`
- This is out of scope for the current implementation but the design should not preclude it
