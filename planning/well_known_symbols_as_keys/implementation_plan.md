# Well-Known Symbols as Keys - Implementation Plan

## Overview

This plan outlines the implementation steps for supporting well-known symbols as computed property keys in the Escalier compiler, enabling parsing and type-checking of TypeScript's ES2015+ standard library files.

---

## Implementation Status Summary

| Phase | Description | Status | Progress | Blocking Issues |
|-------|-------------|--------|----------|-----------------|
| **1** | Recursive Lib File Loading | 🔒 Blocked | 60% | Awaiting Phase 3; Task 1.5 refactor pending |
| **2** | Parse `unique symbol` Type | ✅ Done | 100% | None |
| **3** | Convert Computed Property Keys | ⬜ Not Started | 25% | **CRITICAL BLOCKER** (Task 3.3 done) |
| **4** | Dependency Graph for Computed Keys | 🚧 In Progress | 70% | Needs computed key tracking |
| **5** | Infer Symbol-Keyed Properties | 🚧 In Progress | 60% | Task 5.4 needs verification |
| **6** | Symbol Key Property Access | 🚧 In Progress | 30% | Needs verification |
| **7** | Testing | ⬜ Not Started | 0% | Awaiting implementation |

**Legend:** ✅ Done | 🚧 In Progress | ⬜ Not Started | 🔒 Blocked

**Task Breakdown:**
- **Phase 1:** 1.1-1.3 ✅ | 1.4 ⬜ Config | 1.5 ⬜ Refactor
- **Phase 3:** 3.1 ⬜ | 3.2 ⬜ | 3.3 ✅ | 3.4 ⬜ Validation
- **Phase 5:** 5.1-5.3 ✅ | 5.4 ⬜ Interface verification
- **Phase 7:** 7.1-7.5 ⬜ | 7.6 ⬜ Lib discovery tests

---

## Current State

### What's Working
- `lib.es5.d.ts` parsing and type inference
- `lib.dom.d.ts` parsing and type inference
- Declaration merging for interfaces via `MergeInterface()`
- Computed property support in Escalier source code (e.g., `[Symbol.customMatcher]`)
- `ObjectType.SymbolKeyMap` field exists and is populated for classes
- `unique symbol` parsing in dts_parser ✅
- `UniqueSymbolType` in type system ✅
- Recursive lib file loading infrastructure ✅ (disabled for ES2015+)
- Symbol binding in global scope with `iterator` and `customMatcher` ✅

### Note: `lib.dom.d.ts` Handling
`lib.dom.d.ts` is loaded separately from the ES version hierarchy. It is loaded unconditionally alongside the ES lib files in `loadGlobalDefinitions()`. The DOM lib does NOT use computed symbol keys, so it is unaffected by this work. The recursive loading refactor (Task 1.5) should preserve this behavior by loading DOM separately.

### What's Missing
- ~~Parsing `unique symbol` type in dts_parser~~ ✅ Done
- Converting `ComputedKey` in interop layer (returns error) ❌ **BLOCKER**
- ~~Recursive lib file loading with reference following~~ ✅ Done (disabled pending Phase 3)
- Dependency graph awareness of computed keys (partial)
- Type inference for symbol-keyed properties in interfaces (works for classes)

### Key Files

| File | Purpose | Status |
|------|---------|--------|
| `internal/dts_parser/ast.go` | Contains `ComputedKey` struct | ✅ |
| `internal/dts_parser/object.go` | Parses object type members | ✅ |
| `internal/dts_parser/lexer.go` | `unique` keyword token | ✅ |
| `internal/dts_parser/base.go` | Parses `unique symbol` | ✅ |
| `internal/interop/helper.go` | `convertPropertyKey()` - needs ComputedKey support | ❌ |
| `internal/checker/prelude.go` | Lib file loading via `discoverESLibFiles()` | ✅ (ES2015 disabled) |
| `internal/checker/infer_module.go` | Declaration processing and merging | 🚧 |
| `internal/type_system/types.go` | `ObjectType` with `SymbolKeyMap` | ✅ |
| `internal/ast/type_ann.go` | `UniqueSymbolTypeAnn` | ✅ |

