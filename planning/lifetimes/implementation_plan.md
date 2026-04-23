# Lifetime Implementation Plan

This plan describes how to add liveness-based mutability transitions and lifetime
annotations to Escalier, replacing the current `mut?` system. It is organized
into phases that build incrementally, each producing a testable milestone.

## Phase Overview

| Phase | Description                                          | Depends On | Status |
|------:|------------------------------------------------------|------------|--------|
|     1 | Data structures and representations                  | —          | Done   |
|     2 | Name resolution and VarID assignment                 | 1          | Done   |
|     3 | Liveness analysis (straight-line code)               | 2          | Done   |
|     4 | Liveness analysis (control flow)                     | 3          | Done   |
|     5 | Alias tracking (local variables)                     | 3          | Done   |
|     6 | Mutability transition checking                       | 4, 5       | Done   |
|     7 | Alias tracking (properties, closures, destructuring) | 5, 6       |        |
|     8 | Lifetime annotations and inference                   | 6, 7       |        |
|     9 | Lifetime unification                                 | 8          |        |
|    10 | Lifetime elision rules                               | 8, 9       |        |
|    11 | TypeScript interop                                   | 10         |        |
|    12 | Error messages                                       | 6–11       |        |
|    13 | Remove `mut?`                                        | 6–12       |        |
|    14 | PrintType and display                                | 13         |        |

---

## Phase 1: Data Structures and Representations

**Goal:** Define the core data structures for lifetimes, liveness, and alias
tracking without changing any existing behavior.

### 1.1 Lifetime Variables in the Type System

Add a `LifetimeVar` type to represent lifetime parameters. Unlike type variables,
lifetime variables track aliasing relationships rather than structural types.

**File:** `internal/type_system/types.go`

```go
// LifetimeVar represents a lifetime parameter (e.g. 'a, 'b).
// During inference, Instance is nil. Once bound at a call site,
// Instance points to the concrete LifetimeValue it resolved to.
type LifetimeVar struct {
    ID       int
    Name     string     // e.g. "a", "b" (without the tick)
    Instance *LifetimeValue  // nil until bound
}

// LifetimeValue represents a concrete lifetime — the "identity" of a value
// that can be aliased. Each fresh value created at a program point gets a
// unique LifetimeValue. A LifetimeValue with IsStatic=true represents
// 'static (permanently aliased, e.g. stored into a global).
type LifetimeValue struct {
    ID       int
    Name     string  // lvalue path for diagnostics (e.g. "items", "obj.values", "obj[key]")
    IsStatic bool    // true for 'static
}
```

**Rationale:** Separating `LifetimeVar` (used in function signatures) from
`LifetimeValue` (used during intraprocedural analysis) mirrors how `TypeVarType`
vs concrete types work. `LifetimeVar` is the generic parameter; `LifetimeValue`
is what it gets instantiated to at call sites.

### 1.2 Attach Lifetime to Types

Add an optional `Lifetime` field to types that can carry aliasing information.
Rather than creating a wrapper type (which would require unwrapping everywhere),
add the field directly to the types that participate in aliasing.

**File:** `internal/type_system/types.go`

```go
// Lifetime can be either a LifetimeVar (in function signatures) or a
// LifetimeValue (after instantiation at call sites).
type Lifetime interface {
    isLifetime()
}

func (*LifetimeVar) isLifetime()   {}
func (*LifetimeValue) isLifetime() {}
```

#### Lifetime on Core Type Structs

Lifetimes must be representable at arbitrary positions in the type tree — not
just at the function parameter/return level. The requirements use
lifetime-annotated types like `mut 'a Point`, `'a Array<T>`, and `Array<'a T>`.
To support this, add a `Lifetime` field to each type struct that can participate
in aliasing:

```go
// ObjectType — for 'a {x: number} or mut 'a {x: number}
type ObjectType struct {
    // ... existing fields ...
    Lifetime Lifetime  // nil if no lifetime annotation
}

// TypeRefType — for 'a Point, mut 'a Point, Container<'a, T>, etc.
type TypeRefType struct {
    // ... existing fields (Name, TypeAlias, TypeArgs, etc.) ...
    Lifetime     Lifetime    // nil if no lifetime annotation (e.g. 'a Point)
    LifetimeArgs []Lifetime  // lifetime arguments for constructed types
                             // (e.g. Container<'a> has LifetimeArgs: ['a],
                             //  Pair<'a, 'b> has LifetimeArgs: ['a, 'b])
                             // nil/empty for types with no lifetime parameters
}

// ArrayType — for 'a Array<T> or Array<'a T>
// Note: lifetime on the array itself vs. on the element type are different.
// 'a Array<T> means the array container aliases the source.
// Array<'a T> means the array is fresh but its elements carry lifetime 'a.
// The former uses ArrayType.Lifetime; the latter uses the Lifetime field
// on the element's type (which is itself a TypeRefType, ObjectType, etc.).
type ArrayType struct {
    // ... existing fields ...
    Lifetime Lifetime  // nil if no lifetime annotation
}

// TupleType — for tuples that carry aliasing information
type TupleType struct {
    // ... existing fields ...
    Lifetime Lifetime
}

// Note: MutabilityType (the existing mut wrapper) is removed in Phase 13.2.
// After that phase, mutability is tracked via a `Mutable bool` field on
// each type struct — co-located with the `Lifetime` field added here.
// Until Phase 13, MutabilityType continues to wrap the inner type, and
// the lifetime is stored on the inner type (e.g. TypeRefType.Lifetime),
// not on MutabilityType itself.
```

Types that do NOT need a `Lifetime` field:
- `PrimitiveType` (`number`, `string`, `boolean`) — primitives cannot alias
- `LiteralType` — literal types are values, not references
- `VoidType`, `NullType`, `UndefinedType` — cannot alias
- `UnionType`, `IntersectionType` — lifetimes appear on the member types,
  not on the union/intersection itself

#### Lifetime on Type Arguments

For `Array<'a T>` where the lifetime appears on a type *argument* (not a type
parameter declaration), the lifetime is carried by the type argument's own
type struct. When `T` is instantiated to e.g. `Point`, the result is a
`TypeRefType` with `Lifetime` set to `'a`. This means lifetimes on generic
type arguments are represented naturally through the existing type tree — no
special field on `TypeParam` is needed.

`TypeParam` (which represents type parameter *declarations* like `T` in
`<T>`) does NOT carry a `Lifetime` field. Lifetimes on type arguments are
expressed through the concrete types that replace type parameters during
instantiation.

#### Lifetime on Function Signatures

No additional `Lifetime` field is needed on `FuncParam`. The parameter's
lifetime is carried by `FuncParam.Type`'s own `Lifetime` field (e.g.
`TypeRefType.Lifetime`). All reads and writes of a parameter's lifetime go
through `FuncParam.Type`, avoiding duplication and drift.

```go
type FuncParam struct {
    Pattern  Pat
    Type     Type       // Type carries its own Lifetime field
    Optional bool
    // No standalone Lifetime field; lifetime is read from Type.
    // If caching is needed, derive it during checking and keep Type canonical.
}
```

To avoid ad-hoc traversals of `FuncParam.Type` throughout the codebase,
provide a canonical accessor:

```go
// GetLifetime extracts the lifetime from a type, walking through wrapper
// types as needed. Returns nil if the type carries no lifetime.
func GetLifetime(t Type) Lifetime
```

**Behavior:**
- `TypeRefType` — returns `t.Lifetime` directly.
- `MutabilityType` — unwraps and recurses into the inner type.
- `UnionType` / `IntersectionType` — returns the common lifetime if all
  member types share the same lifetime; returns `nil` (no single lifetime)
  if they differ. Callers that need stricter handling (e.g. reporting a
  conflict) should inspect member lifetimes directly.
- Primitive, literal, void, null, undefined types — returns `nil`.
- All other compound types — recurses into the relevant inner type.

All call sites that currently read `FuncParam.Type` to obtain a lifetime
(e.g. during alias analysis, transition checking, and return-type
validation) should use `GetLifetime` instead of accessing type fields
directly.

Add `LifetimeParams` to `FuncType` (alongside existing `TypeParams`):

```go
type FuncType struct {
    LifetimeParams []*LifetimeVar   // e.g. ['a, 'b]
    TypeParams     []*TypeParam
    Params         []*FuncParam
    Return         Type             // lifetime is on the Return type itself
    Throws         Type
    provenance     Provenance
}
```

Note: There is no separate `ReturnLifetime` field on `FuncType`. The return
type's lifetime is carried by the `Return` type's own `Lifetime` field (e.g.
`TypeRefType.Lifetime`). This keeps lifetime representation uniform throughout
the type tree.

Add `LifetimeParams` to `TypeAlias` (alongside existing `TypeParams`).
This is needed for classes whose constructors store reference-typed
parameters as fields — the inferred lifetimes become parameters of the
class type itself (e.g. `Container<'a>`, `Pair<'a, 'b>`):

```go
type TypeAlias struct {
    Type            Type
    TypeParams      []*TypeParam
    LifetimeParams  []*LifetimeVar  // e.g. ['a, 'b] for Pair<'a, 'b>
    DefaultMutable  bool            // true if instances default to mut
                                    // (class has mut self methods and no
                                    // immutable modifier)
    Exported        bool
    IsTypeParam     bool
}
```

When a class has both type parameters and lifetime parameters, they
coexist: `Container<'a, T>` has `LifetimeParams: ['a]` and
`TypeParams: [T]`. At construction sites, lifetime arguments are
instantiated alongside type arguments and stored in
`TypeRefType.LifetimeArgs`.

### 1.3 Liveness Data Structures

Create a new package `internal/liveness/` for the analysis pass.

**File:** `internal/liveness/liveness.go`

```go
package liveness

// VarID uniquely identifies a variable within a function body.
// Sequential integer IDs are assigned during name resolution (Phase 2)
// and stored directly on AST nodes (IdentExpr.VarID, IdentPat.VarID,
// etc.). The rename pass sets these; liveness and alias analysis read
// them from the AST nodes.
//
// Each binding gets a unique VarID regardless of name. When the same
// name is bound multiple times (across scopes, or within the same scope
// if same-scope shadowing is added), each binding gets its own VarID.
// Name-based identity would conflate distinct variables that happen to
// share a name.
//
// A VarID of 0 indicates an unresolved or unset ID. Local variable IDs
// start at 1. Non-local variables (module-level, prelude) are assigned
// IDs starting at -1 and counting down, so downstream phases can
// distinguish them: any VarID < 0 is non-local and should be ignored
// by liveness and alias analysis.
//
// Why non-local variables are excluded: liveness and alias analysis are
// intraprocedural — they operate within a single function body. A
// module-level variable has an unbounded lifetime: it is accessible from
// any function, at any time, for the entire program's execution. There
// is no "last use" to compute and no point where it becomes dead, so
// intraprocedural liveness analysis cannot meaningfully track it. Aliasing
// through module-level variables is handled separately via escaping
// reference detection ('static lifetime, Phase 8.4).
type VarID int

// StmtRef identifies a statement by its position in the CFG: the basic
// block it belongs to and its index within that block. This is the key
// used by LivenessInfo to look up per-statement liveness sets.
type StmtRef struct {
    BlockID  int  // index into CFG.Blocks
    StmtIdx  int  // index within BasicBlock.Stmts
}

// LivenessInfo stores the results of liveness analysis for a function body.
// Liveness sets are indexed by basic block ID and statement index within
// the block, avoiding the need to use AST spans as map keys.
type LivenessInfo struct {
    // LiveBefore[blockID][stmtIdx] is the set of variables that are live
    // just before the statement at that position.
    LiveBefore [][]map[VarID]bool

    // LiveAfter[blockID][stmtIdx] is the set of variables that are live
    // just after the statement at that position.
    LiveAfter [][]map[VarID]bool

    // LastUse maps each variable to the location of its last use.
    LastUse map[VarID]StmtRef
}

// IsLiveAfter returns whether the given variable is live after the
// statement at the given position.
func (l *LivenessInfo) IsLiveAfter(ref StmtRef, v VarID) bool {
    return l.LiveAfter[ref.BlockID][ref.StmtIdx][v]
}
```

