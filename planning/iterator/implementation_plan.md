# Iterator Protocol Implementation Plan

This document outlines the implementation strategy for adding iterator protocol support to Escalier, based on the requirements in [requirements.md](requirements.md).

## Current State Analysis

### Already Implemented
- **Spread syntax**: `RestSpreadExpr` fully implemented in parser, type checker, and codegen
- **Async functions**: `async fn` with `await` expressions fully supported
- **Tokens**: `Yield`, `From`, `In`, `Async`, `Await` tokens already defined in lexer
- **Standard library**: TypeScript lib.es2015.d.ts and iterable definitions loaded
- **Symbol support**: `Symbol.iterator` recognized in computed property keys

### Verification Required
- **Array spread Iterable check**: Verify that `RestSpreadExpr` in array contexts (`TupleExpr`) properly validates that the spread operand implements `Iterable<T>`. Object spread does not require this check.

### Not Yet Implemented
- `for...in` loop statement
- `yield` and `yield from` expressions
- Generator detection (functions containing `yield` are generators)
- Async generators (async functions containing `yield`)
- `for await...in` loop statement

---

## Phase 0: Verification and Foundation

Before implementing new iterator features, verify foundational pieces and derisk the implementation by testing critical assumptions.

### 0.1 Verify Standard Library Types Load Correctly

**Why this matters**: The entire implementation depends on `Iterator`, `Iterable`, `Generator`, and `AsyncGenerator` being correctly loaded from lib.es2015.d.ts. If these types aren't available or have unexpected structure, nothing else will work.

**Test cases to add**:
```go
func TestStdLibIteratorTypesLoaded(t *testing.T) {
    ctx := loadStdLib()

    // Verify Iterator type exists with correct type parameters
    iteratorType := ctx.GetTypeAlias("Iterator")
    assert.NotNil(t, iteratorType, "Iterator type should be loaded")
    assert.Equal(t, 3, len(iteratorType.TypeParams), "Iterator<T, TReturn, TNext>")

    // Verify Iterable type exists
    iterableType := ctx.GetTypeAlias("Iterable")
    assert.NotNil(t, iterableType, "Iterable type should be loaded")

    // Verify Generator type exists
    generatorType := ctx.GetTypeAlias("Generator")
    assert.NotNil(t, generatorType, "Generator type should be loaded")
    assert.Equal(t, 3, len(generatorType.TypeParams), "Generator<T, TReturn, TNext>")

    // Verify AsyncGenerator type exists
    asyncGeneratorType := ctx.GetTypeAlias("AsyncGenerator")
    assert.NotNil(t, asyncGeneratorType, "AsyncGenerator type should be loaded")
}
```

### 0.2 Verify Symbol.iterator Property Lookup

**Why this matters**: `GetIterableElementType` needs to look up `[Symbol.iterator]` on types. This is a computed property key, not a regular named property.

**Test cases to add**:
```go
func TestSymbolIteratorLookup(t *testing.T) {
    input := `
        declare val arr: Array<number>
        val iter = arr[Symbol.iterator]()
    `
    iterType, errors := inferType(input, "iter")
    assert.Empty(t, errors)
    // iter should have type Iterator<number, ...> or IterableIterator<number, ...>
    assert.Contains(t, iterType.String(), "Iterator")
}
```

### 0.3 Implement GetIterableElementType (Spike)

**Why this matters**: This is the foundational function used by for-in loops, array spread validation, and yield delegation. Implement and test it before building features that depend on it.

**Test against known types**:
```go
func TestGetIterableElementType(t *testing.T) {
    tests := []struct {
        typeName string
        expected string
    }{
        {"Array<number>", "number"},
        {"Set<string>", "string"},
        {"Map<string, number>", "[string, number]"},
        {"string", "string"},
        {"Generator<boolean, void, never>", "boolean"},
    }

    for _, tt := range tests {
        t.Run(tt.typeName, func(t *testing.T) {
            typ := parseType(tt.typeName)
            elementType := GetIterableElementType(typ)
            assert.NotNil(t, elementType, "should be iterable")
            assert.Equal(t, tt.expected, elementType.String())
        })
    }

    // Non-iterable types should return nil
    nonIterables := []string{"{a: number}", "number", "boolean"}
    for _, typeName := range nonIterables {
        t.Run(typeName+" (non-iterable)", func(t *testing.T) {
            typ := parseType(typeName)
            elementType := GetIterableElementType(typ)
            assert.Nil(t, elementType, "should not be iterable")
        })
    }
}
```

### 0.4 Verify Array Spread Iterable Check

**Files to check**: `internal/checker/infer_expr.go`