> **Note:** Line numbers throughout this document are approximate and may shift as code evolves. Use function/type names (e.g., `convertPropertyKey`, `discoverESLibFiles`) when searching.

---

## Phase 1: Recursive Lib File Loading (FR1)

**Status:** 🔒 Blocked (60% complete - awaiting Phase 3)
**Difficulty:** ~~Medium~~ → Mostly Done
**Risk:** Low

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

### Task 1.3: Update Prelude Loading ✅ DONE (but ES2015 disabled)

**Location:** `internal/checker/prelude.go` - `loadGlobalDefinitions()` function

**What exists:**
- `loadGlobalDefinitions()` calls `discoverESLibFiles()`
- Successfully loads `lib.es5.d.ts` and `lib.dom.d.ts`

**What's blocking:**
- `targetVersion := "es5"` is hard-coded in the function
- Comment states: "Currently limited to ES5 because ES2015+ lib files use ComputedKey"
- **Action required:** Change to `"es2015"` once Phase 3 is complete

### Task 1.4: Add Target Version Configuration ⬜ NOT STARTED

**Location:** `internal/checker/checker.go` or configuration
**Difficulty:** Easy
**Risk:** Low

Add a way to specify the target ES version (currently hard-coded to `es5`).

**Note:** Low priority - can be done after core functionality works.

### Task 1.5: Refactor `discoverESLibFiles` to Use Recursive Loading ⬜ NOT STARTED

**Location:** `internal/checker/prelude.go` - `discoverESLibFiles()` and related helper functions
**Difficulty:** Medium
**Risk:** Low

**Current approach (overly complex):**
1. Starts with `lib.es5.d.ts`
2. Reads directory to find all bundle files (`lib.es2015.d.ts`, `lib.es2016.d.ts`, etc.)
3. Sorts bundle files by version
4. Filters to only include versions up to `targetVersion`
5. Parses each bundle to extract reference directives
6. Builds a flat list of sub-libraries, excluding bundle files themselves

**Problems with current approach:**
- Requires `os.ReadDir` which may not work in WASM
- Complex bundle file detection logic (`bundleFilePattern`, `isBundleFile`)
- Version comparison and filtering logic
- Special-casing of ES5 vs ES2015+

**Proposed simpler approach:**
1. Start with `lib.<targetVersion>.d.ts` (e.g., `lib.es2015.d.ts`)
2. Parse the file to extract reference directives AND any declarations
3. Recursively process each referenced file (following `/// <reference lib="..." />`)
4. Track visited files to avoid duplicates
5. Return declarations in dependency order (referenced files before the referencing file)

```go
// loadLibFilesRecursive loads a lib file and all its references recursively.
// Returns filenames in dependency order (dependencies first).
func loadLibFilesRecursive(libDir string, libName string, visited map[string]bool) ([]string, error) {
    filename := "lib." + libName + ".d.ts"
    if visited[filename] {
        return nil, nil // Already processed
    }
    visited[filename] = true

    filePath := filepath.Join(libDir, filename)
    refs, err := parseReferenceDirectives(filePath)
    if err != nil {
        return nil, fmt.Errorf("failed to parse %s: %w", filename, err)
    }

    var result []string

    // Process dependencies first (depth-first)
    for _, ref := range refs {
        if isESNextFile("lib." + ref + ".d.ts") {
            continue // Skip unstable ESNext features
        }
        refFiles, err := loadLibFilesRecursive(libDir, ref, visited)
        if err != nil {
            return nil, err
        }
        result = append(result, refFiles...)
    }

    // Add this file after its dependencies
    result = append(result, filename)

    return result, nil
}

// discoverESLibFiles returns ES lib files for the given target version.
func discoverESLibFiles(libDir string, targetVersion string) ([]string, error) {
    visited := make(map[string]bool)
    return loadLibFilesRecursive(libDir, targetVersion, visited)
}
```