### 1.4 Alias Set Data Structures

**File:** `internal/liveness/alias.go`

```go
package liveness

// AliasSet tracks a group of variables that reference the same underlying
// value. Each value created at runtime gets its own AliasSet. Variables
// join an alias set when assigned from another variable in the set.
//
// Note: AliasSet intentionally carries only a VarID for Origin, not rich
// diagnostic info (spans, creation context, etc.). Detailed diagnostic
// context is assembled at error-construction time by the error types in
// Phase 12 (e.g. AliasOrigin on LiveMutableAliasError). Keeping AliasSet
// lightweight avoids coupling the analysis data structure to the error
// reporting format.
type AliasSet struct {
    ID        SetID
    Members   map[VarID]Mutability  // variable → whether it holds a mut ref
    Origin    VarID                 // the variable that created the value
    IsStatic  bool                  // true if this value has 'static lifetime
}

type Mutability int

const (
    Immutable Mutability = iota
    Mutable
)

// SetID uniquely identifies an alias set within an AliasTracker.
type SetID int

// AliasTracker manages alias sets for a function body.
// A variable may belong to multiple alias sets when assigned from different
// values depending on control flow (conditional aliasing, Phase 7.4).
type AliasTracker struct {
    NextID    SetID
    Sets      map[SetID]*AliasSet     // SetID → AliasSet
    VarToSets map[VarID][]SetID       // variable → which alias sets it belongs to
}

func NewAliasTracker() *AliasTracker { ... }

// NewValue creates a fresh alias set for a newly created value (e.g. a
// literal, constructor call, or function returning a fresh value).
func (a *AliasTracker) NewValue(v VarID, mut Mutability) { ... }

// AddAlias adds a variable to the alias set of another variable.
func (a *AliasTracker) AddAlias(target VarID, source VarID, mut Mutability) { ... }

// Reassign removes a variable from its current alias set and optionally
// adds it to a new one (if assigned from another variable) or creates
// a fresh set (if assigned a fresh value).
func (a *AliasTracker) Reassign(v VarID, newSource *VarID, mut Mutability) { ... }

// MergeAliasSets merges the alias sets of two variables into a single
// set. All members of both sets become members of the merged set, and
// VarToSets is updated so every affected variable points to the merged
// set. The second set is removed from Sets.
//
// This is used when a value is stored into an object property
// (e.g. obj.next = node) — the container's and the value's alias sets
// must be merged so that transitive connections through property chains
// are preserved even when intermediate variables are reassigned.
// See Phase 7.1 for details.
func (a *AliasTracker) MergeAliasSets(v1 VarID, v2 VarID) { ... }

// GetAliasSets returns all alias sets that v belongs to. A variable may
// belong to multiple sets due to conditional aliasing (Phase 7.4).
// Callers must iterate all returned sets to avoid missing conflicts.
func (a *AliasTracker) GetAliasSets(v VarID) []*AliasSet { ... }
```

### 1.5 Lifetime Counter on Checker

Add a `LifetimeVarID` counter to the `Checker` struct for generating fresh
lifetime variables, similar to the existing `TypeVarID`.

**File:** `internal/checker/checker.go`

```go
type Checker struct {
    // ... existing fields ...
    LifetimeVarID int
}

func (c *Checker) FreshLifetimeVar(name string) *type_system.LifetimeVar {
    c.LifetimeVarID++
    return &type_system.LifetimeVar{
        ID:   c.LifetimeVarID,
        Name: name,
    }
}
```

### 1.6 Tests

- Unit tests for `AliasTracker` operations (new value, add alias, reassign,
  merge, get aliases)
- Unit tests for `LifetimeVar` and `LifetimeValue` construction

---

## Phase 2: Name Resolution and VarID Assignment

**Goal:** Build a pre-pass that walks the AST, assigns a unique `VarID` to
every local variable binding and use site, validates that all variable uses
refer to in-scope bindings, and produces a mapping that downstream phases
(liveness, alias tracking) can consume without any scope lookup.

### 2.1 Why a Separate Phase

Currently, name resolution happens inline during type checking via
`ctx.Scope.GetValue(expr.Name)`. The liveness and alias analyses run as a
pre-pass *before* type checking (see Phase 6.4), so they cannot rely on the
checker's scope.

This phase performs **alpha-conversion** (renaming): every local variable
binding gets a unique `VarID`, and every use site is resolved to its
binding's `VarID`. After this pass completes, all downstream phases work
exclusively with VarIDs — no name-based lookup is needed for local
variables, and the scope stack used during renaming is discarded.

The rename pass also **validates** that every variable use refers to an
in-scope binding. Unresolved local names are reported as errors during this
pass, so the checker does not need to re-check local variable scoping.

**Scope of this pass:** This phase covers local variable bindings only —
`VarDecl`, function parameters, destructuring bindings, `for..in` loop
variables, etc. Module-level bindings (imports, top-level declarations) and
type names are *not* handled here; they continue to use the checker's
existing lookup mechanisms (a flat map or the existing scope), since they
don't participate in liveness or alias analysis.

### 2.2 Rename Pass

**File:** `internal/liveness/rename.go`

The rename pass uses a temporary scope stack internally to resolve names to
VarIDs. The scope stack is local to the pass and is discarded after it
completes — no downstream phase ever sees it.

```go
package liveness

import "github.com/escalier-lang/escalier/internal/ast"

// scope is internal to the rename pass. It tracks name-to-VarID mappings
// during the top-to-bottom walk. It is discarded after Rename() returns.
type scope struct {
    parent   *scope
    bindings map[string]VarID
}

func (s *scope) lookup(name string) (VarID, bool) {
    if id, ok := s.bindings[name]; ok {
        return id, true
    }
    if s.parent != nil {
        return s.parent.lookup(name)
    }
    return 0, false
}
```

For same-scope shadowing, a new `VarDecl` for an existing name overwrites
the entry in `bindings`. Uses before the shadow were already resolved to the
old `VarID`; uses after resolve to the new one, because the walk processes
statements top-to-bottom.

When the walk enters a nested block (e.g. `do { ... }`, `for ... { ... }`),
a new `scope` is pushed. When the block ends, the scope is popped, restoring
visibility of any outer bindings that were shadowed.

### 2.3 VarID on AST Nodes

Rather than maintaining span-keyed side-table maps, the rename pass sets
`VarID` directly on AST nodes. This is consistent with the existing pattern
of storing `InferredType` on nodes.

**Binding sites:** Add a `VarID` field to each pattern node that introduces
a binding — `IdentPat`, destructuring patterns, and `FuncParam`. The rename
pass sets this field when it processes the binding.

**Use sites:** Add a `VarID` field to `IdentExpr`. The rename pass sets
this field when it resolves a use to its binding.

Downstream phases read `VarID` directly from the AST node (e.g.
`expr.VarID`) instead of performing a map lookup.

### 2.4 Rename Result

**File:** `internal/liveness/rename.go`

```go
// RenameResult holds the output of the rename pass for a function body.
// VarIDs are stored directly on AST nodes (IdentExpr, IdentPat, etc.)
// rather than in side-table maps.
type RenameResult struct {
    // NextID is the number of distinct local variables found (for sizing
    // data structures in later phases).
    NextID int

    // Errors contains any unresolved variable references found during
    // the rename pass.
    Errors []RenameError
}

// RenameError represents a variable use that could not be resolved to
// any in-scope binding.
type RenameError struct {
    Name string
    Span ast.Span
}

// Rename walks a function body, assigns VarIDs to all local binding and
// use sites, and validates that all variable uses resolve to in-scope
// bindings. VarIDs are set directly on AST nodes (IdentExpr.VarID,
// IdentPat.VarID, etc.). Module-level and prelude bindings are supplied
// via outerBindings so that free variables can be distinguished from
// truly unresolved names.
//
// After this function returns, the internal scope stack is discarded.
// All downstream phases read VarIDs directly from AST nodes.
func Rename(body ast.Block, outerBindings map[string]VarID) *RenameResult { ... }
```

The `outerBindings` parameter supplies module-level and prelude bindings
(e.g. top-level `val`/`var` declarations, `fn` declarations, imports).
These are assigned negative VarIDs (starting at -1, counting down) so they
occupy a distinct range from local variables (which start at 1). This
allows downstream phases to distinguish non-local references by checking
`VarID < 0`. A use site that resolves to an outer binding has its `VarID`
set to the corresponding negative ID. Liveness and alias analysis skip
any VarID < 0, since module-level variables have unbounded lifetimes —
they are accessible from any function at any time and cannot be tracked
by intraprocedural analysis (see the VarID comment above for the full
rationale). `NextID` counts only local variables.

### 2.5 Integration

The rename pass runs once per function body at the start of
`inferFuncBody`, before liveness analysis:

```go
func (c *Checker) inferFuncBody(ctx Context, body ast.Block, ...) {
    // 1. Rename: assign VarIDs, validate scoping
    renamed := liveness.Rename(body, ctx.outerBindings())

    // Report any unresolved variable errors
    for _, err := range renamed.Errors {
        errors = append(errors, ...)
    }

    // 2. Liveness analysis (Phase 3) reads VarIDs from AST nodes
    // 3. Alias tracking (Phase 5) reads VarIDs from AST nodes
    // No scope stack is passed to any downstream phase.
    ...
}
```

After this point, the checker's existing `ctx.Scope` is still used for
type name resolution and module-level lookups, but local variable resolution
is handled entirely through VarIDs on AST nodes.

**Note:** `ctx.outerBindings()` must include *all* names that are valid
in the function's outer scope — not just module-level value bindings, but
also namespace names from directory-based modules (i.e. `lib/`
subdirectories) and enum namespaces. These are assigned negative VarIDs
like any other outer binding so that the rename pass does not report them
as unresolved. Liveness and alias analysis ignore negative VarIDs, so
namespace names are effectively invisible to those phases.

### 2.6 Tests

- Simple binding and use: `val x = 1; print(x)` → `x` gets one VarID,
  the use resolves to it
- Cross-scope shadowing: `val x = 1; do { val x = 2; print(x) }; print(x)`
  → two distinct VarIDs, inner `print(x)` resolves to the inner one, outer
  `print(x)` resolves to the outer one; after the block ends, the outer `x`
  is visible again
- Same-scope shadowing: `val x = 1; val y = x; val x = 2; print(x)` → two
  distinct VarIDs for `x`, `y = x` resolves to the first, `print(x)`
  resolves to the second
- Function parameters: `fn f(a, b) { print(a) }` → `a` and `b` get
  distinct VarIDs
- Destructuring: `val {a, b} = obj` → `a` and `b` each get a VarID
- Unresolved local name: `print(unknown)` where `unknown` is not in
  `outerBindings` → reported as a `RenameError`
- Module-level name: `print(globalVar)` where `globalVar` is in
  `outerBindings` → resolved successfully, `VarID` set on the `IdentExpr`
- Scope restoration after block: `val x = 1; do { val x = 2 }; print(x)`
  → `print(x)` resolves to the outer `x`, not the inner one

---

## Phase 3: Liveness Analysis — Straight-Line Code

**Goal:** Compute which variables are live at each program point, starting
with sequential code (no branching).

### 3.1 Variable Use Collection

**File:** `internal/liveness/collect_uses.go`