When `RestSpreadExpr` appears inside a `TupleExpr` (array literal), verify the type checker:
1. Checks that the spread operand implements `Iterable<T>`
2. Extracts the element type `T` from the iterable
3. Reports an error if the operand is not iterable

```go
// Expected behavior in inferTupleExpr when encountering RestSpreadExpr:
case *ast.RestSpreadExpr:
    spreadType, errs := c.inferExpr(ctx, elem.Value)
    errors = slices.Concat(errors, errs)

    // Check that spread operand is iterable
    elementType := c.getIterableElementType(spreadType)
    if elementType == nil {
        errors = append(errors, Error{
            Message: fmt.Sprintf("Type '%s' is not iterable", spreadType),
            Span:    elem.Span(),
        })
        elementType = AnyType{}
    }
    elemTypes = append(elemTypes, elementType)
```

**Test cases to add**:
```go
func TestArraySpreadRequiresIterable(t *testing.T) {
    // Valid: spreading an array
    input1 := `val arr = [1, 2, ...([3, 4])]`
    errors1 := check(input1)
    assert.Empty(t, errors1)

    // Valid: spreading a Set
    input2 := `val arr = [...new Set([1, 2, 3])]`
    errors2 := check(input2)
    assert.Empty(t, errors2)

    // Invalid: spreading a non-iterable object
    input3 := `val arr = [...{a: 1}]`
    errors3 := check(input3)
    assert.NotEmpty(t, errors3)
}
```

**Note**: Object spread (`{...obj}`) does NOT require `Iterable<T>` - it copies enumerable own properties. This is already correctly handled.

---

## Phase 1: AST Extensions

### 1.1 Add ForInStmt to Statement AST

**File**: `internal/ast/stmt.go`

Add a new statement type for for...in loops:

```go
type ForInStmt struct {
    Pattern  Pattern   // Loop variable pattern (supports destructuring)
    Iterable Expr      // Expression being iterated
    Body     []Stmt    // Loop body statements
    IsAwait  bool      // true for `for await...in`
    span     Span
}

func NewForInStmt(pattern Pattern, iterable Expr, body []Stmt, isAwait bool, span Span) *ForInStmt {
    return &ForInStmt{
        Pattern:  pattern,
        Iterable: iterable,
        Body:     body,
        IsAwait:  isAwait,
        span:     span,
    }
}

func (s *ForInStmt) stmtNode()    {}
func (s *ForInStmt) Span() Span   { return s.span }
```

### 1.2 Add YieldExpr to Expression AST

**File**: `internal/ast/expr.go`

Add yield expression:

```go
type YieldExpr struct {
    Value      Expr   // The yielded value (nil for bare `yield`)
    IsDelegate bool   // true for `yield from` (compiles to yield*)
    span       Span
}

func NewYieldExpr(value Expr, isDelegate bool, span Span) *YieldExpr {
    return &YieldExpr{
        Value:      value,
        IsDelegate: isDelegate,
        span:       span,
    }
}

func (e *YieldExpr) exprNode()   {}
func (e *YieldExpr) Span() Span  { return e.span }
```

**Note**: No changes needed to `FuncSig` - generator functions are detected by the presence of `yield` expressions in the function body during type checking and code generation.

---

## Phase 2: Parser Extensions

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

Add yield expression parsing. Since `yield` has lower precedence than most operators, handle it at the statement-expression boundary:

```go
func (p *Parser) parseYieldExpr() *ast.YieldExpr {
    startSpan := p.lexer.current().Span
    p.lexer.consume() // consume 'yield'

    // Check for 'from' keyword (yield from for delegation)
    isDelegate := false
    if p.lexer.current().Type == From {
        isDelegate = true
        p.lexer.consume()
    }

    // Parse the yielded expression (if any)
    var value ast.Expr
    if !p.isStatementTerminator() {
        value = p.expr()
    }

    endSpan := startSpan
    if value != nil {
        endSpan = value.Span()
    }

    return ast.NewYieldExpr(value, isDelegate, ast.MergeSpans(startSpan, endSpan))
}
```

**Note**: No special parsing needed for generator functions - any function containing `yield` is automatically a generator. The type checker and code generator detect this by scanning the function body for yield expressions.

---

## Phase 3: Type System Extensions

### 3.1 Generator Type Handling

**File**: `internal/type_system/types.go`

The existing type system can handle `Generator<T, TReturn, TNext>` as a generic type reference. No new type constructors are needed, but helper functions may be useful:

```go
// Helper to construct Generator<T, TReturn, TNext> type
func MakeGeneratorType(yieldType, returnType, nextType Type) *TypeRefType {
    return NewTypeRef("Generator", []Type{yieldType, returnType, nextType})
}

// Helper to construct AsyncGenerator<T, TReturn, TNext> type
func MakeAsyncGeneratorType(yieldType, returnType, nextType Type) *TypeRefType {
    return NewTypeRef("AsyncGenerator", []Type{yieldType, returnType, nextType})
}
```