**Benefits:**
- No directory scanning required (WASM-friendly)
- No bundle file detection logic needed
- No version comparison/filtering
- Naturally handles the dependency tree
- Simpler, more maintainable code
- Can be removed: `bundleFilePattern`, `isBundleFile`, `extractESVersion`, `compareESVersions`

**Note:** This refactoring can be done independently of ComputedKey support, but is blocked by the same issue (ES2015+ files use computed keys).

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

**Location:** `internal/checker/prelude.go:730-757`

**What exists:**
- `addSymbolBinding()` creates Symbol object with `iterator` and `customMatcher` unique symbols
- Well-known symbols are pre-created with unique IDs

---

## Phase 3: Convert Computed Property Keys (FR4)

**Status:** ⬜ NOT STARTED (0% complete) - **CRITICAL BLOCKER**
**Difficulty:** Medium
**Risk:** Medium (must handle all valid computed key patterns)

**Goal:** Convert `ComputedKey` from dts_parser AST to Escalier AST.

**This phase is the critical blocker preventing ES2015+ support.**

### Task 3.1: Add `convertTypeAnnToExpr` Helper ⬜ NOT STARTED

**Location:** `internal/interop/helper.go`
**Difficulty:** Medium
**Risk:** Medium

This helper converts type annotations used in computed key contexts to expressions:

```go
// convertTypeAnnToExpr converts a type annotation (used in computed key context)
// to an expression. Only supports patterns valid in computed keys.
func convertTypeAnnToExpr(typeAnn dts_parser.TypeAnn) (ast.Expr, error) {
    switch t := typeAnn.(type) {
    case *dts_parser.IdentTypeAnn:
        // [foo] -> Ident("foo")
        return ast.NewIdent(t.Name.Name, convertSpan(t.Span())), nil

    case *dts_parser.MemberTypeAnn:
        // [Symbol.iterator] -> Member(Ident("Symbol"), Ident("iterator"))
        left, err := convertTypeAnnToExpr(t.Left)
        if err != nil {
            return nil, err
        }
        right := ast.NewIdent(t.Right.Name, convertSpan(t.Right.Span()))
        return ast.NewMember(left, right, convertSpan(t.Span())), nil

    case *dts_parser.TypeofTypeAnn:
        // [typeof foo] -> needs special handling
        return nil, fmt.Errorf("typeof in computed key not yet supported")

    default:
        return nil, fmt.Errorf("unsupported type annotation in computed key: %T", typeAnn)
    }
}
```

**Patterns to handle:**
1. `IdentTypeAnn` → Simple identifier like `[foo]`
2. `MemberTypeAnn` → Member access like `[Symbol.iterator]` (most common)
3. `TypeofTypeAnn` → Typeof expression (can defer)

### Task 3.2: Implement ComputedKey Conversion ⬜ NOT STARTED

**Location:** `internal/interop/helper.go` - `convertPropertyKey()` function, `ComputedKey` case
**Difficulty:** Easy (once Task 3.1 is done)
**Risk:** Low

**Current code (blocking):**
```go
case *dts_parser.ComputedKey:
    // In dts_parser, ComputedKey.Expr is a TypeAnn
    // In ast, ComputedKey.Expr is an Expr
    // We need to handle this conversion somehow
    // TODO: implement conversion for computed keys
    return nil, fmt.Errorf("convertPropertyKey: ComputedKey not yet implemented")
```

**Required change:**
```go
case *dts_parser.ComputedKey:
    expr, err := convertTypeAnnToExpr(k.Expr)
    if err != nil {
        return nil, fmt.Errorf("converting computed key: %w", err)
    }
    return ast.NewComputedKey(expr, convertSpan(k.Span())), nil
```

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

### Task 3.4: Validate ComputedKey Conversion ⬜ NOT STARTED

**Difficulty:** Easy
**Risk:** Low

**Purpose:** Before enabling ES2015+ globally, validate that ComputedKey conversion works correctly on a single lib file.