`CollectUses(stmts []ast.Stmt) []StmtUses` walks a block of statements and
returns per-statement use/def information. Each `StmtUses` contains separate
`Uses` and `Defs` slices of `VarID`. Only local variables (VarID > 0) are
tracked; non-local variables (VarID < 0) and unresolved references (VarID == 0)
are filtered out.

The collector mirrors the rename pass's AST traversal and handles:
1. `IdentExpr` — reads `VarID` and records as a use
2. `MemberExpr` — records a use of the base object
3. Plain assignment (`b = expr`) — records the LHS as a **definition only**
   (no read of the old value); for member/index assignment targets, the base
   object is recorded as a use
4. `VarDecl` — collects uses from the initializer, then records all binding
   sites in the pattern as definitions
5. Nested expressions (if/else, match, try/catch, do, etc.) — uses within
   nested blocks are collected into the enclosing statement's uses

Escalier has no compound assignment operators (`+=`, `-=`, etc.), so there is
no use+def case for assignment targets.

**Note:** `FuncDecl.Name` is `*ast.Ident` which has no `VarID` field, so
function name definitions are not yet tracked by liveness. A `VarID` field
should be added to `FuncDecl` in a future change.

### 3.2 Backward Liveness Analysis (Linear)

**File:** `internal/liveness/analyze.go`

`AnalyzeBlock(stmts []ast.Stmt) *LivenessInfo` computes liveness for a linear
block of statements by walking backward:

1. Start with `LiveAfter = {}` for the last statement
2. For each statement, working backward:
   - `LiveAfter[stmt] = LiveBefore[next_stmt]`
   - `LiveBefore[stmt] = (LiveAfter[stmt] - Defs[stmt]) ∪ Uses[stmt]`
3. A variable is "dead" at a point if it is not in `LiveBefore` or `LiveAfter`

Results are stored in `LivenessInfo` indexed by `(blockID=0, stmtIdx)` for the
single-block model. Phase 4 extends this to multiple blocks via a CFG.
`LastUse` is populated by scanning forward to find the last statement where
each variable appears in the use set.

### 3.3 Integration Point

**File:** `internal/checker/checker.go`

Added fields to `Context`:

```go
Liveness  *liveness.LivenessInfo
Aliases   *liveness.AliasTracker
StmtToRef map[ast.Stmt]liveness.StmtRef
```

### 3.4 Tests

**File:** `internal/liveness/analyze_test.go`

6 `CollectUses` tests:
- Simple declaration (def only)
- Declaration with identifier initializer (use + def)
- Assignment (def only, no use of old value)
- Expression statement with function call (use)
- Member expression (use of base object)
- Non-local variables are filtered out

10 `AnalyzeBlock` tests:
- Empty block
- Simple sequential declarations and uses
- Variable becomes dead after its last use
- Variable definitions kill liveness
- Unused variables are never live
- Shadowing with distinct VarIDs: `val x = 1; val y = x; val x = 2; print(x)`
- LastUse tracking with multiple uses of the same variable
- IsLiveAfter helper method
- Multiple variables with overlapping lifetimes
- Assignment from one variable to another

---

## Phase 4: Liveness Analysis — Control Flow

**Goal:** Extend liveness analysis to handle branching, loops, early returns,
and throws.

### 4.1 Control Flow Graph (CFG) Construction

Build a CFG from the AST for each function body. Each node in the CFG is a
basic block (a sequence of statements with no internal branches).

**File:** `internal/liveness/cfg.go`

```go
// BasicBlock represents a maximal sequence of statements with no internal
// branching. Control flow edges connect blocks.
type BasicBlock struct {
    ID         int
    Stmts      []ast.Stmt
    Successors []*BasicBlock
    Predecessors []*BasicBlock
}

// CFG represents the control flow graph for a function body.
type CFG struct {
    Entry  *BasicBlock
    Exit   *BasicBlock
    Blocks []*BasicBlock
}

// BuildCFG constructs a control flow graph from a function body.
func BuildCFG(body ast.Block) *CFG { ... }
```

The CFG builder handles:
- **`IfElseExpr` / `IfLetExpr`:** Two successor edges from the condition block
  — one to the consequent block, one to the alternative (or the join point if
  no else)
- **`ForInStmt`:** Back edge from the end of the loop body to the loop header;
  exit edge from the header to the post-loop block
- **`MatchExpr`:** One successor edge per case arm from the match entry
- **`ReturnStmt`:** Edge to the exit block (terminates the path). This
  reduces the set of paths on which subsequent variables are live, which
  can enable mutability transitions that would otherwise be rejected.
- **`ThrowExpr`:** Edge to the exit block (terminates the path). Same
  path-reduction effect as `ReturnStmt`.
- **`BlockExpr`:** Nested block — inline into the CFG with its own basic blocks

### 4.2 Backward Liveness on CFG

Standard dataflow analysis using the CFG:

```text
for each block b:
    LiveOut[b] = ∪ { LiveIn[s] | s ∈ successors(b) }
    LiveIn[b]  = (LiveOut[b] - Defs[b]) ∪ Uses[b]
```

Iterate until fixed point (typically 2-3 iterations for simple programs,
more for nested loops).

**File:** `internal/liveness/analyze.go`

```go
// AnalyzeFunction computes liveness for a full function body with
// control flow. Replaces AnalyzeBlock for bodies with branches/loops.
func AnalyzeFunction(cfg *CFG) *LivenessInfo { ... }
```

### 4.3 Statement-Level Granularity

The CFG produces per-block liveness, but mutability checking needs per-statement
granularity. Within each basic block, use the linear analysis from Phase 3 to
compute per-statement liveness, using the block's `LiveOut` as the initial
`LiveAfter` for the last statement. The results are stored in `LivenessInfo`
indexed by `(blockID, stmtIdx)`, so callers can look up liveness at any
statement using a `StmtRef`.

### 4.4 Tests

- `if/else`: Variable used only in one branch is dead on the other
- `if` without else: Variable used after the if is live through both branches
- `for` loops: Variable used inside loop body is live for the entire loop
- Early `return`: Variable used only after a return is dead on the returning path
- `throw`: Same as early return
- `match` expressions: Variable used in one arm may be dead in others
- Nested control flow: if inside for, match inside if, etc.

---

## Phase 5: Alias Tracking — Local Variables

**Goal:** Track which variables alias the same value through direct assignment.

### 5.1 Integrate AliasTracker with Statement Processing

As the checker processes each statement in a function body, update the
`AliasTracker`:

**File:** `internal/liveness/alias_analysis.go`

Walk statements in order. The `mut` argument to each call is determined by
the **declared or inferred type of the variable being bound** — specifically,
whether the binding's type is `mut` or immutable. Since alias tracking runs
incrementally during type checking (Phase 6.4), the variable's type is
already known by the time the `AliasTracker` is updated.

1. **`VarDecl` with literal/constructor init:** Call `NewValue(varID, mut)` to
   create a fresh alias set
   ```esc
   val a: mut Point = {x: 0, y: 0}   // NewValue(a, Mutable)
   val b: Point = {x: 0, y: 0}       // NewValue(b, Immutable)
   ```
2. **`VarDecl` with identifier init** (e.g. `val b = a`): Call
   `AddAlias(b, a, mut)` to add `b` to `a`'s alias set
   ```esc
   val c: mut Point = a               // AddAlias(c, a, Mutable)
   val d: Point = a                   // AddAlias(d, a, Immutable)
   ```
3. **Assignment** (e.g. `b = a` where `b` is `var`): Call
   `Reassign(b, &a, mut)` to leave old set and join `a`'s set
4. **Assignment with fresh value** (e.g. `b = {x: 1}`): Call
   `Reassign(b, nil, mut)` to create a new alias set

The mutability stored in the alias set's `Members` map records how each
variable accesses the shared value. This is what `CheckMutabilityTransition`
(Phase 6) inspects: it iterates the alias set's members and checks whether
any live member has a conflicting mutability.

### 5.2 Determine Alias Source from Expressions

Not all initializers are simple identifiers. Need a function that examines an
expression and determines whether it's:
- A **fresh value** (literal, object construction, array literal, `new` call) →
  no aliasing
- A **variable reference** (identifier) → aliases that variable
- A **function call** → depends on lifetime annotations (Phase 8); for now,
  treat as fresh
- A **property access** (e.g. `obj.field`) → aliases the property's source
  (Phase 7)
- A **conditional** (e.g. `if cond { a } else { b }`) → aliases all branches
  (Phase 7)

**File:** `internal/liveness/alias_analysis.go`

```go
// AliasSource describes where a value comes from.
type AliasSource struct {
    Kind    AliasSourceKind
    VarIDs  []VarID  // empty for Fresh, one for Variable, multiple for Conditional
}

type AliasSourceKind int

const (
    AliasSourceFresh    AliasSourceKind = iota  // new value, no alias
    AliasSourceVariable                         // aliases a specific variable
    AliasSourceMultiple                         // aliases one of several variables (conditional)
    AliasSourceUnknown                          // cannot determine statically (function call without lifetime info)
)

// DetermineAliasSource examines an expression and returns its alias source.
// When the expression is an IdentExpr, the VarID is read directly from
// the node (set by the rename pass in Phase 2).
func DetermineAliasSource(expr ast.Expr) AliasSource { ... }
```

### 5.3 Tests

- `val b = a` → b and a are in the same alias set
- `val b = {x: 1}` → b gets a fresh alias set
- `var b = a; b = {x: 1}` → b leaves a's set after reassignment
- `var b = a; b = c` → b leaves a's set and joins c's set
- Multiple aliases: `val b = a; val c = a` → a, b, c all in same set
- Chain: `val b = a; val c = b` → a, b, c all in same set
- Shadowing: `val x = a; val x = {y: 1}` → second x gets a fresh alias set,
  first x remains in a's alias set (distinct VarIDs despite same name)

---

## Phase 6: Mutability Transition Checking

**Goal:** Enforce Rules 1 and 2 from the requirements — reject mutability
transitions when conflicting live aliases exist.

### 6.1 Transition Check Logic

When a value is assigned from one variable to another with a different
mutability, check the alias set and liveness:

**File:** `internal/checker/check_transitions.go`

```go
// CheckMutabilityTransition verifies that a mutability transition is safe
// at the given program point. Returns an error if conflicting live aliases
// exist AND the target alias is itself live (a dead target cannot observe
// violations, so the transition is safe).
//
// Rule 1 (mut → immutable): No live mutable aliases may exist after this point,
//     provided the target (immutable) alias is also live.
// Rule 2 (immutable → mut): No live immutable aliases may exist after this point,
//     provided the target (mutable) alias is also live.
// Rule 3: Multiple mutable aliases are always allowed.
func (c *Checker) CheckMutabilityTransition(
    ctx Context,
    sourceVar VarID,
    targetVar VarID,
    sourceMut bool,     // mutability of the source
    targetMut bool,     // mutability of the target
    assignRef liveness.StmtRef,
) []Error { ... }
```

The algorithm:
1. If `sourceMut == targetMut`, no transition — always OK (Rule 3 for mut→mut)
2. If `targetVar` is **not live** after `assignRef`, the transition is safe —
   a dead target alias can never observe a conflicting mutation. Return early.
3. Get **all** alias sets of `sourceVar` via `GetAliasSets(sourceVar)` (a
   variable may belong to multiple sets due to conditional aliasing)
4. For each alias set that `sourceVar` belongs to:
   - For each variable `v` in that alias set (including `sourceVar`):
     - Check if `v` is live after `assignRef` (using `LivenessInfo.IsLiveAfter`)
     - If `sourceMut && !targetMut` (Rule 1): error if `v` has mutable
       access and is live
     - If `!sourceMut && targetMut` (Rule 2): error if `v` has immutable
       access and is live