### 3.2 Iterable Type Extraction

Add utilities to extract the element type from an `Iterable<T>`:

```go
// GetIterableElementType extracts T from Iterable<T> or returns nil
func GetIterableElementType(t Type) Type {
    // Check if type has [Symbol.iterator]() method
    // Return the T from Iterator<T, TReturn, TNext> returned by that method
}

// GetAsyncIterableElementType extracts T from AsyncIterable<T>
func GetAsyncIterableElementType(t Type) Type {
    // Similar but for [Symbol.asyncIterator]()
}

// GetIteratorReturnType extracts TReturn from Iterator<T, TReturn, TNext>
// Used for determining the type of yield* expressions
func GetIteratorReturnType(t Type) Type {
    // Check if type has [Symbol.iterator]() method
    // Return the TReturn from Iterator<T, TReturn, TNext> returned by that method
}
```

---

## Phase 4: Type Checker Extensions

### 4.1 Infer For-In Loop Types

**File**: `internal/checker/infer_stmt.go`

Add type inference for for-in loops:

```go
func (c *Checker) inferForInStmt(ctx *Context, stmt *ast.ForInStmt) []Error {
    errors := []Error{}

    // Validate async context for 'for await'
    if stmt.IsAwait && !ctx.InAsync {
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
        elementType = c.getAsyncIterableElementType(iterableType)
        if elementType == nil {
            errors = append(errors, Error{
                Message: fmt.Sprintf("Type '%s' is not async iterable", iterableType),
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
    loopCtx := ctx.NewScope()

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

**File**: `internal/checker/infer_expr.go`

Add yield expression inference:

```go
func (c *Checker) inferYieldExpr(ctx *Context, expr *ast.YieldExpr) (Type, []Error) {
    errors := []Error{}

    // Mark this function context as containing yield (makes it a generator)
    ctx.ContainsYield = true

    if expr.IsDelegate {
        // yield from: the value must be iterable
        if expr.Value != nil {
            valueType, errs := c.inferExpr(ctx, expr.Value)
            errors = slices.Concat(errors, errs)

            elementType := c.getIterableElementType(valueType)
            if elementType == nil {
                errors = append(errors, Error{
                    Message: fmt.Sprintf("Type '%s' is not iterable", valueType),
                    Span:    expr.Value.Span(),
                })
            }
            // Track delegated element types - contributes to T in Generator<T, TReturn, TNext>
            ctx.AddYieldedType(elementType)

            // The yield* expression evaluates to TReturn of the delegated generator.
            // For simplicity, we can start with `unknown` and refine later if needed.
            // Most code doesn't use the return value of yield*.
            delegatedReturnType := c.getIteratorReturnType(valueType)
            if delegatedReturnType == nil {
                delegatedReturnType = UnknownType{}
            }
            return delegatedReturnType, errors
        }
    } else {
        // Regular yield
        if expr.Value != nil {
            valueType, errs := c.inferExpr(ctx, expr.Value)
            errors = slices.Concat(errors, errs)

            // Track yielded types - contributes to T in Generator<T, TReturn, TNext>
            ctx.AddYieldedType(valueType)

            // The yield expression evaluates to TNext (value passed to .next())
            // TNext defaults to `never` since most generators are consumed via
            // for...of loops rather than manual .next(value) calls. If code needs
            // to pass values, it can explicitly annotate the generator type.
            if ctx.GeneratorNextType == nil {
                return NeverType{}, errors
            }
            return ctx.GeneratorNextType, errors
        }
    }

    return VoidType{}, errors
}
```

### 4.3 Infer Generator Functions

**File**: `internal/checker/infer_func.go`

Modify function inference to detect generators by presence of `yield`:

```go
func (c *Checker) inferFuncDecl(ctx *Context, decl *ast.FuncDecl) (Type, []Error) {
    // ... existing setup ...

    funcCtx := ctx.NewScope()
    funcCtx.InAsync = decl.Sig.Async
    funcCtx.ContainsYield = false  // Will be set to true if yield is encountered
    funcCtx.YieldedTypes = []Type{}  // Track yield types

    // ... infer body (this may set funcCtx.ContainsYield = true) ...

    // Check if this function is a generator (contains yield)
    if funcCtx.ContainsYield {
        yieldType := c.unionTypes(funcCtx.YieldedTypes)
        returnType := // from explicit returns or undefined
        nextType := NeverType{}  // defaults to never; explicit annotation required for .next(value) usage

        if decl.Sig.Async {
            return MakeAsyncGeneratorType(yieldType, returnType, nextType), errors
        }
        return MakeGeneratorType(yieldType, returnType, nextType), errors
    }

    // ... rest of existing logic for regular functions ...
}
```

### 4.4 Add Context Fields

**File**: `internal/checker/context.go`

Extend the checker context:

```go
type Context struct {
    // ... existing fields ...

    InAsync           bool
    ContainsYield     bool      // Set to true when yield is encountered
    YieldedTypes      []Type    // Types of all yield expressions
    GeneratorNextType Type      // TNext type for this generator
}
```

**Note**: Loop variable immutability is handled by binding them as `val` (not `var`). The existing type system already prevents reassignment of `val` bindings, so no additional tracking is needed.

### 4.5 Yield Context Scoping

**Important**: Each function creates a new context for `ContainsYield` tracking. When entering a nested function (lambda, callback, etc.), a fresh context is created. This ensures:

1. `yield` in a nested function only makes *that* function a generator
2. The outer function's generator status is unaffected by nested yields
3. Using `yield` inside a `.forEach()` callback creates a generator callback, not a generator outer function

```go
func (c *Checker) inferFuncExpr(ctx *Context, expr *ast.FuncExpr) (Type, []Error) {
    // Create fresh context for this function - does NOT inherit ContainsYield
    funcCtx := ctx.NewScope()
    funcCtx.InAsync = expr.Async
    funcCtx.ContainsYield = false  // Fresh start for this function
    funcCtx.YieldedTypes = []Type{}

    // ... infer body ...

    // funcCtx.ContainsYield reflects only yields in THIS function
}
```

### 4.6 Async Context Validation

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

**Verification**: Ensure existing `await` checking in `inferAwaitExpr` properly checks `ctx.InAsync` and reports errors. The iterator implementation should not break this existing behavior.

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

**File**: `internal/parser/stmt_test.go`

```go
func TestParseForInStmt(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {
            input: `for item in items { console.log(item) }`,
            expected: // AST representation
        },
        {
            input: `for [key, value] in map { }`,
            expected: // With destructuring
        },
        {
            input: `for await item in asyncItems { }`,
            expected: // Async iteration
        },
    }
    // ... test implementation ...
}
```

**File**: `internal/parser/expr_test.go`

```go
func TestParseYieldExpr(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"yield 1", /* expected */},
        {"yield from items", /* expected */},
        {"yield", /* bare yield */},
    }
}