**Validation steps:**

1. **Write a minimal test:**
```go
func TestConvertComputedKey(t *testing.T) {
    source := `interface Iterable<T> {
        [Symbol.iterator](): Iterator<T>;
    }`
    module, err := dts_parser.Parse(source)
    require.NoError(t, err)

    converted, err := interop.ConvertModule(module)
    require.NoError(t, err)

    // Verify the interface has a method with ComputedKey
    iface := converted.Stmts[0].(*ast.InterfaceDecl)
    method := iface.Body.Elems[0].(*ast.MethodTypeAnn)
    _, ok := method.Name.(*ast.ComputedKey)
    assert.True(t, ok, "expected ComputedKey")
}
```

2. **Test against real lib file:**
```bash
# Create a simple test that parses and converts lib.es2015.symbol.d.ts
go test ./internal/interop/... -run TestConvertES2015Symbol -v
```

3. **If validation passes:** Proceed to enable ES2015 target (Task 1.3)

4. **If validation fails:** Debug the specific failure before proceeding

**Success criteria:**
- [ ] `lib.es2015.symbol.d.ts` converts without errors
- [ ] `lib.es2015.iterable.d.ts` converts without errors
- [ ] ComputedKey expressions are correctly converted to `ast.ComputedKey`

---

## Phase 4: Dependency Graph for Computed Keys (FR3)

**Status:** 🚧 IN PROGRESS (70% complete)
**Difficulty:** Hard
**Risk:** High (affects processing order of all declarations)

**Goal:** Ensure declarations are processed in correct order when computed keys create dependencies.

### Task 4.1: Detect Computed Key Dependencies 🚧 PARTIAL

**Location:** `internal/dep_graph/`
**Difficulty:** Medium
**Risk:** Medium

**What exists:**
- `DepGraph` structure tracks declarations and dependencies
- `BindingKey` system (value:name, type:name)
- Basic dependency tracking infrastructure

**What's missing:**
- Automatic dependency detection for computed keys like `[Symbol.iterator]`
- When an interface has `[Symbol.iterator]`, it should automatically create a dependency on the `Symbol` variable

```go
// When processing an interface declaration with computed keys:
func (g *DepGraph) addComputedKeyDependencies(decl *ast.InterfaceDecl) {
    for _, elem := range decl.Body.Elems {
        switch e := elem.(type) {
        case *ast.PropertyTypeAnn:
            if computedKey, ok := e.Key.(*ast.ComputedKey); ok {
                // Extract the variable reference from the computed key
                // e.g., [Symbol.iterator] depends on "Symbol"
                varName := extractRootIdent(computedKey.Expr)
                if varName != "" {
                    g.addDependency(decl, varName, DependencyKindValue)
                }
            }
        case *ast.MethodTypeAnn:
            // Similar handling for method computed keys
        }
    }
}

func extractRootIdent(expr ast.Expr) string {
    switch e := expr.(type) {
    case *ast.Ident:
        return e.Name
    case *ast.Member:
        return extractRootIdent(e.Left)
    default:
        return ""
    }
}
```

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

**Status:** 🚧 IN PROGRESS (50% complete)
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

### Task 7.3: Interop Tests ⬜ NOT STARTED

**Location:** `internal/interop/interop_test.go`
**Difficulty:** Easy

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
Task 3.1-3.2: ComputedKey conversion ← CRITICAL, do this first!
    ↓
Task 3.4: Validate conversion works on ES2015 lib files
    ↓
Task 1.5: Refactor discoverESLibFiles (optional, simplifies code)
    ↓
Task 1.3: Enable ES2015 target (flip targetVersion)
    ↓
Task 5.4: Verify/implement interface symbol key inference
    ↓
Task 4.1: Computed key dependency tracking (if needed)
    ↓
Phase 6: Verify property access
    ↓