5. Union all conflicting live aliases across all sets into the error report

### 6.2 Integration with `inferVarDecl`

Hook the transition check into variable declaration processing in
`infer_stmt.go`. After unification succeeds, check whether the assignment
involves a mutability transition:

```go
func (c *Checker) inferVarDecl(ctx Context, decl *ast.VarDecl) (...) {
    // ... existing type inference ...

    // After successful type inference, check mutability transitions
    if ctx.Liveness != nil {
        transErrors := c.checkTransitionsForDecl(ctx, decl, bindings)
        errors = slices.Concat(errors, transErrors)
    }

    return bindings, errors
}
```

### 6.3 Integration with Assignment Expressions

For `var` reassignment (`b = expr`), also check transitions:

**File:** `internal/checker/infer_expr.go` (in the assignment case)

After type-checking the assignment, invoke `CheckMutabilityTransition` with
the target variable and the source expression's alias information.

### 6.4 Running the Analysis

The liveness and alias analyses must run before or during type checking of a
function body. Two options:

**Option A: Pre-pass.** Run liveness analysis on the AST before type checking
the function body. This requires the AST to already have binding information
(from pattern inference), but not full types.

**Option B: Integrated.** Compute liveness and aliases incrementally as the
checker walks statements. This is simpler to implement since the checker already
walks statements in order, but requires backward liveness to be precomputed.

**Recommended approach:** Use a **pre-pass** for name resolution and liveness
(backward analysis needs full knowledge of uses) and build alias sets
**incrementally** during type checking (alias relationships are discovered as
statements are processed).

The pre-pass runs at the start of `inferFuncBody` or equivalent:
1. Resolve names → VarIDs (Phase 2)
2. Collect variable uses from the function body AST
3. Build CFG
4. Run backward liveness analysis
5. Store `LivenessInfo` and `CFG` on the `Context`
6. Build a `StmtToRef map[ast.Stmt]StmtRef` lookup (by walking CFG blocks
   and recording each statement's position) and store it on the `Context`
7. Initialize `AliasTracker` on the `Context`
8. During statement-by-statement type checking, use `StmtToRef` to look up
   the current statement's `StmtRef`, update `AliasTracker`, and invoke
   `CheckMutabilityTransition` at each assignment

### 6.5 Tests

Test cases from the requirements:

**Rule 1 — mut to immutable:**
```esc
// OK: items is dead after the assignment
val items: mut Array<number> = [1, 2, 3]
items.push(4)
val snapshot: Array<number> = items
print(snapshot.length)

// ERROR: items is used mutably after the assignment
val items: mut Array<number> = [1, 2, 3]
val snapshot: Array<number> = items  // ERROR
items.push(4)
print(snapshot.length)
```

**Rule 2 — immutable to mut:**
```esc
// OK: config is dead after the assignment
val config: {host: string} = {host: "localhost"}
print(config.host)
val mutableConfig: mut {host: string} = config
mutableConfig.host = "0.0.0.0"

// ERROR: config is used after the assignment
val config: {host: string} = {host: "localhost"}
val mutableConfig: mut {host: string} = config  // ERROR
mutableConfig.host = "0.0.0.0"
print(config.host)
```

**Rule 3 — multiple mutable aliases allowed:**
```esc
// OK
val a: mut {x: number} = {x: 1}
val b: mut {x: number} = a
b.x = 2
print(a.x)
```

**Alias tracking:**
```esc
// ERROR: r is a live mutable alias of p, and q is live (used below)
val p: mut Point = {x: 0, y: 0}
val r: mut Point = p
val q: Point = p  // ERROR: r is live and mutable, q is live
r.x = 5
print(q.x)

// OK: q is dead (never used after assignment), so no conflict
val p: mut Point = {x: 0, y: 0}
val r: mut Point = p
val q: Point = p  // OK: q is never used
r.x = 5
```

---

## Phase 7: Alias Tracking — Advanced Cases

**Goal:** Extend alias tracking to cover object properties, closures,
destructuring, conditional aliasing, and method receivers.

### 7.1 Object Property Aliasing

When a value is stored into an object property (`obj.prop = value`), the
alias sets of the containing object and the value are **merged**. All
variables in `obj`'s alias set(s) become aliases of `value`, and vice versa.

**File:** `internal/liveness/alias_analysis.go`

```go
// When processing: obj.prop = value
// 1. Determine the alias source of value (using DetermineAliasSource)
// 2. If value aliases a variable v:
//    - Call AliasTracker.MergeAliasSets(obj, v) to merge their alias sets
// 3. If value is a fresh value: obj gains no new aliases
```

**Why merge, not just add?** Simple addition (`add obj to value's set`)
loses transitive connections when intermediate variables are reassigned.
This matters for iterative construction of recursive/cyclic structures:

```esc
var current: mut Node = Node(1, undefined)
val head: mut Node = current              // {head, current}
val next: mut Node = Node(2, undefined)   // {next}
current.next = next                       // merge → {head, current, next}
current = next                            // current leaves and re-joins
// head is still connected to next — connection preserved
```

With simple addition, `current.next = next` would only add `current` to
`next`'s set. When `current` is later reassigned, `head`'s connection to
`next` is severed. Merging ensures all variables that alias the containing
object also alias the stored value, maintaining transitive connections
through property chains.

**Conservatism:** Merging is conservative — the entire reachable object
graph may end up in one alias set. This is correct for cyclic structures
(you cannot freeze part of a cycle) and sound for acyclic structures
(a container that stores a reference truly does alias it).

This requires extending `DetermineAliasSource` to handle `MemberExpr` on the
left side of assignments, and to recognize when a property assignment creates
an alias relationship.

### 7.2 Closure Capture Aliasing

When a closure (function expression) captures a variable from the enclosing
scope, the closure variable is an alias of the captured variable.

**Implementation:**
1. During function expression inference (`inferFuncExpr`), identify captured
   variables by comparing the function body's free variables against the
   enclosing scope
2. For each captured variable, determine if the capture is mutable (the closure
   writes to the captured variable) or read-only (the closure only reads it)
3. Add the closure variable to the alias set of each captured variable with
   the appropriate mutability

**File:** `internal/liveness/capture_analysis.go`

```go
// CaptureInfo describes how a closure captures a variable.
type CaptureInfo struct {
    VarID     VarID
    IsMutable bool  // true if the closure writes to the captured variable
}

// AnalyzeCaptures determines which variables a closure captures and how.
func AnalyzeCaptures(funcBody ast.Block, enclosingScope map[string]VarID) []CaptureInfo { ... }
```

#### Read-Only Captures and Transition Rules

A read-only capture means the closure cannot mutate the captured variable —
but it does NOT freeze the original value. The requirements (Closures section)
specify that external mutations through the original mutable reference are
still permitted:

```esc
val items: mut Array<number> = [1, 2, 3]
val f = fn() { print(items.length) }  // read-only capture
items.push(4)                          // OK: f has read-only access, no conflict
f()                                    // observes length 4
```

For transition checking, a read-only capture creates an **immutable alias**
in the closure's alias set. This means:
- **Rule 1 (mut → immutable):** A read-only capture does NOT block the
  transition, because the capture itself is immutable — it doesn't conflict
  with other immutable references.
- **Rule 2 (immutable → mut):** A read-only capture DOES block the
  transition, because the closure holds an immutable reference and would
  observe unexpected mutations.
- A **mutable capture** blocks BOTH Rule 1 (it's a live mutable alias) and
  creates the same aliasing constraints as any other mutable alias.

This distinction is important: a read-only capture is not simply "no alias" —
it is an immutable alias that blocks immutable-to-mutable transitions on the
captured variable while the closure is live.

### 7.3 Destructuring Aliasing

When a value is destructured, each extracted binding aliases the corresponding
part of the original value:

```esc
val {a} = obj  // a aliases obj.a
```

**Implementation:** In `inferPattern` for `ObjectPat` and `ArrayPat`, when
the initializer is a `VarRef`, add each destructured binding to the
`AliasSet` for the **source variable** — i.e. `AliasSet.Members`
(`map[VarID]Mutability`) gains an entry for the new binding.

Alias sets do not track property-level granularity — they track variables,
not sub-paths. Putting `a` in `obj`'s alias set is a conservative
approximation: it means "if you freeze `obj`, `a` is a conflicting alias,"
which is correct because `a` points into `obj`'s data. This may produce
false positives (e.g. freezing an unrelated property of `obj` would flag
`a` as conflicting) but never misses a real conflict. Property-level alias
sets would be more precise but add significant complexity for little
practical benefit.

If `obj.a` is later assigned a new value (e.g. `obj.a = other`), the
Phase 7.1 merge semantics handle connecting the object-level and
property-level alias sets — the same merge that fires for any
`obj.prop = value` assignment ensures transitive connections are maintained.

### 7.4 Conditional Aliasing

When a variable is assigned from different values depending on control flow,
it joins the alias sets of all possible sources:

```esc
val c = if cond { a } else { b }
// c is in both a's and b's alias sets
```

**Implementation:** Extend `DetermineAliasSource` to handle `IfElseExpr` and
`MatchExpr` by collecting alias sources from all branches and returning
`AliasSourceMultiple`. The `AliasTracker` already supports membership in
multiple alias sets via `VarToSets` (defined in Phase 1.4, now used from Phase 5).

### 7.5 Reassignment

When a `var` variable is reassigned, it leaves its previous alias set(s):

```esc
var b = a       // b aliases a
b = {x: 1, y: 1}  // b leaves a's alias set
val q: Point = a   // OK: b no longer aliases a
```

This is already handled by `AliasTracker.Reassign` from Phase 5.

### 7.6 Method Receiver Aliasing

When a method stores a parameter into `self`, the receiver becomes an alias
of that parameter. This is handled through lifetime annotations (Phase 8) —
the method's lifetime signature captures the relationship, and the caller
updates alias sets based on the signature.

Given a method body like:

```esc
fn setItem(mut self, p: mut Point) -> void {
    self.item = p  // stores p into self
}
```

Phase 8.3's inference detects that `p` is stored into `self` and creates
a shared lifetime linking them. The inferred signature is:

```esc
fn setItem<'a>(mut 'a self, p: mut 'a Point) -> void
```

The shared `'a` on `self` and `p` tells callers that after `c.setItem(p)`,
`c` aliases `p`. The caller's alias tracker merges their alias sets.

### 7.7 Tests

- Object property: `obj.prop = p` makes obj alias p
- Closure capture (mutable): closure writing to captured var blocks Rule 1
- Closure capture (read-only): closure only reading captured var
- Read-only capture does NOT block mutation through original mutable reference
- Read-only capture DOES block immutable-to-mutable transition on captured var
- Destructuring: `val {point} = obj` aliases obj.point
- Conditional: `val c = if cond { a } else { b }` aliases both
- Reassignment: var leaving alias set on reassignment
- Combined: closure capturing variable that is later aliased through property

---

## Phase 8: Lifetime Annotations and Inference

**Goal:** Add lifetime annotations to function signatures and infer them from
function bodies.

### 8.1 Parser Changes — Lifetime Syntax

Extend the parser to recognize lifetime parameters in function declarations
and type annotations.

**File:** `internal/parser/type_ann.go`

**Syntax:** Lifetime parameters appear in angle brackets alongside type
parameters, prefixed with `'`:

```esc
fn identity<'a>(p: mut 'a Point) -> mut 'a Point { return p }
fn first<'a, 'b>(a: mut 'a Point, b: mut 'b Point) -> mut 'a Point { return a }
```

**Parsing changes:**
1. In `parseTypeParams()`, detect tokens starting with `'` as lifetime
   parameters (vs. regular type parameters)
2. In `parseTypeAnn()`, when parsing `mut` types, check for an optional
   lifetime annotation before the type name
3. Store lifetime annotations in a new `LifetimeAnn` AST node

**File:** `internal/ast/type_ann.go`

```go
// LifetimeAnn represents a lifetime in source code (e.g. 'a). Used for
// both declaration sites (in <'a, T>) and use sites (in mut 'a Point).
// The checker resolves which is which during Phase 8.3.
type LifetimeAnn struct {
    Name string  // e.g. "a" (without the tick)
    span Span
}
```

Add `LifetimeParams` to `FuncTypeAnn`:

```go
type FuncTypeAnn struct {
    LifetimeParams []*LifetimeAnn  // e.g. ['a, 'b]
    TypeParams     []*TypeParam
    // ... existing fields ...
}
```

Add optional `Lifetime` to `MutTypeAnn` (or wherever `mut` is parsed):

```go
type MutTypeAnn struct {
    Type     TypeAnn
    Lifetime *LifetimeAnn  // optional, e.g. 'a in `mut 'a Point`
    // ...
}
```

#### `immutable` Class Modifier

**File:** `internal/parser/decl.go`

Extend `classDecl` (or the declaration dispatcher that calls it) to accept
an optional `immutable` token before `class`:

```esc
immutable class Config(host: string) {
    fn setHost(mut self, h: string) { self.host = h }
}
```

**Parsing changes:**
1. Before matching `Class`, check if the current token is `Immutable`
   (a new keyword token). If so, consume it and set a local flag
   `isImmutable = true`, then expect `Class` as the next token.
2. Pass `isImmutable` through to `classDecl`.

**File:** `internal/ast/class.go`

Add an `Immutable` field to `ClassDecl`:

```go
type ClassDecl struct {
    Name       *Ident
    TypeParams []*TypeParam
    Extends    *TypeRefTypeAnn
    Params     []*Param
    Body       []ClassElem
    Immutable  bool            // true when declared with `immutable class`
    export     bool
    declare    bool
    span       Span
    provenance provenance.Provenance
}
```

**Integration with constructor lifetime inference:**

In `InferConstructorLifetimes` (Section 8.6), when determining default
mutability (step 5), consult `classDecl.Immutable`. If `true`, set
`TypeAlias.DefaultMutable = false` regardless of whether the class has
`mut self` methods.

#### Multiple Lifetimes on a Type (`('a | 'b) Point`)

When a return type may alias one of several parameters, the lifetime
annotation uses `|` syntax inside parentheses — consistent with Escalier's
union type syntax. This is the cross-function equivalent of conditional
aliasing.

**Parsing:** When the parser encounters `(` followed by lifetime tokens
separated by `|` and then `)` before a type, it parses a multi-lifetime
annotation:

```esc
fn pick<'a, 'b>(a: 'a Point, b: 'b Point, cond: boolean) -> ('a | 'b) Point
```

**AST representation:** The `Lifetime` field on `MutTypeAnn` (and on
immutable type annotations) accepts either a single `LifetimeAnn` or a
list:

```go
// LifetimeUnionAnn represents multiple lifetimes on a single type
// (e.g. ('a | 'b) in `('a | 'b) Point`).
type LifetimeUnionAnn struct {
    Lifetimes []*LifetimeAnn  // e.g. ['a, 'b]
    span      Span
}
```

Both `LifetimeAnn` and `LifetimeUnionAnn` satisfy the same interface so
they can be used interchangeably wherever a lifetime annotation appears.

**Type system representation:** In `type_system/types.go`, the `Lifetime`
field on `TypeRefType`, `ObjectType`, etc. already accepts the `Lifetime`
interface. Add a `LifetimeUnion` type:

```go
// LifetimeUnion represents a value that may carry one of several lifetimes.
// Used when a function returns one of multiple parameters depending on
// control flow.
type LifetimeUnion struct {
    Lifetimes []Lifetime  // e.g. [LifetimeVar('a), LifetimeVar('b)]
}

func (*LifetimeUnion) isLifetime() {}
```

At call sites, when a return type has a `LifetimeUnion`, the result is
added to the alias sets of **all** corresponding arguments.

### 8.2 Lexer Changes

Add a new token type for lifetime identifiers:

**File:** `internal/parser/lexer.go`

The lexer recognizes `'` followed by an identifier as a `LIFETIME` token
(e.g. `'a` → Token{Kind: LIFETIME, Value: "a"}).

This must be context-aware to avoid conflicts with character literals (if any)
or other uses of `'`. Since Escalier uses double quotes for strings and doesn't
have character literals, `'` followed by an identifier should be unambiguous.

### 8.3 Lifetime Inference from Function Bodies

When a function body is available, infer lifetime relationships by analyzing
which parameters are returned or stored:

**File:** `internal/checker/infer_lifetime.go`

```go
// InferLifetimes analyzes a function body to determine which parameters
// are aliased by the return value (or yielded value, for generators).
// Returns the inferred lifetime parameters and a map from parameter index
// to the lifetime variable linking that parameter to the return/yield type.
func (c *Checker) InferLifetimes(
    ctx Context,
    params []*type_system.FuncParam,
    body ast.Block,
    returnType type_system.Type,
) ([]*type_system.LifetimeVar, map[int]*type_system.LifetimeVar) { ... }
// The map key is the parameter index (0-based position in params).
// The map value is the LifetimeVar that links that parameter to the
// return type. Parameters not present in the map have no aliasing
// relationship with the return value.
```

**Algorithm:**
1. Walk the function body to find all `return` statements and `yield`
   expressions (including `yield from`)
2. For each return/yield expression, determine its alias source (using
   `DetermineAliasSource` from Phase 5)
3. If the expression aliases a parameter, create a lifetime variable
   linking that parameter to the return type (for `return`) or the
   generator's yield type `T` in `Generator<T, TReturn, TNext>` (for
   `yield`)
