# Well-Known Symbols as Keys - Implementation Plan

## Overview

This plan outlines the implementation steps for supporting well-known symbols as computed property keys in the Escalier compiler, enabling parsing and type-checking of TypeScript's ES2015+ standard library files.

---

## Implementation Status Summary

| Phase | Description | Status | Progress | Blocking Issues |
|-------|-------------|--------|----------|-----------------|
| **1** | Recursive Lib File Loading | ✅ Done | 100% | None |
| **2** | Parse `unique symbol` Type | ✅ Done | 100% | None |
| **3** | Convert Computed Property Keys | ✅ Done | 100% | None |
| **4** | Dependency Graph for Computed Keys | ✅ Done | 100% | None |
| **5** | Infer Symbol-Keyed Properties | 🚧 In Progress | 60% | Task 5.4 needs verification |
| **6** | Symbol Key Property Access | 🚧 In Progress | 30% | Needs verification |
| **7** | Testing | 🚧 In Progress | 15% | Interop tests added |

**Legend:** ✅ Done | 🚧 In Progress | ⬜ Not Started | 🔒 Blocked

**Task Breakdown:**
- **Phase 1:** 1.1-1.5 ✅ Complete
- **Phase 3:** 3.0 ✅ Expr types | 3.1 ✅ parseExpr | 3.2 ✅ convertExpr | 3.3 ✅ | 3.4 ✅ Validation
- **Phase 4:** 4.0 ✅ Merge lib files (Risk 2 mitigation) | 4.1-4.3 ✅
- **Phase 5:** 5.1-5.3 ✅ | 5.4 ⬜ Interface verification
- **Phase 7:** 7.1-7.5 ⬜ | 7.3 ✅ Interop tests | 7.6 ⬜ Lib discovery tests

---

## Current State

### What's Working
- `lib.es5.d.ts` parsing and type inference ✅
- `lib.es2015.*.d.ts` parsing and type inference ✅
- `lib.dom.d.ts` parsing and type inference ✅
- Declaration merging for interfaces via `MergeInterface()` ✅
- Computed property support in Escalier source code (e.g., `[Symbol.customMatcher]`) ✅
- `ObjectType.SymbolKeyMap` field exists and is populated for classes ✅
- `unique symbol` parsing in dts_parser ✅
- `UniqueSymbolType` in type system ✅
- Recursive lib file loading with ES2015 target ✅
- Symbol binding from ES2015 lib files with well-known symbols ✅
- `Symbol.customMatcher` added to `SymbolConstructor` for enum pattern matching ✅
- `CustomMatcherSymbolID` cached and restored across test runs ✅
- Unified DepGraph for all lib files (Risk 2 mitigation) ✅
- Type parameter constraint dependency tracking in `FuncTypeAnn.Accept()` ✅
- `KeyOfType` expansion cycle prevention via `insideKeyOfTarget` ✅

### Note: `lib.dom.d.ts` Handling
`lib.dom.d.ts` is loaded separately from the ES version hierarchy. It is loaded unconditionally alongside the ES lib files in `loadGlobalDefinitions()`. The DOM lib does NOT use computed symbol keys, so it is unaffected by this work. The recursive loading refactor (Task 1.5) should preserve this behavior by loading DOM separately.

### What's Missing
- ~~Parsing `unique symbol` type in dts_parser~~ ✅ Done
- ~~Converting `ComputedKey` in interop layer~~ ✅ Done
- ~~Recursive lib file loading with reference following~~ ✅ Done
- ~~Enable ES2015 target in prelude.go (Task 1.3)~~ ✅ Done
- ~~Unified DepGraph for all lib files (Task 4.0)~~ ✅ Done
- Verification of symbol-keyed properties in interfaces (Task 5.4 - may already work)
- Verification of property access via symbol keys (Phase 6)

### Key Files

| File | Purpose | Status |
|------|---------|--------|
| `internal/dts_parser/ast.go` | `ComputedKey`, `Expr` interface, `IdentExpr`, `MemberExpr`, `LitExpr` | ✅ |
| `internal/dts_parser/object.go` | Parses object type members, `parseExpr()`, `parseIdentOrMemberExpr()` | ✅ |
| `internal/dts_parser/class.go` | Class computed key parsing uses `parseExpr()` | ✅ |
| `internal/dts_parser/lexer.go` | `unique` keyword token | ✅ |
| `internal/dts_parser/base.go` | Parses `unique symbol` | ✅ |
| `internal/interop/helper.go` | `convertExpr()`, `convertPropertyKey()` with ComputedKey support | ✅ |
| `internal/interop/helper_test.go` | ComputedKey validation tests | ✅ |
| `internal/checker/prelude.go` | Lib file loading via `discoverESLibFiles()`, ES2015 enabled | ✅ |
| `internal/checker/infer_module.go` | Declaration processing and merging | 🚧 |
| `internal/type_system/types.go` | `ObjectType` with `SymbolKeyMap` | ✅ |
| `internal/ast/type_ann.go` | `UniqueSymbolTypeAnn` | ✅ |

> **Note:** Line numbers throughout this document are approximate and may shift as code evolves. Use function/type names (e.g., `convertPropertyKey`, `discoverESLibFiles`) when searching.

---

## Phase 1: Recursive Lib File Loading (FR1)

**Status:** ✅ DONE (100% complete)
**Difficulty:** ~~Medium~~ → Done
**Risk:** N/A (completed)