func TestParseGeneratorFunc(t *testing.T) {
    tests := []struct {
        input    string
        expected string
    }{
        {"fn count() { yield 1 }", /* expected */},
        {"async fn fetch() { yield await x }", /* expected */},
    }
}
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
            var n = 0
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

**Directory**: `fixtures/iterator/`

Create test fixtures with input Escalier code and expected JavaScript output:

```
fixtures/iterator/
    for_in_basic/
        input.esc
        output.js
    for_in_destructuring/
        input.esc
        output.js
    for_in_break_continue/
        input.esc
        output.js
    for_await_in/
        input.esc
        output.js
    generator_basic/
        input.esc
        output.js
    generator_with_finally/
        input.esc
        output.js
    async_generator/
        input.esc
        output.js
    yield_delegation/
        input.esc
        output.js
    empty_iterable/
        input.esc
        output.js
```

---

## Implementation Order

### Milestone 0: Verification and Foundation (Derisking)
1. Test that std lib types (Iterator, Iterable, Generator, AsyncGenerator) load correctly
2. Test that Symbol.iterator property lookup works on known types
3. Implement and test `GetIterableElementType` as a standalone spike
4. Verify array spread properly checks for `Iterable<T>` (or add this check)
5. Verify existing `await` context checking works correctly

**Exit criteria**: All foundation tests pass. If any fail, fix them before proceeding - these are blocking issues.

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
| Phase 0 | `internal/checker/infer_expr.go` | Verify array spread checks `Iterable<T>` |
| AST | `internal/ast/stmt.go` | Add `ForInStmt` |
| AST | `internal/ast/expr.go` | Add `YieldExpr` |
| Parser | `internal/parser/stmt.go` | Parse for-in loops |
| Parser | `internal/parser/expr.go` | Parse yield and yield from expressions |
| Types | `internal/type_system/types.go` | Helper functions for Generator types, GetIteratorReturnType |
| Checker | `internal/checker/context.go` | Add `ContainsYield`, `YieldedTypes` fields |
| Checker | `internal/checker/infer_stmt.go` | Infer for-in loops, validate async context, bind loop vars (Mutable: false) |
| Checker | `internal/checker/infer_expr.go` | Infer yield expressions, set ContainsYield, verify await checks |
| Checker | `internal/checker/infer_func.go` | Detect generators via ContainsYield |
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