4. If different return/yield expressions alias **different** parameters,
   create a `LifetimeUnion` on the return type combining all relevant
   lifetimes. For example, `if cond { a } else { b }` where `a` has
   lifetime `'a` and `b` has lifetime `'b` produces `('a | 'b)` on the
   return type
5. If the expression calls another function with lifetime annotations,
   propagate those lifetimes
6. If the function stores a parameter into a module-level variable, assign
   `'static` to that parameter

**Note on generators:** `yield` expressions are alias sources just like
`return` statements. A `yield expr` makes the yielded value available to
the caller via `Iterator.next()`, so if `expr` aliases a parameter, the
lifetime must link that parameter to the generator's `T` type parameter.
For `yield from`, the delegated iterator's element type is propagated.
See the requirements document's "Async/Await and Generators" section for
the full rationale.

#### Determining Container-Level vs Element-Level Lifetimes

When inferring lifetimes for functions that return generic containers, the
inference must distinguish between:
- **Container-level lifetime** (`'a Array<T>`): the returned container IS the
  input — full alias (e.g. `identity` returning its argument)
- **Element-level lifetime** (`Array<'a T>`): the returned container is fresh
  but its elements alias the input (e.g. `filter` returning a new array with
  elements from the input)

**How to determine the distinction:**
1. If the return expression is directly a parameter (or an alias of a
   parameter), the lifetime goes on the **container**: `'a Array<T>`
2. If the return expression is a newly constructed container (array literal,
   `new Array(...)`, spread into a new array) whose elements are derived
   from a parameter, the lifetime goes on the **element type**: `Array<'a T>`
3. If the return expression is a call to another function, propagate that
   function's lifetime placement (container or element level)

