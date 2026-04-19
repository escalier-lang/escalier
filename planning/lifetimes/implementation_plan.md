# Lifetime Implementation Plan

This plan describes how to add liveness-based mutability transitions and lifetime
annotations to Escalier, replacing the current `mut?` system. It is organized
into phases that build incrementally, each producing a testable milestone.

## Phase Overview

| Phase | Description                                          | Depends On |
|------:|------------------------------------------------------|------------|
|     1 | Data structures and representations                  | —          |
|     2 | Liveness analysis (straight-line code)               | 1          |
|     3 | Liveness analysis (control flow)                     | 2          |
|     4 | Alias tracking (local variables)                     | 2          |
|     5 | Mutability transition checking                       | 3, 4       |
|     6 | Alias tracking (properties, closures, destructuring) | 4, 5       |
|     7 | Lifetime annotations and inference                   | 5, 6       |
|     8 | Lifetime unification                                 | 7          |
|     9 | Lifetime elision rules                               | 7, 8       |
|    10 | TypeScript interop                                   | 9          |
|    11 | Error messages                                       | 5–10       |
|    12 | Remove `mut?`                                        | 5–11       |
|    13 | PrintType and display                                | 12         |

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
    Name     string  // variable name for diagnostics (e.g. "items")
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

// TypeRefType — for 'a Point or mut 'a Point
type TypeRefType struct {
    // ... existing fields ...
    Lifetime Lifetime  // nil if no lifetime annotation
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

// MutabilityType — the existing mut wrapper.
// When both mut and a lifetime are present (mut 'a Point), the lifetime
// is stored on the inner type (e.g. TypeRefType.Lifetime), not on
// MutabilityType itself. MutabilityType only carries the mutability flag.
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

### 1.3 Liveness Data Structures

Create a new package `internal/liveness/` for the analysis pass.

**File:** `internal/liveness/liveness.go`

```go
package liveness

import "github.com/escalier-lang/escalier/internal/ast"

// VarID uniquely identifies a variable within a function body.
// Uses the span of the binding site as the identity.
type VarID = int

// LivenessInfo stores the results of liveness analysis for a function body.
type LivenessInfo struct {
    // LiveBefore maps each statement/expression span to the set of variables
    // that are live just before that point.
    LiveBefore map[ast.Span]map[VarID]bool

    // LiveAfter maps each statement/expression span to the set of variables
    // that are live just after that point.
    LiveAfter map[ast.Span]map[VarID]bool

    // LastUse maps each variable to the span of its last use.
    LastUse map[VarID]ast.Span
}
```

### 1.4 Alias Set Data Structures

**File:** `internal/liveness/alias.go`

```go
package liveness

// AliasSet tracks a group of variables that reference the same underlying
// value. Each value created at runtime gets its own AliasSet. Variables
// join an alias set when assigned from another variable in the set.
type AliasSet struct {
    ID        int
    Members   map[VarID]Mutability  // variable → whether it holds a mut ref
    Origin    VarID                 // the variable that created the value
}

type Mutability int

const (
    Immutable Mutability = iota
    Mutable
)

// AliasTracker manages alias sets for a function body.
// A variable may belong to multiple alias sets when assigned from different
// values depending on control flow (conditional aliasing, Phase 6.4).
type AliasTracker struct {
    NextID    int
    Sets      map[int]*AliasSet       // SetID → AliasSet
    VarToSets map[VarID][]int         // variable → which alias sets it belongs to
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

// GetAliases returns all variables in the same alias set as v.
func (a *AliasTracker) GetAliases(v VarID) map[VarID]Mutability { ... }
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
  get aliases)
- Unit tests for `LifetimeVar` and `LifetimeValue` construction

---

## Phase 2: Liveness Analysis — Straight-Line Code

**Goal:** Compute which variables are live at each program point, starting
with sequential code (no branching).

### 2.1 Variable Use Collection

Walk the AST to collect all uses of each variable. A "use" is any `IdentExpr`
that reads a variable, or any `MemberExpr` / `IndexExpr` that reads through
a variable.

**File:** `internal/liveness/collect_uses.go`

Implement an AST visitor that:
1. Visits each expression in a block of statements
2. For each `IdentExpr`, records the variable name and span as a use
3. For each `MemberExpr` with an `IdentExpr` base, records the base variable
4. For each assignment target, records as both a use (read-before-write for
   compound assignments) and a definition

The visitor produces a `map[VarID][]ast.Span` of use sites per variable.

### 2.2 Backward Liveness Analysis (Linear)

For straight-line code (a flat list of statements with no branches), liveness
is computed by walking backward from the last statement:

1. Start with `LiveAfter = {}` for the last statement
2. For each statement, working backward:
   - `LiveAfter[stmt] = LiveBefore[next_stmt]`
   - `LiveBefore[stmt] = (LiveAfter[stmt] - Defs[stmt]) ∪ Uses[stmt]`
3. A variable is "dead" at a point if it is not in `LiveBefore` or `LiveAfter`

**File:** `internal/liveness/analyze.go`

```go
// AnalyzeBlock computes liveness for a linear block of statements.
// This is the foundation — Phase 3 extends it to handle control flow.
func AnalyzeBlock(stmts []ast.Stmt, bindings map[string]VarID) *LivenessInfo { ... }
```

### 2.3 Integration Point

The liveness analysis runs per function body. Store the result on the checker
`Context` so the mutability transition checker (Phase 5) can query it.

**File:** `internal/checker/checker.go`

```go
type Context struct {
    // ... existing fields ...
    Liveness *liveness.LivenessInfo
    Aliases  *liveness.AliasTracker
}
```

### 2.4 Tests

- Test liveness for simple sequential variable declarations and uses
- Test that a variable becomes dead after its last use
- Test that variable definitions kill liveness
- Test that unused variables are never live

---

## Phase 3: Liveness Analysis — Control Flow

**Goal:** Extend liveness analysis to handle branching, loops, early returns,
and throws.

### 3.1 Control Flow Graph (CFG) Construction

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
- **`ReturnStmt`:** Edge to the exit block (terminates the path)
- **`ThrowExpr`:** Edge to the exit block (terminates the path)
- **`BlockExpr`:** Nested block — inline into the CFG with its own basic blocks

### 3.2 Backward Liveness on CFG

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
func AnalyzeFunction(cfg *CFG, bindings map[string]VarID) *LivenessInfo { ... }
```

### 3.3 Statement-Level Granularity

The CFG produces per-block liveness, but mutability checking needs per-statement
granularity. Within each basic block, use the linear analysis from Phase 2 to
compute per-statement liveness, using the block's `LiveOut` as the initial
`LiveAfter` for the last statement.

### 3.4 Tests

- `if/else`: Variable used only in one branch is dead on the other
- `if` without else: Variable used after the if is live through both branches
- `for` loops: Variable used inside loop body is live for the entire loop
- Early `return`: Variable used only after a return is dead on the returning path
- `throw`: Same as early return
- `match` expressions: Variable used in one arm may be dead in others
- Nested control flow: if inside for, match inside if, etc.

---

## Phase 4: Alias Tracking — Local Variables

**Goal:** Track which variables alias the same value through direct assignment.

### 4.1 Integrate AliasTracker with Statement Processing

As the checker processes each statement in a function body, update the
`AliasTracker`:

**File:** `internal/liveness/alias_analysis.go`

Walk statements in order:
1. **`VarDecl` with literal/constructor init:** Call `NewValue(varID, mut)` to
   create a fresh alias set
2. **`VarDecl` with identifier init** (e.g. `val b = a`): Call
   `AddAlias(b, a, mut)` to add `b` to `a`'s alias set
3. **Assignment** (e.g. `b = a` where `b` is `var`): Call
   `Reassign(b, &a, mut)` to leave old set and join `a`'s set
4. **Assignment with fresh value** (e.g. `b = {x: 1}`): Call
   `Reassign(b, nil, mut)` to create a new alias set

### 4.2 Determine Alias Source from Expressions

Not all initializers are simple identifiers. Need a function that examines an
expression and determines whether it's:
- A **fresh value** (literal, object construction, array literal, `new` call) →
  no aliasing
- A **variable reference** (identifier) → aliases that variable
- A **function call** → depends on lifetime annotations (Phase 7); for now,
  treat as fresh
- A **property access** (e.g. `obj.field`) → aliases the property's source
  (Phase 6)
- A **conditional** (e.g. `if cond { a } else { b }`) → aliases all branches
  (Phase 6)

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
func DetermineAliasSource(expr ast.Expr) AliasSource { ... }
```

### 4.3 Tests

- `val b = a` → b and a are in the same alias set
- `val b = {x: 1}` → b gets a fresh alias set
- `var b = a; b = {x: 1}` → b leaves a's set after reassignment
- `var b = a; b = c` → b leaves a's set and joins c's set
- Multiple aliases: `val b = a; val c = a` → a, b, c all in same set
- Chain: `val b = a; val c = b` → a, b, c all in same set

---

## Phase 5: Mutability Transition Checking

**Goal:** Enforce Rules 1 and 2 from the requirements — reject mutability
transitions when conflicting live aliases exist.

### 5.1 Transition Check Logic

When a value is assigned from one variable to another with a different
mutability, check the alias set and liveness:

**File:** `internal/checker/check_transitions.go`

```go
// CheckMutabilityTransition verifies that a mutability transition is safe
// at the given program point. Returns an error if conflicting live aliases
// exist.
//
// Rule 1 (mut → immutable): No live mutable aliases may exist after this point.
// Rule 2 (immutable → mut): No live immutable aliases may exist after this point.
// Rule 3: Multiple mutable aliases are always allowed.
func (c *Checker) CheckMutabilityTransition(
    ctx Context,
    sourceVar VarID,
    sourceMut bool,     // mutability of the source
    targetMut bool,     // mutability of the target
    assignSpan ast.Span,
) []Error { ... }
```

The algorithm:
1. If `sourceMut == targetMut`, no transition — always OK (Rule 3 for mut→mut)
2. Get the alias set of `sourceVar`
3. For each variable `v` in the alias set (including `sourceVar`):
   - Check if `v` is live after `assignSpan` (using `LivenessInfo`)
   - If `sourceMut && !targetMut` (Rule 1): error if `v` has mutable access
     and is live
   - If `!sourceMut && targetMut` (Rule 2): error if `v` has immutable access
     and is live

### 5.2 Integration with `inferVarDecl`

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

### 5.3 Integration with Assignment Expressions

For `var` reassignment (`b = expr`), also check transitions:

**File:** `internal/checker/infer_expr.go` (in the assignment case)

After type-checking the assignment, invoke `CheckMutabilityTransition` with
the target variable and the source expression's alias information.

### 5.4 Running the Analysis

The liveness and alias analyses must run before or during type checking of a
function body. Two options:

**Option A: Pre-pass.** Run liveness analysis on the AST before type checking
the function body. This requires the AST to already have binding information
(from pattern inference), but not full types.

**Option B: Integrated.** Compute liveness and aliases incrementally as the
checker walks statements. This is simpler to implement since the checker already
walks statements in order, but requires backward liveness to be precomputed.

**Recommended approach:** Use a **pre-pass** for liveness (backward analysis
needs full knowledge of uses) and build alias sets **incrementally** during
type checking (alias relationships are discovered as statements are processed).

The pre-pass runs at the start of `inferFuncBody` or equivalent:
1. Collect variable uses from the function body AST
2. Build CFG
3. Run backward liveness analysis
4. Store `LivenessInfo` on the `Context`
5. Initialize `AliasTracker` on the `Context`
6. During statement-by-statement type checking, update `AliasTracker` and
   invoke `CheckMutabilityTransition` at each assignment

### 5.5 Tests

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
// ERROR: r is a live mutable alias of p
val p: mut Point = {x: 0, y: 0}
val r: mut Point = p
val q: Point = p  // ERROR: r is live and mutable
r.x = 5
```

---

## Phase 6: Alias Tracking — Advanced Cases

**Goal:** Extend alias tracking to cover object properties, closures,
destructuring, conditional aliasing, and method receivers.

### 6.1 Object Property Aliasing

When a mutable value is stored into an object property, the containing object
becomes an alias:

```go
// In alias_analysis.go:
// When processing: obj.prop = expr
// If expr aliases variable v, add obj to v's alias set
```

This requires extending `DetermineAliasSource` to handle `MemberExpr` on the
left side of assignments, and to recognize when a property assignment creates
an alias relationship.

### 6.2 Closure Capture Aliasing

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

### 6.3 Destructuring Aliasing

When a value is destructured, each extracted binding aliases the corresponding
part of the original value:

```esc
val {point} = obj  // point aliases obj.point
```

**Implementation:** In `inferPattern` for `ObjectPat` and `ArrayPat`, when
the initializer is a variable reference, add each destructured binding to the
alias set of the source variable (or the specific property path).

### 6.4 Conditional Aliasing

When a variable is assigned from different values depending on control flow,
it joins the alias sets of all possible sources:

```esc
val c = if cond { a } else { b }
// c is in both a's and b's alias sets
```

**Implementation:** Extend `DetermineAliasSource` to handle `IfElseExpr` and
`MatchExpr` by collecting alias sources from all branches and returning
`AliasSourceMultiple`. The `AliasTracker` already supports membership in
multiple alias sets via `VarToSets` (defined in Phase 1.4).

### 6.5 Reassignment

When a `var` variable is reassigned, it leaves its previous alias set(s):

```esc
var b = a       // b aliases a
b = {x: 1, y: 1}  // b leaves a's alias set
val q: Point = a   // OK: b no longer aliases a
```

This is already handled by `AliasTracker.Reassign` from Phase 4.

### 6.6 Method Receiver Aliasing

When a method stores a parameter into `self`, the receiver becomes an alias
of that parameter. This is handled through lifetime annotations (Phase 7) —
the method's lifetime signature captures the relationship, and the caller
updates alias sets based on the signature.

### 6.7 Tests

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

## Phase 7: Lifetime Annotations and Inference

**Goal:** Add lifetime annotations to function signatures and infer them from
function bodies.

### 7.1 Parser Changes — Lifetime Syntax

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
// LifetimeAnn represents a lifetime annotation in source code (e.g. 'a).
type LifetimeAnn struct {
    Name string  // e.g. "a" (without the tick)
    span Span
}

// LifetimeParamAnn represents a lifetime parameter declaration (e.g. 'a in <'a, T>).
type LifetimeParamAnn struct {
    Name string
    span Span
}
```

Add `LifetimeParams` to `FuncTypeAnn`:

```go
type FuncTypeAnn struct {
    LifetimeParams []*LifetimeParamAnn  // e.g. ['a, 'b]
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

### 7.2 Lexer Changes

Add a new token type for lifetime identifiers:

**File:** `internal/parser/lexer.go`

The lexer recognizes `'` followed by an identifier as a `LIFETIME` token
(e.g. `'a` → Token{Kind: LIFETIME, Value: "a"}).

This must be context-aware to avoid conflicts with character literals (if any)
or other uses of `'`. Since Escalier uses double quotes for strings and doesn't
have character literals, `'` followed by an identifier should be unambiguous.

### 7.3 Lifetime Inference from Function Bodies

When a function body is available, infer lifetime relationships by analyzing
which parameters are returned or stored:

**File:** `internal/checker/infer_lifetime.go`

```go
// InferLifetimes analyzes a function body to determine which parameters
// are aliased by the return value. Returns the inferred lifetime parameters
// and a map from parameter index to the lifetime variable linking that
// parameter to the return type.
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
1. Walk the function body to find all `return` statements
2. For each return expression, determine its alias source (using
   `DetermineAliasSource` from Phase 4)
3. If the return expression aliases a parameter, create a lifetime variable
   linking that parameter to the return type
4. If the return expression calls another function with lifetime annotations,
   propagate those lifetimes
5. If the function stores a parameter into a module-level variable, assign
   `'static` to that parameter

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

### 7.4 Escaping Reference Detection

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

### 7.5 Effect on Callers — Alias Set Updates

When a function with lifetime annotations is called, the caller's alias
tracker must be updated:

**File:** `internal/checker/infer_expr.go` (in call expression handling)

After type-checking a call expression:
1. Look up the callee's `FuncType` and its `LifetimeParams`
2. For each lifetime parameter that links a parameter to the return type,
   add the call result variable to the alias set of the argument variable
3. For `'static` parameters, mark the argument as permanently aliased

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

### 7.6 Constructor Lifetime Inference

Constructors require special handling because unlike functions, they do not
have explicit `return` statements — they produce objects from parameters. The
requirements specify that when a constructor parameter is stored as a field,
the constructed object aliases that parameter.

**Differences from function lifetime inference:**
- The lifetime appears on the **constructed type** (e.g. `'a Container`),
  not on a return type in the traditional sense
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

**Example — single reference parameter:**
```esc
class Container(item: mut Point) { item, }
// Inferred: Container<'a>(item: mut 'a Point) → 'a Container
```

**Example — multiple reference parameters:**
```esc
class Pair(first: mut Point, second: mut Point) { first, second, }
// Inferred: Pair<'a, 'b>(first: mut 'a Point, second: mut 'b Point)
// Both 'a and 'b appear on the constructed type
```

**Example — primitive parameters (no lifetime):**
```esc
class Point(x: number, y: number) { x, y, }
// No lifetimes — x and y are primitives
```

**Effect on callers:** When a constructor with lifetime annotations is called,
the caller's alias tracker is updated the same way as for function calls
(Phase 7.5). The constructed object is added to the alias set of each
reference-typed argument whose corresponding parameter has a lifetime.

**Elision for constructors:** If a constructor has a single reference
parameter, elision rule 1 applies — the constructed type's lifetime is
assumed to match the input. For constructors with multiple reference
parameters, explicit annotation is required (or inference determines the
lifetimes from the class definition, which is the common case since
constructors always have bodies).

### 7.7 Recursive and Mutually Recursive Functions

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

### 7.8 Tests

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
```

**Explicit annotation parsing:**
```esc
fn first<'a, 'b>(a: mut 'a Point, b: mut 'b Point) -> mut 'a Point { return a }
```

**Constructor lifetime inference:**
```esc
class Container(item: mut Point) { item, }
// Inferred: Container<'a>(item: mut 'a Point) → 'a Container

val p: mut Point = {x: 0, y: 0}
val c = Container(p)   // c aliases p
val q: Point = p        // ERROR: c is live and provides mutable access to p

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

---

## Phase 8: Lifetime Unification

**Goal:** Integrate lifetime variables into the type unification engine so
that lifetimes are propagated through type inference.

### 8.1 Lifetime Variable Binding

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

### 8.2 Lifetime Instantiation at Call Sites

When a generic function with lifetime parameters is called, the lifetimes are
instantiated similarly to type parameters:

**File:** `internal/checker/infer_expr.go`

In `instantiateGenericFunc`, also instantiate lifetime parameters:
1. Create fresh `LifetimeValue` instances for each `LifetimeVar` in the signature
2. Substitute lifetime variables in parameter and return types
3. After argument unification, the lifetime values are bound to the alias sets
   of the actual arguments

### 8.3 Lifetime Constraint Propagation

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
for multiple parameters (e.g. `fn swap<'a>(a: mut 'a Point, b: mut 'a Point)`),
the unification process detects conflicts:

1. The first argument binds `'a` to the `LifetimeValue` of the first actual
   argument (e.g. `p`'s lifetime)
2. The second argument tries to bind `'a` to the `LifetimeValue` of the
   second actual argument (e.g. `q`'s lifetime)
3. If `p` and `q` have different `LifetimeValue`s (they are independent
   values), unification detects a **conflict** — `'a` is already bound to
   `p`'s lifetime and cannot also be `q`'s lifetime
4. The error message explains that the function requires both arguments to
   alias the same value

If the caller passes two aliases of the same value (e.g. `p` and `r` where
`r` aliases `p`), both have the same `LifetimeValue` (or are in the same
alias set), and unification succeeds.

#### `'static` Lifetime in Unification

The `'static` lifetime (assigned to parameters that escape via Phase 7.4)
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

### 8.4 Lifetime on Generic Type Parameters

When lifetimes appear on type parameters (e.g. `Array<'a T>`), unification
recurses into the type arguments and propagates lifetimes:

- `Array<'a T>` vs `Array<T>`: Succeeds; lifetime propagated
- `Array<'a T>` vs `Array<'b T>`: Succeeds if `'a` and `'b` unify
- `mut Array<'a T>` vs `mut Array<'b T>`: Invariant — `'a` and `'b` must unify

### 8.5 Function Type Unification with Lifetimes

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
fn apply<'a>(f: fn(mut 'a Point) -> mut 'a Point, p: mut 'a Point) -> mut 'a Point {
    return f(p)
}
```

When `apply(identity, myPoint)` is called:
1. Unify `identity`'s type with the expected `fn(mut 'a Point) -> mut 'a Point`
2. `identity`'s own lifetime `'b` is unified with `'a` through the parameter
   and return types
3. `'a` is bound to `myPoint`'s lifetime from the second argument
4. The return type `mut 'a Point` inherits `myPoint`'s lifetime, so the
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

### 8.6 Tests

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

## Phase 9: Lifetime Elision Rules

**Goal:** Apply default lifetime rules to body-less declarations so that
common cases don't require explicit annotations.

### 9.1 Elision Rule Implementation

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

### 9.2 Determining "Reference Type"

A type is a "reference type" for elision purposes if it can alias:
- Object types
- Array types
- Function types
- Type references that resolve to object/array/function types
- NOT: primitives (`number`, `string`, `boolean`), `void`, `null`, `undefined`

**File:** `internal/checker/elision.go`

```go
// IsReferenceType returns true if the type can participate in aliasing.
func IsReferenceType(t type_system.Type) bool { ... }
```

### 9.3 Interface Method Lifetime Verification

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

type Cloner: Transform {
    fn apply(self, p: mut Point) -> mut Point {
        return {x: p.x, y: p.y}  // OK: more conservative (fresh value)
    }
}

type Storer: Transform {
    fn apply(self, p: mut Point) -> mut Point {
        self.stored = p
        return self.stored  // ERROR: returns alias of self, not p — violates 'a
    }
}
```

### 9.4 Integration

Apply elision during:
- Interface method declaration processing (`inferInterface`)
- External function declaration processing
- TypeScript type import processing (Phase 10)

Apply lifetime verification during:
- Interface implementation checking (when a type declares it implements an
  interface)

### 9.5 Tests

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

## Phase 10: TypeScript Interop

**Goal:** Automatically assign lifetime annotations to TypeScript type
declarations imported into Escalier.

### 10.1 Automatic Lifetime Assignment

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

### 10.2 Override Mechanism

Support manual override files for correcting heuristic lifetime assignments:

**File:** `internal/interop/lifetime_overrides.go`

Override files (`.esc.d.ts` or a dedicated format) allow developers to provide
explicit lifetime annotations for specific imported functions. The overrides
are loaded during package initialization and take precedence over heuristic
rules.

**Format:** TBD — could be a subset of Escalier syntax with explicit lifetime
annotations, or a JSON/TOML configuration file.

### 10.3 Built-in Overrides

Ship overrides for common TypeScript APIs (Array methods, Promise, etc.)
as part of the standard library. These are the overrides listed in the
requirements document (Array.forEach, Array.map, Array.filter, etc.).

**File:** `internal/checker/prelude_lifetimes.go`

### 10.4 Tests

- `Array.prototype.sort` → aliases receiver
- `Array.prototype.map` → fresh array, no container alias
- `Array.prototype.filter` → fresh array, element alias
- `Array.prototype.find` → element alias
- `Object.keys` → no alias
- Callback with readonly parameter → immutable
- Override file correctly replaces heuristic assignment

---

## Phase 11: Error Messages

**Goal:** Produce clear, actionable error messages that show lifetime
information only when it helps the developer.

### 11.1 Error Types

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

### 11.2 Error Formatting

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

### 11.3 Tests

- Simple local alias error message
- Cross-function alias error message with lifetime shown
- Escaping reference error message
- Multiple conflicting aliases shown in one error
- Help text suggests concrete fix

---

## Phase 12: Remove `mut?`

**Goal:** Remove `MutabilityUncertain` from the type system, completing the
transition to liveness-based mutability.

### 12.1 Audit All Uses of `MutabilityUncertain`

The `MutabilityUncertain` variant is used across the codebase. Each use must
be replaced. The table below lists known files — run
`grep -r MutabilityUncertain internal/` to produce the complete list before
starting this phase:

| File | Current Use | Replacement |
|------|-------------|-------------|
| `type_system/types.go` | `MutabilityUncertain` constant | Remove |
| `type_system/print_type.go` | Printing `mut?` | Remove case |
| `checker/unify.go` | Special handling during unification | Remove |
| `checker/unify_mut.go` | Invariant checking with `mut?` | Simplify |
| `checker/infer_expr.go` | Creating `mut?` types | Use `mut` or immutable directly |
| `checker/infer_func.go` | `mut?` in parameter inference | Binary inference (mut/immutable) |
| `checker/infer_module.go` | `mut?` propagation | Remove |
| `checker/generalize.go` | Stripping `mut?` during generalization | Remove stripping |
| `checker/expand_type.go` | `mut?` in type expansion | Remove case |
| `checker/iterable.go` | `mut?` on iterable types | Remove |
| `codegen/dts.go` | `mut?` in .d.ts output | Remove |
| *(remaining files)* | Grep for complete list at implementation time | Case-by-case |

### 12.2 Simplify `MutabilityType`

Change `Mutability` from a three-state enum to a boolean or remove the
uncertainty variant:

```go
// Before:
const (
    MutabilityMutable   Mutability = "!"
    MutabilityUncertain Mutability = "?"
)

// After: Only MutabilityMutable remains. Immutable types have no wrapper.
const MutabilityMutable Mutability = "!"
```

Or simplify `MutabilityType` to always represent `mut` (the wrapper's presence
means mutable, absence means immutable).

### 12.3 Replace `mut?` Inference with Direct Tracking

Where the checker currently creates `MutabilityUncertain` types, replace with
direct write-tracking:
- If a parameter is written to in the function body → `mut`
- If a parameter is only read → immutable
- No intermediate `mut?` state

### 12.4 Update Snapshot Tests

Many snapshot tests will change since `mut?` will no longer appear in type
output. Run all tests with `UPDATE_SNAPS=true` to update snapshots, then
review the diffs to ensure correctness.

### 12.5 Tests

- All existing mutation tests should still pass (behavior preserved)
- Types no longer show `mut?` in any output
- `MutabilityUncertain` constant is removed (compile error if referenced)

---

## Phase 13: PrintType and Display

**Goal:** Update type printing to support lifetime display with configurable
visibility.

### 13.1 Default: Hidden Lifetimes

By default, `PrintType` does not show lifetime annotations:

```go
// fn identity(p: mut Point) -> mut Point
```

### 13.2 Verbose Mode: Show Lifetimes

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

### 13.3 Error Context: Show Relevant Lifetimes

In error messages (Phase 11), show lifetimes only when they are relevant to
understanding the error. This uses a targeted print mode that shows lifetimes
for specific functions referenced in the error, not globally.

### 13.4 Tests

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

### Backwards Compatibility

- **Source compatibility:** Existing code that doesn't use `mut?` explicitly
  (which is all user code, since `mut?` is internal) is unaffected
- **New errors:** Some programs that were previously accepted may now produce
  liveness errors. These represent real bugs (aliasing violations) that the
  old system silently permitted
- **Migration path:** Enable lifetime checking with a flag initially, allowing
  gradual adoption. Once stable, enable by default and remove the flag

### LSP Integration (Out of Scope — Follow-On Work)

The language server (`cmd/lsp-server/`) will need updates after the core
implementation is stable. These are explicitly **not part of Phases 1–13**:

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
  deferred until the error types (Phase 11) are finalized.

---

## Implementation Order and Dependencies

```text
1. Data structures
└── 2. Liveness (linear)
    ├── 3. Liveness (control flow)
    │   └── 5. Transition checking ←── (also depends on 4)
    └── 4. Alias tracking (local)
        ├── 5. Transition checking
        │   └── 6. Advanced alias tracking (properties, closures, destructuring)
        │       └── 7. Lifetime annotations, inference, & constructors
        │           ├── 8. Lifetime unification ('static, conflict detection,
        │           │      higher-order function threading)
        │           │   └── 9. Elision rules & interface lifetime verification
        │           │       └── 10. TypeScript interop
        │           └────────── 10. TypeScript interop
        └── 6. Advanced alias tracking
11. Error messages ←── (depends on 5–10)
└── 12. Remove mut?
    └── 13. PrintType & display
```

Phases 2–4 can be developed in parallel. Phases 7–10 can also be partially
parallelized (elision and TS interop depend on the annotation infrastructure
but not on each other).

**Key sub-phase dependencies within phases:**
- Phase 7.6 (constructors) depends on 7.3 (lifetime inference from bodies)
- Phase 8.3 (`'static` and conflict detection) depends on 8.1 (lifetime
  binding) and 8.2 (instantiation)
- Phase 9.3 (interface lifetime verification) depends on 7.3 (inference)
  and 8.1 (binding)