**Goal:** Load lib files by following `/// <reference lib="..." />` directives recursively.

> **Note:** Line numbers in this section are approximate and may shift as code changes. Use function names for navigation.

### Task 1.1: Parse Reference Directives ✅ DONE

**Location:** `internal/checker/prelude.go` - `referenceDirectivePattern` variable and `parseReferenceDirectives()` function

**What exists:**
- Regex pattern: `var referenceDirectivePattern = regexp.MustCompile(...)`
- Bundle file pattern for ES2015+ detection
- Reference extraction works correctly

### Task 1.2: Implement Lib File Discovery ✅ DONE (iterative approach)

**Location:** `internal/checker/prelude.go` - `discoverESLibFiles()` function

**What exists:**
- `discoverESLibFiles()` function loads ES lib files in dependency order
- Uses directory scanning to find bundle files, then parses their reference directives
- Handles ES5 as special case (loaded first)
- Supports version filtering (es5, es2015, es2016, etc.)

**Note:** This is an *iterative* implementation that scans the directory. Task 1.5 proposes a simpler *recursive* approach that follows references directly without directory scanning.

### Task 1.3: Update Prelude Loading ✅ DONE

**Location:** `internal/checker/prelude.go` - `loadGlobalDefinitions()` function

**What exists:**
- `loadGlobalDefinitions()` calls `discoverESLibFiles()` with `targetVersion := "es2015"`
- Successfully loads all ES2015 lib files including `lib.es2015.symbol.d.ts`, `lib.es2015.iterable.d.ts`, etc.
- `filterLibFileErrors()` filters out `InvalidObjectKeyError` during lib file loading (expected for computed keys like `[Symbol.iterator]` when symbol isn't available yet)
- `addCustomMatcherToSymbol()` adds `Symbol.customMatcher` to `SymbolConstructor` after ES2015 lib files are loaded
- `CustomMatcherSymbolID` is cached and restored in `Prelude()` function for test isolation

**Key changes made:**
1. Changed `targetVersion := "es5"` to `targetVersion := "es2015"`
2. Added `filterLibFileErrors()` to handle `InvalidObjectKeyError` during lib loading
3. Added `CustomMatcherSymbolID` field to `Checker` struct
4. Added `cachedCustomMatcherSymbolID` for caching across test runs
5. Updated `unify.go` to use `c.CustomMatcherSymbolID` instead of hardcoded symbol ID

### Task 1.4: Add Target Version Configuration ⬜ NOT STARTED

**Location:** `internal/checker/checker.go` or configuration
**Difficulty:** Easy
**Risk:** Low

Add a way to specify the target ES version (currently hard-coded to `es5`).

**Note:** Low priority - can be done after core functionality works.

### Task 1.5: Refactor `discoverESLibFiles` to Use Recursive Loading ✅ DONE

**Location:** `internal/checker/prelude.go` - `discoverESLibFiles()` and related helper functions
**Difficulty:** Medium
**Risk:** Low

**Implementation completed:**
- Replaced iterative directory-scanning approach with recursive reference-following
- Added `loadLibFilesRecursive()` function that processes dependencies depth-first
- Added `isESLibReference()` to filter out non-ES references (decorators, dom, scripthost, etc.)
- Removed unused functions: `bundleFilePattern`, `isBundleFile`, `extractESVersion`, `compareESVersions`
- Updated tests to reflect new behavior

**Benefits achieved:**
- No directory scanning required (WASM-friendly)
- No bundle file detection logic needed
- No version comparison/filtering
- Naturally handles the dependency tree
- Simpler, more maintainable code (~40 lines vs ~70 lines)

---

## Phase 2: Parse `unique symbol` Type (FR5)

**Status:** ✅ DONE (100% complete)
**Difficulty:** N/A (completed)
**Risk:** N/A

**Goal:** Parse and represent `unique symbol` as a distinct type.

> **Note:** Line numbers below are from when these tasks were implemented and serve as historical reference. Use type/function names when searching.

### Task 2.1: Add Lexer Support ✅ DONE

**Location:** `internal/dts_parser/lexer.go:74`

**What exists:** `"unique": Unique` keyword token defined

### Task 2.2: Add AST Node ✅ DONE

**Location:** `internal/dts_parser/ast.go:531`

**What exists:** `PrimUniqueSymbol` primitive type constant defined

### Task 2.3: Parse `unique symbol` ✅ DONE

**Location:** `internal/dts_parser/base.go:188-194`

**What exists:**
```go
if p.match(Unique) {
    if !p.check(Symbol) {
        p.error("expected 'symbol' after 'unique'")
    }
    p.advance()
    return &PrimTypeAnn{Prim: PrimUniqueSymbol, span: ...}
}
```

### Task 2.4: Add Type System Representation ✅ DONE

**Location:** `internal/type_system/types.go:509-539`

**What exists:**
```go
type UniqueSymbolType struct {
    Value      int  // Unique identifier
    provenance Provenance
}
```
- Constructor: `NewUniqueSymbolType(provenance, value)`
- Symbol counter managed via `c.SymbolID` in checker

### Task 2.5: Add Interop Conversion ✅ DONE

**Location:** `internal/interop/helper.go:265-266`

**What exists:**
```go
case dts_parser.PrimUniqueSymbol:
    return ast.NewUniqueSymbolTypeAnn(span), nil
```

### Task 2.6: Add AST Type Annotation ✅ DONE

**Location:** `internal/ast/type_ann.go:111-122`

**What exists:**
```go
type UniqueSymbolTypeAnn struct {
    span         Span
    inferredType Type
}
```

### Task 2.7: Infer Unique Symbol Types ✅ DONE

**Location:** `internal/checker/prelude.go`

**What exists:**
- ES2015 lib files define `Symbol` and `SymbolConstructor` with standard well-known symbols (`iterator`, `toStringTag`, etc.)
- `addCustomMatcherToSymbol()` adds `customMatcher` property to `SymbolConstructor` (Escalier-specific, used for enum pattern matching)
- `CustomMatcherSymbolID` stored in `Checker` struct for use in `unify.go`
- Symbol IDs are cached via `cachedCustomMatcherSymbolID` for test isolation

---

## Phase 3: Convert Computed Property Keys (FR4)

**Status:** ✅ DONE (100% complete)
**Difficulty:** Medium
**Risk:** Medium (must handle all valid computed key patterns)

**Goal:** Convert `ComputedKey` from dts_parser AST to Escalier AST.

**This phase was the critical blocker preventing ES2015+ support - now resolved.**

### Task 3.0: Add Expr Interface to dts_parser ✅ DONE

**Location:** `internal/dts_parser/ast.go:46-81`
**Difficulty:** Medium
**Risk:** Low

**Implementation completed:**

Added proper `Expr` interface and expression types to `dts_parser/ast.go`:

```go
// Expr represents an expression in computed keys
type Expr interface {
    isExpr()
    Node
}

func (*LitExpr) isExpr()    {}
func (*IdentExpr) isExpr()  {}
func (*MemberExpr) isExpr() {}

// LitExpr wraps a Literal as an expression
type LitExpr struct {
    Lit  Literal
    span ast.Span
}

type IdentExpr struct {
    Name string
    span ast.Span
}

type MemberExpr struct {
    Object Expr
    Prop   *Ident
    span   ast.Span
}
```

**Updated `ComputedKey` struct** (`internal/dts_parser/ast.go:457-460`):
```go
type ComputedKey struct {
    Expr Expr // expression inside [...] (was TypeAnn)
    span ast.Span
}
```

### Task 3.1: Add `parseExpr()` Function ✅ DONE

**Location:** `internal/dts_parser/object.go:601-648`
**Difficulty:** Medium
**Risk:** Low

**Implementation completed:**

Added expression parsing functions for computed keys:

```go
// parseExpr parses an expression for computed keys.
// Supports: identifiers, member expressions (a.b.c), string literals, number literals
func (p *DtsParser) parseExpr() Expr {
    switch token.Type {
    case StrLit:
        return &LitExpr{Lit: &StringLiteral{...}, span: tok.Span}
    case NumLit:
        return &LitExpr{Lit: &NumberLiteral{...}, span: tok.Span}
    case Identifier:
        return p.parseIdentOrMemberExpr()
    default:
        return nil
    }
}

// parseIdentOrMemberExpr parses identifier or member expression (a.b.c)
func (p *DtsParser) parseIdentOrMemberExpr() Expr {
    // Parses chains like Symbol.iterator into MemberExpr
}
```

**Also updated:**
- `internal/dts_parser/object.go:582` - `parseComputedPropertyKey()` uses `parseExpr()`
- `internal/dts_parser/class.go:159` - Class computed key parsing uses `parseExpr()`

### Task 3.2: Implement `convertExpr()` Helper ✅ DONE

**Location:** `internal/interop/helper.go:84-107`
**Difficulty:** Easy
**Risk:** Low

**Implementation completed:**

Single `convertExpr()` function replaces previous `convertQualIdentToExpr` and `convertTypeAnnToExpr`:

```go
// convertExpr converts a dts_parser.Expr to an ast.Expr
func convertExpr(expr dts_parser.Expr) (ast.Expr, error) {
    switch e := expr.(type) {
    case *dts_parser.IdentExpr:
        return ast.NewIdent(e.Name, e.Span()), nil
    case *dts_parser.MemberExpr:
        obj, err := convertExpr(e.Object)
        if err != nil {
            return nil, err
        }
        prop := ast.NewIdentifier(e.Prop.Name, e.Prop.Span())
        return ast.NewMember(obj, prop, false, e.Span()), nil
    case *dts_parser.LitExpr:
        switch lit := e.Lit.(type) {
        case *dts_parser.StringLiteral:
            return ast.NewLitExpr(ast.NewString(lit.Value, lit.Span())), nil
        case *dts_parser.NumberLiteral:
            return ast.NewLitExpr(ast.NewNumber(lit.Value, lit.Span())), nil
        }
    }
    return nil, fmt.Errorf("convertExpr: unsupported expression type %T", expr)
}
```

**Patterns handled:**
1. ✅ `IdentExpr` → Simple identifier like `[foo]`
2. ✅ `MemberExpr` → Member access like `[Symbol.iterator]`
3. ✅ `LitExpr` with `StringLiteral` → String literal like `["key"]`
4. ✅ `LitExpr` with `NumberLiteral` → Number literal like `[42]`

### Task 3.3: Verify AST ComputedKey Exists ✅ DONE

**Location:** `internal/ast/obj_elem.go:13`

**What exists:**
```go
type ComputedKey struct {
    Expr Expr  // NOTE: In ast, this must be an Expr
    // ... additional fields
}
```

The AST `ComputedKey` already exists and can be used as an `ObjKey`.

### Task 3.4: Validate ComputedKey Conversion ✅ DONE

**Location:** `internal/interop/helper_test.go:745-925`
**Difficulty:** Easy
**Risk:** Low

**Purpose:** Validate that ComputedKey conversion works correctly before enabling ES2015+ globally.

**Tests implemented:**

1. **`TestConvertComputedKey`** - Validates member access computed keys:
   - Tests `[Symbol.iterator]` method conversion
   - Tests `[Symbol.toStringTag]` property conversion
   - Tests `[Symbol.hasInstance]` method conversion
   - Verifies `ComputedKey` contains `MemberExpr` with correct object/property names

2. **`TestConvertComputedKeySimpleIdent`** - Validates simple identifier computed keys:
   - Tests `[key]` property conversion
   - Verifies `ComputedKey` contains `IdentExpr` with correct name

3. **`TestConvertES2015LibFiles`** - Tests all ES2015 lib files convert without errors:
   - `lib.es2015.symbol.d.ts` ✅
   - `lib.es2015.iterable.d.ts` ✅
   - All 9 ES2015 lib files ✅

**Verification:**
```bash
go test ./internal/interop/... -run TestConvertComputedKey -v
go test ./internal/interop/... -run TestConvertES2015LibFiles -v
```

**Success criteria:**
- [x] `lib.es2015.symbol.d.ts` converts without errors ✅
- [x] `lib.es2015.iterable.d.ts` converts without errors ✅
- [x] ComputedKey expressions are correctly converted to `ast.ComputedKey` ✅

---

## Phase 4: Dependency Graph for Computed Keys (FR3)

**Status:** ✅ DONE (100% complete)
**Difficulty:** Hard
**Risk:** High (affects processing order of all declarations)

**Goal:** Ensure declarations are processed in correct order when computed keys create dependencies.

### Task 4.0: Merge Lib Files into Unified Dependency Graph ✅ DONE

**Location:** `internal/checker/prelude.go` - `loadGlobalDefinitions()`
**Difficulty:** Medium
**Risk:** Medium

**Implementation completed:**

All TypeScript stdlib `.d.ts` files are now merged into a single module with a unified dependency graph. The implementation:

1. **Added `mergeModules()` helper function** to merge all AST modules:
   - Merges `Namespaces` btree map
   - Appends `Files` slices
   - Merges `Sources` maps

2. **Updated `loadGlobalDefinitions()`** to:
   - Create a combined `globalModule` and `namedModules` map
   - Load each lib file and merge its modules into the combined structures
   - Call `InferModule()` once with the unified module

3. **Bug fixes discovered during implementation:**

   **Bug 1: Infinite loop in type expansion (`expand_type.go`)**
   - When expanding `KeyOfType`, nested `KeyOfType` types inside `MappedElems` were also being expanded recursively
   - Fixed by adding `insideKeyOfTarget` field to `TypeExpansionVisitor`
   - Added `expandTypeWithConfig()` helper to propagate the flag
   - Modified `KeyOfType` handler to skip expansion when `insideKeyOfTarget > 0`

   **Bug 2: Missing dependency tracking for type parameter constraints (`type_ann.go`)**
   - `FuncTypeAnn.Accept()` was not visiting `TypeParams`, so constraints like `T extends ArrayBufferView` were never traversed
   - The dependency graph never recorded that declarations depend on types referenced in type parameter constraints
   - Fixed by updating `FuncTypeAnn.Accept()` to visit type parameters and their constraints

**Result:**
- All tests pass with unified DepGraph
- `SymbolConstructor` declarations from multiple files are properly merged
- Interface declarations that depend on well-known symbols are processed in correct order

### Task 4.1: Detect Computed Key Dependencies ✅ DONE

**Location:** `internal/dep_graph/dep_graph.go`
**Difficulty:** Medium
**Risk:** Medium

**Implementation completed:**

The dependency graph now properly tracks computed key dependencies:

1. **Computed keys in interfaces/classes** - The `DependencyVisitor` already traverses `ComputedKey` expressions via the AST visitor pattern. When an interface has `[Symbol.iterator]`, the visitor traverses the `Member` expression, finding the `Ident("Symbol")` reference.

2. **Type parameter constraints** - Fixed in Task 4.0. `FuncTypeAnn.Accept()` now visits type parameters and their constraints, ensuring dependencies like `T extends ArrayBufferView` are tracked.

3. **Unified DepGraph** - All lib files are merged into a single module before building the DepGraph, ensuring all declarations and their dependencies are processed together.

**Result:** All declarations are processed in correct dependency order, with `Symbol` being available before interfaces that use well-known symbols like `[Symbol.iterator]`.

### Task 4.2: Group Same-Named Interface Declarations ✅ DONE

**Location:** `internal/checker/infer_module.go` - SCC processing logic

**What exists:**
- Comments indicate interface merging is already handled
- Comment: "even when multiple declarations share the same binding key (overloads, interface merging)"
- Same-named interfaces are processed together

### Task 4.3: Process Interface Groups Together ✅ DONE

**Location:** `internal/checker/infer_module.go`

**What exists:**
- Interface merging via `MergeInterface()` already works
- SCC processing handles same-named declarations

---

## Phase 5: Infer Symbol-Keyed Properties (FR6)

**Status:** 🚧 IN PROGRESS (60% complete - Tasks 5.1-5.3 done, Task 5.4 needs verification)
**Difficulty:** Medium
**Risk:** Medium

**Goal:** Properly infer and track symbol-keyed properties in the type system.

### Task 5.1: Evaluate Computed Key Expressions ✅ DONE (for classes)

**Location:** `internal/checker/utils.go` - `astKeyToTypeKey()` function

**What exists:**
```go
func astKeyToTypeKey(c *Checker, ctx Context, key ast.ObjKey) (*type_system.ObjTypeKey, error) {
    // ...
    case *ast.ComputedKey:
        keyType, _ := c.inferExpr(ctx, k.Expr)
        switch t := keyType.(type) {
        case *type_system.UniqueSymbolType:
            newKey := type_system.NewSymKey(t.Value)
            return &newKey, nil
        // ... handles string/number literals too
        }
}
```

### Task 5.2: Populate SymbolKeyMap ✅ DONE (for classes)

**Location:** `internal/checker/infer_module.go` - class inference logic

**What exists for classes:**
- Symbol keys are extracted during class inference
- `staticSymbolKeyMap` and `instanceSymbolKeyMap` track symbol expressions
- Maps are populated in `ObjectType.SymbolKeyMap`

**What may be missing:**
- Equivalent handling for interface declarations (needs verification)
- May need to add similar logic to interface inference

### Task 5.3: Update Property Type to Support Symbol Keys ✅ DONE

**Location:** `internal/type_system/types.go`

**What exists:**
```go
type ObjTypeKey struct {
    Kind ObjTypeKeyKind  // StrObjTypeKeyKind, NumObjTypeKeyKind, SymObjTypeKeyKind
    Str  string
    Num  float64
    Sym  int  // For symbol keys
}
```

- `NewSymKey(sym int)` constructor exists
- Properties can have symbol keys via `ObjTypeKey`

### Task 5.4: Add Interface Symbol Key Inference ⬜ NEEDS VERIFICATION

**Location:** `internal/checker/infer_type_ann.go` or `internal/checker/infer_stmt.go`
**Difficulty:** Medium
**Risk:** Medium

**Context:** Symbol key handling is confirmed working for classes (Task 5.1, 5.2). This task verifies and implements equivalent handling for interfaces.

**Verification steps:**
1. Check if `inferInterface()` handles `ComputedKey` in property/method declarations
2. Check if `SymbolKeyMap` is populated for interface types
3. If missing, implement similar logic to class inference

**What to look for in the code:**
```go
// In interface inference, when processing elements:
case *ast.MethodTypeAnn:
    if computedKey, ok := method.Name.(*ast.ComputedKey); ok {
        // Should evaluate the key expression and populate SymbolKeyMap
    }
```

**If missing, implement:**
```go
func (c *Checker) inferInterfaceElem(ctx Context, elem ast.ObjTypeElem, symbolKeyMap map[int]any) {
    switch e := elem.(type) {
    case *ast.MethodTypeAnn:
        if computedKey, ok := e.Name.(*ast.ComputedKey); ok {
            keyType, _ := c.inferExpr(ctx, computedKey.Expr)
            if uniqueSym, ok := keyType.(*type_system.UniqueSymbolType); ok {
                symbolKeyMap[uniqueSym.Value] = computedKey.Expr
            }
        }
    // ... similar for PropertyTypeAnn
    }
}
```

---

## Phase 6: Symbol Key Property Access (FR7)

**Status:** 🚧 IN PROGRESS (30% complete)
**Difficulty:** Medium
**Risk:** Low

**Goal:** Support accessing properties via symbol keys (e.g., `arr[Symbol.iterator]`).

### Task 6.1: Handle Symbol Index Access 🚧 PARTIAL

**Location:** `internal/checker/infer_expr.go`
**Difficulty:** Medium
**Risk:** Low

**What exists:**
- `astKeyToTypeKey()` in `utils.go` handles conversion of symbol expressions to symbol keys
- Basic infrastructure for symbol key lookup exists

**What needs verification:**
- Full index expression handling for `arr[Symbol.iterator]()`
- Error messages for missing symbol properties
- Integration with method calls on symbol-keyed properties

---

## Phase 7: Testing

**Status:** ⬜ NOT STARTED
**Difficulty:** Easy-Medium
**Risk:** Low

### Task 7.1: Parser Tests ⬜ NOT STARTED

**Location:** `internal/dts_parser/parser_test.go`
**Difficulty:** Easy

```go
func TestParseUniqueSymbol(t *testing.T) {
    source := `interface SymbolConstructor {
        readonly iterator: unique symbol;
    }`
    // Test parsing succeeds and produces correct AST
}

func TestParseComputedKey(t *testing.T) {
    source := `interface Iterable<T> {
        [Symbol.iterator](): Iterator<T>;
    }`
    // Test parsing succeeds and produces ComputedKey with MemberTypeAnn
}
```

### Task 7.2: Lib File Parsing Tests ⬜ NOT STARTED

**Location:** `internal/dts_parser/integration_test.go`
**Difficulty:** Easy

### Task 7.3: Interop Tests ✅ DONE

**Location:** `internal/interop/helper_test.go`, `internal/interop/module_test.go`
**Difficulty:** Easy

**What exists:**

1. **`TestConvertComputedKey`** (`helper_test.go:747-863`):
   - Tests `[Symbol.iterator]` method conversion
   - Tests `[Symbol.toStringTag]` readonly property conversion
   - Tests `[Symbol.hasInstance]` method conversion
   - Validates `MemberExpr` conversion with correct object/property names

2. **`TestConvertComputedKeySimpleIdent`** (`helper_test.go:867-925`):
   - Tests simple identifier computed keys like `[key]`
   - Validates `IdentExpr` conversion

3. **`TestConvertES2015LibFiles`** (`module_test.go:384-444`):
   - Integration test for all 9 ES2015 lib files
   - Verifies parsing and interop conversion succeeds without errors

**Verification:**
```bash
go test ./internal/interop/... -run TestConvertComputedKey -v
go test ./internal/interop/... -run TestConvertES2015LibFiles -v
```

### Task 7.4: Type Inference Tests ⬜ NOT STARTED

**Location:** `internal/checker/tests/`
**Difficulty:** Medium

### Task 7.5: Declaration Merging Tests ⬜ NOT STARTED

**Location:** `internal/checker/tests/`
**Difficulty:** Medium

### Task 7.6: Lib File Discovery Tests ⬜ NOT STARTED

**Location:** `internal/checker/prelude_test.go`
**Difficulty:** Easy

Test the refactored `discoverESLibFiles` / `loadLibFilesRecursive` function:

```go
func TestDiscoverESLibFiles(t *testing.T) {
    libDir := filepath.Join(testRepoRoot, "node_modules", "typescript", "lib")

    tests := []struct {
        name          string
        targetVersion string
        wantContains  []string
        wantNotContains []string
    }{
        {
            name:          "es5 only",
            targetVersion: "es5",
            wantContains:  []string{"lib.es5.d.ts"},
            wantNotContains: []string{"lib.es2015.core.d.ts"},
        },
        {
            name:          "es2015 includes symbol libs",
            targetVersion: "es2015",
            wantContains:  []string{
                "lib.es5.d.ts",
                "lib.es2015.core.d.ts",
                "lib.es2015.symbol.d.ts",
                "lib.es2015.iterable.d.ts",
            },
            wantNotContains: []string{"lib.es2016.array.include.d.ts"},
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            files, err := discoverESLibFiles(libDir, tt.targetVersion)
            require.NoError(t, err)
            for _, want := range tt.wantContains {
                assert.Contains(t, files, want)
            }
            for _, notWant := range tt.wantNotContains {
                assert.NotContains(t, files, notWant)
            }
        })
    }
}

func TestDiscoverESLibFilesNoCycles(t *testing.T) {
    // Verify that circular references (if any) don't cause infinite loops
    libDir := filepath.Join(testRepoRoot, "node_modules", "typescript", "lib")
    files, err := discoverESLibFiles(libDir, "es2015")
    require.NoError(t, err)

    // Check no duplicates
    seen := make(map[string]bool)
    for _, f := range files {
        assert.False(t, seen[f], "duplicate file: %s", f)
        seen[f] = true
    }
}
```

---

## Implementation Order

### Recommended Sequence (Updated)

```
Task 3.1-3.2: ComputedKey conversion ✅ DONE
    ↓
Task 3.4: Validate conversion works on ES2015 lib files ✅ DONE
    ↓
Task 1.5: Refactor discoverESLibFiles ✅ DONE
    ↓
Task 1.3: Enable ES2015 target ✅ DONE
    ↓
Task 4.0: Merge lib files into unified DepGraph ✅ DONE (Risk 2 mitigation)
    ↓
Task 4.1: Computed key dependency tracking ✅ DONE
    ↓
Task 5.4: Verify/implement interface symbol key inference ← NEXT STEP
    ↓
Phase 6: Verify property access
    ↓
Phase 7: Testing (throughout)
```

### Rationale (Updated)

1. ~~**Tasks 3.1-3.2 first**: This is the **critical blocker**. ~35 lines of code unlocks ES2015+~~ ✅ Done
2. ~~**Task 3.4**: Validate the conversion works on actual ES2015 lib files before proceeding~~ ✅ Done
3. ~~**Task 1.5**: Simplify `discoverESLibFiles` to use recursive loading~~ ✅ Done
4. ~~**Task 1.3**: Change `targetVersion := "es5"` to `"es2015"` in prelude.go~~ ✅ Done
5. ~~**Task 4.0**: Merge all lib files into a single module with unified DepGraph (Risk 2 mitigation)~~ ✅ Done
6. **Task 5.4**: Verify interface symbol key inference works (may already work based on class implementation) ← **NEXT**
7. ~~**Task 4.1**: Computed key dependency tracking~~ ✅ Done (handled by unified DepGraph)
8. **Phase 6**: Verify existing property access infrastructure works
9. **Phase 7**: Add tests throughout to validate functionality

---

## Effort Estimates

| Task | Difficulty | Lines of Code | Risk | Status |
|------|------------|---------------|------|--------|
| Task 3.0: Add Expr types to dts_parser | Medium | ~35 | Low | ✅ Done |
| Task 3.1: Add parseExpr() | Medium | ~45 | Low | ✅ Done |
| Task 3.2: Add convertExpr() | Easy | ~25 | Low | ✅ Done |
| Task 3.4: Validation tests | Easy | ~180 | Low | ✅ Done |
| Task 1.3: Enable ES2015 | Medium | ~50 | Low | ✅ Done |
| Task 1.5: Refactor discoverESLibFiles | Medium | ~30 (net reduction) | Low | ✅ Done |
| Task 4.0: Merge lib files (Risk 2) | Medium | ~50-80 | Medium | ✅ Done |
| Task 4.1: Computed key deps | Medium | ~40 | Medium | ✅ Done |
| Task 5.4: Interface symbol keys | Medium | ~20-40 | Medium | ⬜ |
| Task 6.1: Verify property access | Unknown | TBD | Low | ⬜ |
| Task 7.6: Lib discovery tests | Easy | ~50 | Low | ⬜ |

**Phase 3 completed:** ~285 lines added (including new Expr types, parseExpr, convertExpr, and tests)
**Task 1.5 simplification:** Removes ~60 lines, adds ~30 lines (net -30 lines)

---

## Checkpoints

### Checkpoint 1: Parsing Works ✅ COMPLETE
- [x] `unique symbol` parses correctly ✅
- [x] `[Symbol.iterator]` parses as ComputedKey ✅ (dts_parser)
- [x] All ES2015 lib files parse without errors ✅

**Verification:**
```bash
go test ./internal/dts_parser/... -run TestParse -v
go test ./internal/interop/... -run TestConvertES2015LibFiles -v
```

### Checkpoint 2: Interop Works ✅ COMPLETE
- [x] ComputedKey converts to Escalier AST ✅
- [x] UniqueSymbolType converts correctly ✅
- [x] No errors when converting ES2015 lib files ✅

**Verification:**
```bash
go test ./internal/interop/... -run TestConvertComputedKey -v
go test ./internal/interop/... -run TestConvertES2015LibFiles -v
```

### Checkpoint 3: Loading Works ✅ COMPLETE
- [x] Reference directives are parsed ✅
- [x] Recursive loading follows all references ✅
- [x] All declarations from all ES2015 files are collected ✅
- [x] `InvalidObjectKeyError` filtered during lib loading ✅
- [x] `Symbol.customMatcher` added to `SymbolConstructor` ✅
- [x] `CustomMatcherSymbolID` cached for test isolation ✅

**Verification:**
```bash
go test ./internal/checker/... -v
# All tests pass with ES2015 target enabled
```

### Checkpoint 4: Type Inference Works ✅ COMPLETE
- [x] SymbolConstructor interface has all well-known symbols ✅
- [x] Symbol variable has type with `iterator` and `customMatcher` ✅
- [x] Iterable<T> has [Symbol.iterator] method ✅
- [x] Array<T> has [Symbol.iterator] from merged declarations ✅
- [x] All lib file declarations processed in correct dependency order ✅

**Verification:**
```bash
go test ./internal/checker/... -run TestSymbol -v
go test ./internal/checker/... -run TestIterable -v
```

**Manual verification:**
```go
// Add to a test file to inspect Symbol type:
symbolType := globalScope.Lookup("Symbol")
fmt.Printf("Symbol type: %s\n", symbolType)
// Should show iterator, toStringTag, etc.
```

### Checkpoint 5: Property Access Works
- [ ] `arr[Symbol.iterator]()` type-checks correctly
- [ ] Symbol-keyed properties can be accessed
- [ ] Type display shows `[Symbol.iterator]` not internal IDs

**Verification:**
```bash
go test ./internal/checker/... -run TestSymbolKeyAccess -v
```

**Manual verification with Escalier source:**
```
// test.esc
val arr = [1, 2, 3]
val iter = arr[Symbol.iterator]()
// Should type-check without errors
```

---

## Risks and Mitigations

### Risk: Parser Changes Break Existing Code ✅ MITIGATED

**Status:** The `unique` keyword is already implemented and working. No additional parser changes needed.

### Risk: Dependency Graph Changes Affect Performance

**Difficulty:** Medium
**Risk Level:** Medium

**Mitigation:** Benchmark before and after. The additional dependency tracking should be O(n) in the number of declarations.

### Risk: Symbol Identity Issues ✅ MITIGATED

**Status:** Symbol identity is managed via `c.SymbolID` counter in the checker. Well-known symbols come from ES2015 lib files (`lib.es2015.symbol.d.ts`). The `Symbol.customMatcher` property is added by `addCustomMatcherToSymbol()` after lib files are loaded. The `CustomMatcherSymbolID` is stored in the `Checker` struct and cached via `cachedCustomMatcherSymbolID` for test isolation.

### Risk: Symbol Identity Across Declarations (Risk 2 from requirements.md) ✅ MITIGATED

**Status:** Fully mitigated. All lib files are now merged into a single module with a unified DepGraph before calling `InferModule()`.

**Implementation (Task 4.0):**
- Added `mergeModules()` helper to combine AST modules
- Updated `loadGlobalDefinitions()` to merge all lib files before inference
- `SymbolConstructor` declarations from all files are now properly merged
- Declarations are processed in correct dependency order

**Bug fixes during implementation:**
- Fixed infinite loop in `KeyOfType` expansion by adding `insideKeyOfTarget` tracking
- Fixed missing dependency tracking for `FuncTypeAnn` type parameter constraints

### Risk: ComputedKey Conversion Edge Cases

**Difficulty:** Medium
**Risk Level:** Medium

**Potential issues:**
- Complex expressions in computed keys (mapped types, etc.)
- Nested member expressions deeper than `Symbol.X`

**Mitigation:** Start with `IdentTypeAnn` and `MemberTypeAnn` support only. Document unsupported patterns and add them incrementally.

### Risk: Circular References in Lib Files

**Risk Level:** Low

**Context:** The recursive loading approach in Task 1.5 assumes TypeScript lib files do not have circular `/// <reference>` directives.

**Analysis:** TypeScript's lib files are structured as a DAG (directed acyclic graph):
- `lib.es2015.d.ts` references `lib.es2015.core.d.ts`, `lib.es2015.symbol.d.ts`, etc.
- Sub-libraries do not reference back to parent bundles
- No known circular references exist in TypeScript's lib files

**Mitigation:** The `visited` map in the recursive loading function prevents infinite loops even if circular references were introduced:

```go
if visited[filename] {
    return nil, nil // Already processed - breaks any cycle
}
visited[filename] = true
```

**Testing:** Task 7.6 includes `TestDiscoverESLibFilesNoCycles` to verify no duplicates are returned, which would indicate cycle handling is working.

---

## Success Criteria

All items from requirements.md Success Criteria section:

### Parser Criteria
1. [x] `lib.es2015.symbol.d.ts` parses without errors ✅ (dts_parser)
2. [x] `lib.es2015.symbol.wellknown.d.ts` parses without errors ✅ (dts_parser)
3. [x] `lib.es2015.iterable.d.ts` parses without errors ✅ (dts_parser)
4. [x] All 9 ES2015 lib files parse without errors ✅ (dts_parser)

### Interop Criteria
5. [x] Computed keys with `Symbol.X` expressions convert correctly ✅
6. [x] `unique symbol` type converts to a distinct type representation ✅
7. [x] Interface declarations with symbol keys produce correct Escalier AST ✅

### Type System Criteria
8. [x] `ObjectType.SymbolKeyMap` is populated for ~~interfaces~~ classes with symbol keys ✅
9. [x] Multiple interface declarations with the same name merge correctly ✅
10. [x] `unique symbol` properties on `SymbolConstructor` have distinct types ✅

### Type Checking Criteria
11. [ ] `arr[Symbol.iterator]()` type-checks correctly for arrays
12. [ ] `Iterable<T>` interface is available with `[Symbol.iterator]` method
13. [ ] Declaration merging across lib files produces complete interfaces

---

## Next Steps

1. ~~**Immediate:** Implement `convertTypeAnnToExpr` helper in `internal/interop/helper.go`~~ ✅ Done
2. ~~**Immediate:** Update `convertPropertyKey` to handle `ComputedKey`~~ ✅ Done
3. ~~**Test:** Verify ES2015 lib files convert without errors~~ ✅ Done
4. ~~**Enable:** Change `targetVersion` to `"es2015"` in `internal/checker/prelude.go` (Task 1.3)~~ ✅ Done
5. ~~**Implement:** Merge lib files into unified DepGraph (Task 4.0) - Risk 2 mitigation~~ ✅ Done
6. **Verify:** Run type inference tests to confirm symbol-keyed properties work in interfaces (Task 5.4)
7. **Verify:** Test computed key dependency tracking if needed (Task 4.1)
8. **Verify:** Test property access via symbol keys (Phase 6)

## Recent Changes (Task 1.3 Implementation)

The following changes were made to enable ES2015 target:

1. **`internal/checker/checker.go`**: Added `CustomMatcherSymbolID` field to store the symbol ID for `Symbol.customMatcher`

2. **`internal/checker/prelude.go`**:
   - Changed `targetVersion` from `"es5"` to `"es2015"`
   - Added `filterLibFileErrors()` to filter `InvalidObjectKeyError` during lib loading
   - Added `cachedCustomMatcherSymbolID` variable for caching
   - Updated `Prelude()` to cache and restore `CustomMatcherSymbolID`
   - Updated `addCustomMatcherToSymbol()` to store symbol ID in `c.CustomMatcherSymbolID`

3. **`internal/checker/unify.go`**: Replaced hardcoded `Sym == 2` checks with `c.CustomMatcherSymbolID` (two occurrences at lines 605 and 709)

4. **`internal/checker/tests/infer_test.go`**: Updated expected symbol IDs (`symbol2` → `symbol12`, `symbol3` → `symbol13`, `symbol4` → `symbol14`) to reflect additional symbols from ES2015 lib files

## Recent Changes (Task 4.0 Implementation)

The following changes were made to merge lib files into a unified DepGraph:

1. **`internal/checker/prelude.go`**:
   - Added `mergeModules()` helper function to merge AST modules (Namespaces, Files, Sources)
   - Updated `loadGlobalDefinitions()` to:
     - Create combined `globalModule` and `namedModules` structures
     - Load each lib file and merge its modules into the combined structures
     - Call `InferModule()` once with the unified module

2. **`internal/checker/expand_type.go`** (Bug fix: infinite loop in type expansion):
   - Added `insideKeyOfTarget` field to `TypeExpansionVisitor` struct
   - Added `expandTypeWithConfig()` helper function to propagate the flag
   - Modified `KeyOfType` handler to check `insideKeyOfTarget > 0` and skip expansion
   - Modified `TypeRefType` handler to propagate `insideKeyOfTarget` flag
   - This prevents infinite recursion when `KeyOfType` contains nested `KeyOfType` in `MappedElems`

3. **`internal/ast/type_ann.go`** (Bug fix: missing type parameter constraint dependencies):
   - Updated `FuncTypeAnn.Accept()` to visit `TypeParams` and their constraints
   - This ensures dependencies like `T extends ArrayBufferView` are properly tracked in the dependency graph
   - Previously, `ReadableStreamBYOBReader` failed with "Unknown type: ArrayBufferView" because the constraint wasn't visited