In practice, the inference examines the return expression's structure:
- Direct return of parameter → container-level
- Array literal with elements from parameter → element-level
- Method calls like `.filter()`, `.slice()` → element-level (from the
  callee's signature)
- `.sort()`, `.reverse()` (mutate in place, return `this`) → container-level
  (from the callee's signature)

**Integration:** Call `InferLifetimes` during `inferFuncDecl` / `inferFuncExpr`
after the body has been type-checked. Attach the inferred lifetimes to the
`FuncType`.

### 8.4 Escaping Reference Detection

When a function stores a parameter into a location that outlives the function
(e.g. a module-level variable, an object property that escapes), the parameter
gets a `'static` lifetime:

**File:** `internal/checker/infer_lifetime.go`

```go
// DetectEscapingRefs walks a function body looking for parameter references
// that are stored into locations that outlive the function call.
func (c *Checker) DetectEscapingRefs(
    ctx Context,
    params []*type_system.FuncParam,
    body ast.Block,
) map[VarID]bool { ... }
```

Escaping locations include:
- Module-level variables (looked up through scope chain)
- Properties of objects that are themselves module-level
- Return values (handled by lifetime inference, not escaping detection)

### 8.5 Effect on Callers — Alias Set Updates

When a function with lifetime annotations is called, the caller's alias
tracker must be updated:

**File:** `internal/checker/infer_expr.go` (in call expression handling)

After type-checking a call expression:
1. Look up the callee's `FuncType` and its `LifetimeParams`
2. For each lifetime parameter that links a parameter to the return type,
   add the call result variable to the alias set of the argument variable
3. If the return type has a `LifetimeUnion` (e.g. `('a | 'b)`), add the
   call result variable to the alias sets of **all** corresponding
   arguments — the cross-function equivalent of conditional aliasing
4. For `'static` parameters, mark the argument as permanently aliased

```go
// updateAliasesFromCall updates the caller's alias tracker based on the
// callee's lifetime annotations.
func (c *Checker) updateAliasesFromCall(
    ctx Context,
    callExpr *ast.CallExpr,
    funcType *type_system.FuncType,
    argVarIDs []VarID,
    resultVarID VarID,
) { ... }
```

### 8.6 Constructor Lifetime Inference

Constructors require special handling because unlike functions, they do not
have explicit `return` statements — they produce objects from parameters. The
requirements specify that when a constructor parameter is stored as a field,
the constructed object aliases that parameter.

A constructor always produces a **fresh value** — the constructed object
itself is never an alias of an existing value. The only aliasing
relationships come from reference-typed parameters that are stored as
fields. These are represented as lifetime parameters on the class type
(e.g. `Container<'a>`), not as a prefix lifetime on the instance (e.g.
NOT `'a Container`). The prefix form would mean "this value is an alias
of something," which does not apply to constructor results.

**Differences from function lifetime inference:**
- The lifetimes become **generic parameters of the class itself** (e.g.
  `Container<'a>`, `Pair<'a, 'b>`), similar to how Rust handles struct
  lifetimes. The constructed value's type carries these lifetime parameters
  so they can propagate through function signatures. For a single reference
  parameter the constructed type is `Container<'a>`; for multiple reference
  parameters it is `Pair<'a, 'b>` where each lifetime tracks a different
  parameter's alias relationship.
- Constructors do not have a receiver (`self`), so elision rule 3 (method
  receiver) does not apply
- Primitive or value-type parameters do not need lifetimes

**File:** `internal/checker/infer_lifetime.go`

```go
// InferConstructorLifetimes analyzes a class definition to determine
// which constructor parameters are stored as fields, creating alias
// relationships between the constructed object and the parameters.
func (c *Checker) InferConstructorLifetimes(
    ctx Context,
    classDecl *ast.ClassDecl,
) ([]*type_system.LifetimeVar, map[int]*type_system.LifetimeVar) { ... }
// The map key is the parameter index. The map value is the lifetime
// variable linking that parameter to the constructed type.
```

**Algorithm:**
1. For each constructor parameter, check if it is stored as a field in the
   class body
2. If the parameter is a reference type (not a primitive) and is stored as a
   field, create a lifetime variable linking it to the constructed type
3. If the parameter is a primitive (`number`, `string`, `boolean`) or a
   fresh value, no lifetime is needed
4. Multiple reference parameters each get their own lifetime variable
5. Determine the **default mutability** of the constructed instance (see
   the Default Mutability section in the requirements):
   - If the class has the `immutable` modifier → immutable
   - Else if the class has any `mut self` methods → mutable
   - Else → immutable
   Store this on the class's `TypeAlias` so that call sites can apply the
   correct default when no explicit `mut` annotation is present

**Example — single reference parameter:**
```esc
class Container(item: mut Point) { item, }
// Inferred: Container<'a>(item: mut 'a Point) → Container<'a>
```

**Example — multiple reference parameters:**
```esc
class Pair(first: mut Point, second: mut Point) { first, second, }
// Inferred: Pair<'a, 'b>(first: mut 'a Point, second: mut 'b Point)
// The constructed type is Pair<'a, 'b> — callers know which parameters
// are aliased:
//   val pair: Pair<'a, 'b> = Pair(a, b)
//   // 'a bound to a's lifetime, 'b bound to b's lifetime
//   // pair is in the alias sets of both a and b
```

**Example — type parameter and lifetime parameter together:**
```esc
class Container<T>(item: mut T) { item, }
// Inferred: Container<'a, T>(item: mut 'a T) → Container<'a, T>
// LifetimeParams: ['a], TypeParams: [T]

val p: mut Point = {x: 0, y: 0}
val c = Container(p)   // c has type Container<'a, Point> where 'a = lifetime of p
```

**Example — primitive parameters (no lifetime):**
```esc
class Point(x: number, y: number) { x, y, }
// No lifetimes — x and y are primitives
```

**Effect on callers:** When a constructor with lifetime annotations is called,
the caller's alias tracker is updated the same way as for function calls
(Phase 8.5). The constructed object is added to the alias set of each
reference-typed argument whose corresponding parameter has a lifetime.

**Elision for constructors:** If a constructor has a single reference
parameter, elision rule 1 applies — the constructed type's lifetime is
assumed to match the input. For constructors with multiple reference
parameters, explicit annotation is required (or inference determines the
lifetimes from the class definition, which is the common case since
constructors always have bodies).

### 8.7 Recursive and Mutually Recursive Functions

For recursive functions, lifetime inference works the same as for non-recursive
functions — the relationship between parameters and return values is determined
by the function's structure.

For mutually recursive functions, use fixed-point iteration:
1. Start by assuming no lifetimes for all functions in the cycle
2. Analyze each function body using current assumptions
3. Update lifetimes based on the analysis
4. Repeat until stable

**Integration:** The existing dependency graph (`internal/dep_graph/`) already
identifies mutual recursion groups. Extend it to iterate lifetime inference
within each group.

### 8.8 Tests

**Inference from body:**
```esc
fn identity(p: mut Point) -> mut Point { return p }
// Inferred: fn identity<'a>(p: mut 'a Point) -> mut 'a Point

fn clone(p: Point) -> mut Point { return {x: p.x, y: p.y} }
// Inferred: no lifetime (returns fresh value)

fn sum(items: Array<number>) -> number { ... }
// Inferred: no lifetime (returns primitive)
```

**Escaping reference:**
```esc
var cache: Array<number> = []
fn cacheItems(items: Array<number>) -> number {
    cache = items
    return items.length
}
// Inferred: fn cacheItems(items: 'static Array<number>) -> number
```

**Effect on callers:**
```esc
val p: mut Point = {x: 0, y: 0}
val r: mut Point = identity(p)  // r aliases p
val q: Point = p                // ERROR: r is a live mutable alias
r.x = 5
print(q)
```

**Explicit annotation parsing:**
```esc
fn first<'a, 'b>(a: mut 'a Point, b: mut 'b Point) -> mut 'a Point { return a }
```

**Constructor lifetime inference:**
```esc
class Container(item: mut Point) { item, }
// Inferred: Container<'a>(item: mut 'a Point) → Container<'a>

val p: mut Point = {x: 0, y: 0}
val c = Container(p)   // c aliases p
val q: Point = p        // ERROR: c is live and provides mutable access to p
c.item.x = 5
print(q)

class Point(x: number, y: number) { x, y, }
// No lifetimes — primitives
```

**Container-level vs element-level lifetime:**
```esc
fn identity(items: mut Array<number>) -> mut Array<number> { return items }
// Inferred: 'a on the array — container-level

fn filter(items: Array<Point>, f: fn(Point) -> boolean) -> Array<Point> {
    // ... returns new array with elements from items ...
}
// Inferred: 'a on the element type — element-level: Array<'a Point>
```

**Multiple lifetimes on return type (conditional return):**
```esc
fn pick(a: Point, b: Point, cond: boolean) -> Point {
    if cond { a } else { b }
}
// Inferred: fn pick<'a, 'b>(a: 'a Point, b: 'b Point, cond: boolean) -> ('a | 'b) Point

val x: mut Point = {x: 0, y: 0}
val y: mut Point = {x: 1, y: 1}
val result = pick(x, y, true)  // result aliases both x and y
val frozen: Point = x           // ERROR: result is a live mutable alias of x
```

**Explicit multiple-lifetime annotation:**
```esc
// For body-less declarations (interfaces, external functions):
interface Selector {
    fn select<'a, 'b>(self, a: 'a Point, b: 'b Point) -> ('a | 'b) Point
}
```

**Generator lifetime inference:**
```esc
fn items(arr: Array<number>) -> Generator<number, void, never> {
    for val item in arr {
        yield item    // yielded value aliases arr's elements
    }
}
// Inferred: fn items<'a>(arr: 'a Array<number>) -> Generator<'a number, void, never>
```

**Default mutability:**
```esc
// Class with no mut self methods → immutable by default
class Point(x: number, y: number) { x, y, }
val p = Point(1, 2)              // type: Point (immutable)

// Class with mut self methods → mutable by default
class Counter(var count: number) {
    count,
    fn increment(mut self) -> void { self.count = self.count + 1 }
}
val c = Counter(0)               // type: mut Counter (mutable)

// immutable modifier overrides default
immutable class Config(host: string) {
    host,
    fn setHost(mut self, h: string) -> void { self.host = h }
}
val cfg = Config("localhost")    // type: Config (immutable despite setHost)
```

---

## Phase 9: Lifetime Unification

**Goal:** Integrate lifetime variables into the type unification engine so
that lifetimes are propagated through type inference.

### 9.1 Lifetime Variable Binding

During unification, when a type with a lifetime annotation is unified with
a type without one (or with a different lifetime), the lifetimes must be
reconciled:

**File:** `internal/checker/unify.go`

Add cases to the unification engine:

1. **Type with lifetime vs. type without:** Unification succeeds; the lifetime
   is propagated to the result. This is the common case when a function return
   type with a lifetime is assigned to a variable.

2. **Two types with lifetimes:** Unification succeeds if the lifetimes can be
   unified (same `LifetimeVar`, or one is free and gets bound).

3. **Lifetime on `MutabilityType`:** When unifying `mut 'a T` with `mut 'b T`,
   both the types and the lifetimes must match (invariant for mutable types).

### 9.2 Lifetime Instantiation at Call Sites

When a generic function with lifetime parameters is called, the lifetimes are
instantiated similarly to type parameters:

**File:** `internal/checker/infer_expr.go`

In `instantiateGenericFunc`, also instantiate lifetime parameters:
1. Create fresh `LifetimeValue` instances for each `LifetimeVar` in the signature
2. Substitute lifetime variables in parameter and return types
3. After argument unification, the lifetime values are bound to the alias sets
   of the actual arguments

### 9.3 Lifetime Constraint Propagation

When two lifetime variables are unified:
- **Binding:** Free lifetime variable is bound to a concrete `LifetimeValue`
- **Equating:** Two free lifetime variables are linked (binding one binds both)
- **Conflict:** A bound lifetime is unified with an incompatible one → error

This mirrors how type variable binding works in `Prune()` and unification.

**File:** `internal/checker/unify.go`

```go
// unifyLifetimes reconciles two lifetime annotations during unification.
func (c *Checker) unifyLifetimes(
    ctx Context,
    l1, l2 Lifetime,
) []Error { ... }
```

#### Detecting Conflicts for Independent Values

Each fresh value created at a program point gets a unique `LifetimeValue`
(from Phase 1.1). When a function signature uses a shared lifetime variable
for multiple parameters (e.g. `fn swap<'a>(p: mut 'a Point, q: mut 'a Point)`),
the unification process detects conflicts:

1. The first argument binds `'a` to the `LifetimeValue` of `p`
2. The second argument tries to bind `'a` to the `LifetimeValue` of `q`
3. If `p` and `q` have different `LifetimeValue`s (they are independent
   values), unification detects a **conflict** — `'a` is already bound to
   `p`'s lifetime and cannot also be `q`'s lifetime
4. The error message explains that the function requires both arguments to
   alias the same value

If the caller passes two aliases of the same value (e.g. `p` and `r` where
`r` aliases `p`), both have the same `LifetimeValue` (or are in the same
alias set), and unification succeeds.

#### `'static` Lifetime in Unification

The `'static` lifetime (assigned to parameters that escape via Phase 8.4)
interacts with unification as follows:

- **`'static` vs free `LifetimeVar`:** The variable is bound to `'static`.
  This propagates the "permanently aliased" constraint to the caller.
- **`'static` vs concrete `LifetimeValue`:** Unification succeeds — the
  concrete value is subsumed by `'static`. The caller must treat the value
  as permanently aliased after the call.
- **`'static` vs `'static`:** Trivially succeeds.
- **Bound `LifetimeVar` (non-static) vs `'static`:** The binding is
  upgraded to `'static`. If a lifetime is discovered to be `'static` through
  one parameter, all other occurrences of that lifetime variable inherit
  the `'static` constraint.

In the alias tracker, a value with `'static` lifetime is marked as
permanently aliased — it can never undergo a mutability transition. The
`AliasSet` can track this via an `IsStatic bool` field.

### 9.4 Lifetime on Generic Type Parameters

When lifetimes appear on type parameters (e.g. `Array<'a T>`), unification
recurses into the type arguments and propagates lifetimes:

- `Array<'a T>` vs `Array<T>`: Succeeds; lifetime propagated to the result
- `Array<'a T>` vs `Array<'b T>`: Succeeds if `'a` and `'b` unify
- `mut Array<'a T>` vs `mut Array<'b T>`: Same check — `'a` and `'b` must
  unify. (Noted as "invariant" because mutable types require exact match.
  In practice this is the same as the immutable case since Escalier does
  not have lifetime subtyping. The distinction would matter if an
  "outlives" relationship like Rust's `'a: 'b` were added in the future.)

### 9.5 Function Type Unification with Lifetimes

When unifying function types:
- Parameter types are contravariant in lifetimes
- Return types are covariant in lifetimes

#### Higher-Order Function Lifetime Threading

When a higher-order function takes a callback with lifetime annotations, the
lifetimes must be threaded through the callback to the enclosing function's
return type. This requires unification to propagate lifetime bindings through
function type arguments.

**Example — callback that aliases its argument:**
```esc
fn identity(p: mut Point) -> mut Point { return p }
// Inferred: fn identity<'b>(p: mut 'b Point) -> mut 'b Point

fn apply<'a>(f: fn(mut 'a Point) -> mut 'a Point, p: mut 'a Point) -> mut 'a Point {
    return f(p)
}
```

```esc
val myPoint: mut Point = {x: 0, y: 0}
val result = apply(identity, myPoint)
```

When `apply(identity, myPoint)` is called:
1. Unify `identity`'s type `fn(mut 'b Point) -> mut 'b Point` with the
   expected callback type `fn(mut 'a Point) -> mut 'a Point`. This recurses
   into the parameter types (`mut 'b Point` vs `mut 'a Point`) and return
   types (`mut 'b Point` vs `mut 'a Point`), which equates `'b` with `'a`
   — binding one now binds the other
2. Unify `myPoint` with the second parameter `p: mut 'a Point`. This binds
   `'a` to `myPoint`'s concrete `LifetimeValue`. Since `'b` is equated
   with `'a`, `'b` is also bound to `myPoint`'s lifetime
3. The return type `mut 'a Point` inherits `myPoint`'s lifetime, so the
   result aliases `myPoint`

**Example — callback that does NOT alias its argument:**
```esc
fn transform<'a>(f: fn(mut Point) -> mut Point, p: mut 'a Point) -> mut Point {
    return f(p)
}
```

Here `f`'s parameter and return type have no shared lifetime. When unifying
the callback argument with the expected type, no lifetime constraint links
the callback's input to its output. The return type of `transform` has no
lifetime linking it to `p`, so the result is independent.

**Implementation:** During unification of `fn(A1) -> R1` with `fn(A2) -> R2`:
1. Unify parameter types (contravariant): `A2` with `A1`
2. Unify return types (covariant): `R1` with `R2`
3. Any lifetime variables that appear in both parameter and return positions
   of the callback type create constraints linking them
4. These constraints propagate through to the enclosing function's lifetime
   variables via the standard binding/equating mechanism

### 9.6 Tests

- Lifetime binding at call site
- Two calls with same lifetime parameter resolve consistently
- Conflicting lifetime bindings produce errors (e.g. `swap<'a>(p, q)` where
  `p` and `q` are independent values)
- Same-lifetime bindings succeed for aliases (e.g. `swap<'a>(p, r)` where
  `r` aliases `p`)
- `'static` lifetime propagation through unification
- `'static` blocks mutability transitions at call sites
- Lifetime propagation through generic type parameters
- Function type unification with lifetime variance
- Higher-order function: callback with shared lifetime threads through
- Higher-order function: callback without lifetime produces independent result

---

## Phase 10: Lifetime Elision Rules

**Goal:** Apply default lifetime rules to body-less declarations so that
common cases don't require explicit annotations.

### 10.1 Elision Rule Implementation

Elision rules apply only to body-less declarations (interface methods, external
functions, imported TypeScript types):

**File:** `internal/checker/elision.go`

```go
// ApplyLifetimeElision applies default lifetime rules to a function signature
// that has no body and no explicit lifetime annotations.
func (c *Checker) ApplyLifetimeElision(funcType *type_system.FuncType) { ... }
```

**Rules:**
1. **Single reference parameter:** If the declaration has exactly one
   reference-typed parameter and returns a reference type, the output lifetime
   matches the input
2. **No reference return:** If the return type is primitive/void, no lifetimes
   needed
3. **Method receiver:** For methods, the return type defaults to the receiver's
   lifetime

When elision is ambiguous (multiple reference parameters, reference return type),
the compiler requires explicit annotation and reports an error.

### 10.2 Determining "Reference Type"

A type is a "reference type" for elision purposes if it can alias:
- Object types
- Array types
- Function types
- Type references that resolve to object/array/function types
- **Unresolved type parameters** (e.g. `T` in `declare fn wrap<T>(value: T) -> {inner: T}`)
  — conservatively treated as reference types, since `T` could be instantiated
  with a reference type at the call site. If the caller instantiates `T` with
  a primitive, the lifetime is harmless (primitives can't alias). A type
  parameter with a primitive constraint (e.g. `T extends number`) can be
  treated as non-reference.
- NOT: primitives (`number`, `string`, `boolean`), `void`, `null`, `undefined`

**File:** `internal/checker/elision.go`

```go
// IsReferenceType returns true if the type can participate in aliasing.
func IsReferenceType(t type_system.Type) bool { ... }
```

### 10.3 Interface Method Lifetime Verification

When a type implements an interface, its method's inferred lifetimes must be
**compatible** with the interface's declared lifetimes. The requirements
specify that an implementation may be *more conservative* than the interface
(e.g. returning a fresh value when the interface says it may alias) but not
less conservative.

**File:** `internal/checker/check_interface.go`

```go
// VerifyLifetimeCompatibility checks that an implementation's inferred
// lifetimes are compatible with the interface's declared lifetimes.
// An implementation is compatible if it aliases no MORE than the interface
// declares — it may alias less (more conservative).
func (c *Checker) VerifyLifetimeCompatibility(
    ifaceMethod *type_system.FuncType,
    implMethod *type_system.FuncType,
) []Error { ... }
```

**Compatibility rules:**
- If the interface declares `'a` linking a parameter to the return type, the
  implementation may either: (a) also return an alias of that parameter
  (matching `'a`), or (b) return a fresh value (more conservative — safe)
- If the interface declares no lifetime on a parameter, the implementation
  must NOT return an alias of that parameter (it would violate the caller's
  assumption that the return value is independent)
- Lifetime count and parameter positions must match between interface and
  implementation

**Example:**
```esc
interface Transform {
    fn apply<'a>(self, p: mut 'a Point) -> mut 'a Point
}

// Future syntax (once `implements` is supported):
class Cloner() implements Transform {
    fn apply(self, p: mut Point) -> mut Point {
        return {x: p.x, y: p.y}  // OK: more conservative (fresh value)
    }
}

class Storer(var stored: mut Point) implements Transform {
    stored,
    fn apply(self, p: mut Point) -> mut Point {
        self.stored = p
        return self.stored  // ERROR: returns alias of self, not p — violates 'a
    }
}
```

### 10.4 Integration

Apply elision during:
- Interface method declaration processing (`inferInterface`)
- External function declaration processing
- TypeScript type import processing (Phase 11)

Apply lifetime verification during:
- Interface implementation checking (when a type declares it implements an
  interface)

### 10.5 Tests

- Single ref param + ref return → lifetime inferred
- Multiple ref params + ref return → error requiring annotation
- Primitive return → no lifetime regardless of params
- Method receiver → return defaults to receiver's lifetime
- Void return → no lifetime
- Already-annotated declaration → elision rules not applied
- Implementation returning fresh value for aliased interface method → OK
- Implementation returning wrong alias for interface method → error
- Implementation aliasing when interface declares no alias → error

---

## Phase 11: TypeScript Interop

**Goal:** Automatically assign lifetime annotations to TypeScript type
declarations imported into Escalier.

### 11.1 Automatic Lifetime Assignment

When importing TypeScript declarations, apply the heuristic rules from the
requirements (Rules A–F):

**File:** `internal/interop/lifetime_assign.go`

```go
// AssignLifetimes applies heuristic lifetime rules to an imported
// TypeScript function signature.
func AssignLifetimes(funcType *type_system.FuncType) { ... }
```

**Rules implemented:**
- **Rule A:** Primitive/void return → no lifetime
- **Rule B:** Return type matches a parameter type → assume aliasing
- **Rule C:** Return type differs from all parameters → no lifetime
- **Rule D:** Methods returning `this` → alias with receiver
- **Rule E:** Methods returning new collections → no container-level lifetime,
  element-level lifetime where appropriate
- **Rule F:** Callback parameters → determine mutability from callback's
  parameter type

#### Default Mutability for Imported Classes

When importing a TypeScript class, determine `DefaultMutable` for its
`TypeAlias` by inspecting the class declaration for mutating methods:

- If any method has a non-`readonly` `this` parameter, or modifies
  properties on `this` → `DefaultMutable = true`
- If all methods are read-only → `DefaultMutable = false`
- Built-in types (`Map`, `Set`, `Array`, `WeakMap`, `WeakSet`) are
  hardcoded as `DefaultMutable = true` in the built-in overrides
  (Phase 11.3)

Since TypeScript declarations don't have an `immutable` modifier,
overrides (Phase 11.2) can set `DefaultMutable = false` for imported
classes that should default to immutable despite having mutating methods.

### 11.2 Override Mechanism

Support manual override files for correcting heuristic lifetime assignments:

**File:** `internal/interop/lifetime_overrides.go`

Override files (`.esc.d.ts` or a dedicated format) allow developers to provide
explicit lifetime annotations for specific imported functions. The overrides
are loaded during package initialization and take precedence over heuristic
rules.

**Format:** TBD — could be a subset of Escalier syntax with explicit lifetime
annotations, or a JSON/TOML configuration file.

### 11.3 Built-in Overrides

Ship overrides for common TypeScript APIs (Array methods, Promise, etc.)
as part of the standard library. These are the overrides listed in the
requirements document (Array.forEach, Array.map, Array.filter, etc.).

**File:** `internal/checker/prelude_lifetimes.go`

In addition to the Array overrides listed in the requirements, provide
overrides for Map, WeakMap, Set, and WeakSet:

```esc
// Map — get returns an element alias, set returns the map (receiver alias)
declare fn Map.get<'a, K, V>(self: 'a Map<K, V>, key: K) -> 'a V | undefined
declare fn Map.set<'self, K, V>(self: mut 'self Map<K, V>, key: K, value: V) -> mut 'self Map<K, V>
declare fn Map.has<K, V>(self: Map<K, V>, key: K) -> boolean
declare fn Map.delete<K, V>(self: mut Map<K, V>, key: K) -> boolean
declare fn Map.forEach<'a, K, V>(self: 'a Map<K, V>, f: fn('a V, K) -> void) -> void

// WeakMap — keys must be objects, get returns an element alias
declare fn WeakMap.get<'a, K, V>(self: 'a WeakMap<K, V>, key: K) -> 'a V | undefined
declare fn WeakMap.set<'self, K, V>(self: mut 'self WeakMap<K, V>, key: K, value: V) -> mut 'self WeakMap<K, V>
declare fn WeakMap.has<K, V>(self: WeakMap<K, V>, key: K) -> boolean
declare fn WeakMap.delete<K, V>(self: mut WeakMap<K, V>, key: K) -> boolean

// Set — values returns an iterator with element aliases
declare fn Set.has<T>(self: Set<T>, value: T) -> boolean
declare fn Set.add<'self, T>(self: mut 'self Set<T>, value: T) -> mut 'self Set<T>
declare fn Set.delete<T>(self: mut Set<T>, value: T) -> boolean
declare fn Set.forEach<'a, T>(self: 'a Set<T>, f: fn('a T) -> void) -> void

// WeakSet — keys must be objects
declare fn WeakSet.has<T>(self: WeakSet<T>, value: T) -> boolean
declare fn WeakSet.add<'self, T>(self: mut 'self WeakSet<T>, value: T) -> mut 'self WeakSet<T>
declare fn WeakSet.delete<T>(self: mut WeakSet<T>, value: T) -> boolean
```

### 11.4 Tests

- `Array.prototype.sort` → aliases receiver
- `Array.prototype.map` → fresh array, no container alias
- `Array.prototype.filter` → fresh array, element alias
- `Array.prototype.find` → element alias
- `Object.keys` → no alias
- `Map.get` → element alias
- `Map.set` → aliases receiver
- `Set.add` → aliases receiver
- `Set.forEach` → callback receives element alias
- Callback with readonly parameter → immutable
- Override file correctly replaces heuristic assignment

---

## Phase 12: Error Messages

**Goal:** Produce clear, actionable error messages that show lifetime
information only when it helps the developer.

### 12.1 Error Types

Define error types for lifetime violations:

**File:** `internal/checker/error.go`

```go
// LiveMutableAliasError is reported when a mutable-to-immutable transition
// is attempted while live mutable aliases exist.
type LiveMutableAliasError struct {
    // The variable being transitioned
    SourceVar    string
    SourceSpan   ast.Span
    // The immutable binding
    TargetVar    string
    TargetSpan   ast.Span
    // The conflicting live mutable alias
    AliasVar     string
    AliasUseSpan ast.Span  // where the alias is used after the transition
    // If the alias was created through a function call, the function's
    // lifetime annotation that created the relationship
    AliasOrigin  *AliasOrigin
}

// LiveImmutableAliasError is reported when an immutable-to-mutable transition
// is attempted while live immutable aliases exist.
type LiveImmutableAliasError struct {
    // Similar fields to LiveMutableAliasError
    ...
}

// EscapingReferenceError is reported when a function captures a reference
// ('static lifetime) and the caller tries to use the value mutably afterward.
type EscapingReferenceError struct {
    ...
}

// AliasOrigin describes how an alias was created (for error messages).
type AliasOrigin struct {
    FuncName     string
    LifetimeVar  string      // e.g. "'a"
    Kind         string      // "return value aliases parameter", etc.
}
```

### 12.2 Error Formatting

Error messages follow the format shown in the requirements:

```text
error: cannot assign mutable value to immutable variable
  --> src/main.esc:4:20
   |
 2 | val p: mut Point = {x: 0, y: 0}
   |     - mutable reference created here
 3 | val r: mut Point = identity(p)
   |     - aliases `p` (identity returns its argument)
 4 | val q: Point = p
   |              ^^^^^ immutable binding here
 5 | r.x = 5
   |     ^^^ mutable alias `r` is still used here
   |
   = help: ensure all mutable aliases of `p` are dead before the immutable binding
```

For simple cases (no cross-function aliasing), omit lifetime details. For
cross-function cases, show the function signature with lifetimes highlighted.

### 12.3 Tests

- Simple local alias error message
- Cross-function alias error message with lifetime shown
- Escaping reference error message
- Multiple conflicting aliases shown in one error
- Help text suggests concrete fix

---

## Phase 13: Remove `mut?`

**Goal:** Remove `MutabilityUncertain` from the type system, completing the
transition to liveness-based mutability.

### 13.1 Audit All Uses of `MutabilityUncertain`

The `MutabilityUncertain` variant is used across the codebase. Each use must
be replaced. The table below lists known files — run
`grep -r MutabilityUncertain internal/` to produce the complete list before
starting this phase:

| File | Current Use | Replacement |
|------|-------------|-------------|
| `internal/type_system/types.go` | `MutabilityUncertain` constant | Remove |
| `internal/type_system/print_type.go` | Printing `mut?` | Remove case |
| `internal/checker/unify.go` | Special handling during unification | Remove |
| `internal/checker/unify_mut.go` | Invariant checking with `mut?` | Simplify |
| `internal/checker/infer_expr.go` | Creating `mut?` types | Use `mut` or immutable directly |
| `internal/checker/infer_func.go` | `mut?` in parameter inference | Binary inference (mut/immutable) |
| `internal/checker/infer_module.go` | `mut?` propagation | Remove |
| `internal/checker/generalize.go` | Stripping `mut?` during generalization | Remove stripping |
| `internal/checker/expand_type.go` | `mut?` in type expansion | Remove case |
| `internal/checker/iterable.go` | `mut?` on iterable types | Remove |
| `internal/codegen/dts.go` | `mut?` in .d.ts output | Remove |
| *(remaining files)* | Grep for complete list at implementation time | Case-by-case |

### 13.2 Remove `MutabilityType` Wrapper

Rather than keeping `MutabilityType` as a wrapper type (even a simplified
one), move mutability tracking into the type structs directly — the same
approach used for lifetimes (Phase 1.2). This eliminates the constant
wrapping/unwrapping of `MutabilityType` throughout the codebase (~110
occurrences across 14 files).

Add a `Mutable bool` field to each type struct that can be mutable:

```go
// Before: mut is a wrapper
//   &MutabilityType{Type: &TypeRefType{...}, Mutability: MutabilityMutable}

// After: mut is a field on the type itself
type TypeRefType struct {
    // ... existing fields ...
    Mutable  bool      // true = mut, false = immutable
    Lifetime Lifetime  // from Phase 1.2
}

type ObjectType struct {
    // ... existing fields ...
    Mutable  bool
    Lifetime Lifetime
}

type ArrayType struct {
    // ... existing fields ...
    Mutable  bool
    Lifetime Lifetime
}

type TupleType struct {
    // ... existing fields ...
    Mutable  bool
    Lifetime Lifetime
}

type FuncType struct {
    // ... existing fields ...
    Mutable  bool  // for mutable function references
}
```

Types that do NOT need a `Mutable` field:
- `PrimitiveType` — primitives are always immutable values
- `LiteralType` — literals are always immutable
- `VoidType`, `NullType`, `UndefinedType` — cannot be mutable
- `UnionType`, `IntersectionType` — mutability is on the member types

**Benefits:**
- No more wrapping/unwrapping — code that checks mutability reads
  `t.Mutable` directly instead of type-asserting to `*MutabilityType`
  and then unwrapping `.Type`
- Mutability and lifetime are co-located on the same struct, which
  reflects that they are both properties of a value, not separate layers
- The `Mutability` enum, `MutabilityType` struct, and all associated
  visitor methods (`EnterType`/`ExitType` for `MutabilityType`) can be
  removed
- `GetLifetime` (Phase 1.2) no longer needs to unwrap through
  `MutabilityType` to find the inner type's lifetime

**Migration:** This is a large mechanical refactor touching ~110 call
sites. The changes are straightforward but numerous:
- Every `&MutabilityType{Type: t, Mutability: MutabilityMutable}` becomes
  setting `t.Mutable = true`
- Every `case *MutabilityType:` pattern match becomes checking `t.Mutable`
  on the underlying type
- Every `unwrapMutability(t)` or similar helper is removed
- The `TypeVisitor` interface loses the `MutabilityType` enter/exit methods
- `PrintType` checks `t.Mutable` instead of matching `*MutabilityType`

Remove the `Mutability` type and `MutabilityType` struct entirely:

```go
// Remove:
// type Mutability string
// const MutabilityMutable Mutability = "!"
// const MutabilityUncertain Mutability = "?"
// type MutabilityType struct { ... }
```

### 13.3 Replace `mut?` Inference with Direct Tracking

Where the checker currently creates `MutabilityUncertain` types, replace with
direct write-tracking and default mutability rules:

**Function parameters:**
- If a parameter is written to in the function body → `mut`
- If a parameter is only read → immutable
- No intermediate `mut?` state

**Variable declarations without explicit type annotations:**
- Literal initializer (`{x: 1}`, `[1, 2]`, `(1, "a")`) → immutable,
  unless the object literal has `mut self` methods → mutable
- Constructor call → apply the class's `DefaultMutable` (from Phase 8.6):
  immutable unless the class has `mut self` methods and no `immutable`
  modifier
- Function call → determined by the function's return type
- Variable reference → inherits the source's mutability

### 13.4 Update Snapshot Tests

Many snapshot tests will change since `mut?` will no longer appear in type
output. Run all tests with `UPDATE_SNAPS=true` to update snapshots, then
review the diffs to ensure correctness.

### 13.5 Tests

- All existing mutation tests should still pass (behavior preserved)
- Types no longer show `mut?` in any output
- Object/array/tuple literals default to immutable when unannotated
- Class instances default to immutable when class has no `mut self` methods
- Class instances default to mutable when class has `mut self` methods
- `immutable` modifier on a class overrides default to immutable
- Explicit `mut` annotation overrides any default
- `MutabilityUncertain` constant is removed (compile error if referenced)

---

## Phase 14: PrintType and Display

**Goal:** Update type printing to support lifetime display with configurable
visibility.

### 14.1 Default: Hidden Lifetimes

By default, `PrintType` does not show lifetime annotations:

```go
// fn identity(p: mut Point) -> mut Point
```

### 14.2 Verbose Mode: Show Lifetimes

Add a `ShowLifetimes` option to `PrintConfig`:

**File:** `internal/type_system/print_type.go`

```go
type PrintConfig struct {
    // ... existing fields ...
    ShowLifetimes bool  // when true, print lifetime annotations
}
```

When `ShowLifetimes` is true:

```go
// fn identity<'a>(p: mut 'a Point) -> mut 'a Point
```

### 14.3 Error Context: Show Relevant Lifetimes

In error messages (Phase 12), show lifetimes only when they are relevant to
understanding the error. This uses a targeted print mode that shows lifetimes
for specific functions referenced in the error, not globally.

### 14.4 Tests

- Default printing omits lifetimes
- Verbose mode includes lifetimes
- Lifetime syntax renders correctly for various cases:
  - `mut 'a Point`
  - `'a Array<T>`
  - `Array<'a T>`
  - `fn<'a>(p: mut 'a Point) -> mut 'a Point`

---

## Cross-Cutting Concerns

### Testing Strategy

Each phase has its own tests, but integration tests should verify end-to-end
behavior:

1. **Unit tests:** Per-phase, testing individual components (alias tracker,
   liveness analysis, CFG construction, etc.)
2. **Checker tests:** Full type-checking tests using `.esc` source code,
   verifying that correct programs pass and incorrect programs produce the
   expected errors
3. **Snapshot tests:** Updated snapshots for type output (no more `mut?`)
4. **Regression tests:** All existing tests continue to pass throughout

### Performance Considerations

- **Liveness analysis** is linear in the size of the function body (one
  backward pass per basic block, fixed-point iteration for loops)
- **Alias tracking** is O(n) per function body where n is the number of
  statements
- **Lifetime inference** is O(n) per function body (one pass to find returns)
- **Fixed-point iteration** for mutual recursion typically converges in 2-3
  iterations

The overall cost is proportional to function body size, which is bounded by
typical code. No exponential blowup is expected.

### LSP Integration (Out of Scope — Follow-On Work)

The language server (`cmd/lsp-server/`) will need updates after the core
implementation is stable. These are explicitly **not part of Phases 1–14**:

- Show lifetime information in hover tooltips (when verbose mode is enabled)
- Report lifetime errors as diagnostics (will work automatically once the
  checker produces lifetime errors, since the LSP already reports checker
  errors)
- Quick-fix suggestions for common lifetime errors — potential quick-fixes
  include:
  - "Move this binding after the last mutable use" (for Rule 1 violations)
  - "Clone this value to avoid aliasing" (for cross-function alias errors)
  - "Remove the use of `x` after the transition" (for simple reordering)

  These require integrating with the LSP's code action system and are
  deferred until the error types (Phase 12) are finalized.

---

## Implementation Order and Dependencies

```text
1. Data structures ✅
└── 2. Name resolution & VarID assignment ✅
    └── 3. Liveness (linear) ✅
        ├── 4. Liveness (control flow) ✅
        │   └── 6. Transition checking ←── (also depends on 5) ✅
        └── 5. Alias tracking (local) ✅
            ├── 6. Transition checking ✅
            │   └── 7. Advanced alias tracking (properties, closures, destructuring)
            │       └── 8. Lifetime annotations, inference, & constructors
            │           └── 9. Lifetime unification ('static, conflict detection,
            │               │  higher-order function threading)
            │               └── 10. Elision rules & interface lifetime verification
            │                   └── 11. TypeScript interop
            └── 7. Advanced alias tracking
12. Error messages ←── (depends on 6–11)
└── 13. Remove mut?
    └── 14. PrintType & display
```

Phases 3–5 can be developed in parallel (once Phase 2 is complete).
Phases 8–11 can also be partially parallelized (elision and TS interop
depend on the annotation infrastructure but not on each other).

**Key sub-phase dependencies within phases:**
- Phase 8.6 (constructors) depends on 8.3 (lifetime inference from bodies)
- Phase 9.3 (`'static` and conflict detection) depends on 9.1 (lifetime
  binding) and 9.2 (instantiation)
- Phase 10.3 (interface lifetime verification) depends on 8.3 (inference)
  and 9.1 (binding)