Phase 7: Testing (throughout)
```

### Rationale (Updated)

1. **Tasks 3.1-3.2 first**: This is the **critical blocker**. ~35 lines of code unlocks ES2015+
2. **Task 3.4**: Validate the conversion works on actual ES2015 lib files before proceeding
3. **Task 1.5 (optional)**: Simplify `discoverESLibFiles` to use recursive loading. Simpler code will be easier to debug.
4. **Task 1.3**: Change `targetVersion := "es5"` to `"es2015"` in prelude.go
5. **Task 5.4**: Verify interface symbol key inference works (may already work based on class implementation)
6. **Task 4.1**: May not be needed if lib file loading order already handles Symbol being defined first
7. **Phase 6**: Verify existing property access infrastructure works
8. **Phase 7**: Add tests throughout to validate functionality

---

## Effort Estimates

| Task | Difficulty | Lines of Code | Risk |
|------|------------|---------------|------|
| Task 3.1: convertTypeAnnToExpr | Medium | ~30 | Medium |
| Task 3.2: ComputedKey conversion | Easy | ~5 | Low |
| Task 3.4: Validation tests | Easy | ~30 | Low |
| Task 1.3: Enable ES2015 | Trivial | ~1 | Low |
| Task 1.5: Refactor discoverESLibFiles | Medium | ~30 (net reduction) | Low |
| Task 4.1: Computed key deps | Medium | ~40 | Medium |
| Task 5.4: Interface symbol keys | Medium | ~20-40 | Medium |
| Task 6.1: Verify property access | Unknown | TBD | Low |
| Task 7.6: Lib discovery tests | Easy | ~50 | Low |

**Total estimated effort for Phase 3:** ~65 lines (including validation tests)
**Task 1.5 simplification:** Removes ~60 lines, adds ~30 lines (net -30 lines)

---

## Checkpoints

### Checkpoint 1: Parsing Works
- [x] `unique symbol` parses correctly ✅
- [x] `[Symbol.iterator]` parses as ComputedKey ✅ (dts_parser)
- [ ] All ES2015 lib files parse without errors (blocked by Phase 3)

**Verification:**
```bash
go test ./internal/dts_parser/... -run TestParse -v
```

### Checkpoint 2: Interop Works
- [ ] ComputedKey converts to Escalier AST ❌ **BLOCKER**
- [x] UniqueSymbolType converts correctly ✅
- [ ] No errors when converting ES2015 lib files

**Verification:**
```bash
# After implementing Task 3.1 and 3.2:
go test ./internal/interop/... -run TestConvertComputedKey -v
go test ./internal/interop/... -run TestConvertES2015 -v
```

### Checkpoint 3: Loading Works
- [x] Reference directives are parsed ✅
- [x] Recursive loading follows all references ✅
- [ ] All declarations from all files are collected (blocked by Phase 3)

**Verification:**
```bash
# After enabling ES2015:
go test ./internal/checker/... -run TestDiscoverESLibFiles -v
go test ./internal/checker/... -run TestLoadGlobalDefinitions -v
```

### Checkpoint 4: Type Inference Works
- [ ] SymbolConstructor interface has all well-known symbols
- [x] Symbol variable has type with `iterator` and `customMatcher` ✅
- [ ] Iterable<T> has [Symbol.iterator] method
- [ ] Array<T> has [Symbol.iterator] from merged declarations

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

**Status:** Symbol identity is managed via `c.SymbolID` counter in the checker. Well-known symbols are pre-created in `addSymbolBinding()`.

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
5. [ ] Computed keys with `Symbol.X` expressions convert correctly ❌ **BLOCKER**
6. [x] `unique symbol` type converts to a distinct type representation ✅
7. [ ] Interface declarations with symbol keys produce correct Escalier AST

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

1. **Immediate:** Implement `convertTypeAnnToExpr` helper in `internal/interop/helper.go`
2. **Immediate:** Update `convertPropertyKey` to handle `ComputedKey`
3. **Test:** Verify ES2015 lib files convert without errors
4. **Enable:** Change `targetVersion` to `"es2015"` in prelude.go
5. **Verify:** Run type inference tests to confirm symbol-keyed properties work
