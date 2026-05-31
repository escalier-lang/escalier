# 02 — Design notes

Concrete shapes for the pieces the milestones reference. These promote the
spike (`internal/simplesub/`) into production form. Names are
provisional.

## Package layout

```text
internal/solver/            (new top-level package, sibling to internal/checker/; leaf name TBD)
  soltype/                  the type representation (its own; NOT type_system)
    type.go                 Type interface; TypeVar, Primitive, Literal,
                            Function, Tuple, Record, Ref, Union, Intersection,
                            Alias, ... each carrying what it needs
    lifetime.go             Lifetime sort: LifetimeVar (bound lists),
                            StaticLifetime, LifetimeUnion
    print.go                Type -> Escalier annotation string
  constrain.go              constrain(lhs <: rhs), extrusion, lattice rules
  poly.go                   let-polymorphism: schemes, instantiate, freshenAbove
  simplify.go               occurrence analysis, co-occurrence merging
  coalesce.go               SimpleType -> coalesced soltype.Type (polarity)
  infer.go                  the AST-driven inference walk; Info side table
  scope.go                  Scope / Binding / Namespace (own, not type_system)
  errors.go                 type error representation
```

## `soltype` — the type representation

The clean, algorithm-shaped data model that motivates not reusing
`type_system`. The core difference from `type_system.TypeVarType`:

```go
// type_system today: single mutable cell.
type TypeVarType struct { Instance Type; Constraint Type; ... }

// soltype: bound lists (Simple-sub).
type TypeVar struct {
    id          int
    level       int
    lowerBounds []Type
    upperBounds []Type
}
```

Borrows and mutability are carried by a single unified wrapper, `Ref`, with
two flags. Owned types (`Record`, `Tuple`, `Alias`, `Class`) have **no**
lifetime field — a lifetime is the lifetime of a *borrow*, and a borrow is
structurally distinct from the value being borrowed:

```go
type Record struct { fields map[string]Type }         // owned, no lt
type Tuple  struct { elems  []Type }                   // owned, no lt
type Alias  struct { name string; body Type }          // owned, no lt
// Class adds final/exact in M5; see "Exactness" below.

type Ref struct {
    mut   bool       // mutable borrow if true, immutable borrow if false
    lt    Lifetime   // nilable: nil = owned mutable (only meaningful with mut=true)
    inner Type
}
```

The four cells of `(mut, lt)`:

| `mut` | `lt`  | Meaning                                        |
|-------|-------|------------------------------------------------|
| `false` | `nil` | (degenerate — equivalent to bare `inner`)    |
| `false` | `'a`  | immutable borrow with lifetime `'a`          |
| `true`  | `nil` | owned mutable value                          |
| `true`  | `'a`  | mutable borrow with lifetime `'a`            |

The `(false, nil)` cell is meaningless — a smart constructor returns the bare
`inner` rather than constructing it.

### The one `Ref` constrain rule

```go
case Ref <: Ref:
    // 1. Mutability compatibility — can't widen immut to mut.
    if !l.mut && r.mut {
        return mutabilityError
    }

    // 2. Inner variance — bidirectional iff the *target* is mutable
    //    (read view always; write view only when the target writes).
    if r.mut {
        constrain(l.inner, r.inner, seen)
        constrain(r.inner, l.inner, seen)
    } else {
        constrain(l.inner, r.inner, seen)
    }

    // 3. Lifetime — covariant when both present.
    switch {
    case l.lt != nil && r.lt != nil:
        constrainLt(r.lt, l.lt)
    case l.lt == nil && r.lt != nil:
        // owned source into borrow slot: ok (owned satisfies any lifetime)
    case l.lt != nil && r.lt == nil:
        // borrow source into owned slot: reject (escape)
    }

case bare <: Ref:
    // Owned value flowing into a Ref slot: wrap with mut=false, lt=nil
    // (i.e. as if the source were Ref{mut: false, lt: nil, inner: l}) and
    // dispatch back into the Ref <: Ref case. The Ref slot's lt being nil
    // or set is handled there; an owned source satisfies any required
    // lifetime (the lt-nil-on-source branch above).
    constrain(Ref{mut: false, lt: nil, inner: l}, r, seen)

case Ref <: bare:
    // Ref source into an owned-typed slot: only valid when l represents
    // an owned value (l.mut may be true or false, l.lt must be nil).
    // Equivalently: peel the wrapper and continue.
    if l.lt != nil {
        return escapeError
    }
    constrain(l.inner, r, seen)
```

The two cross-cases are written as branches of the same rule because they're
the same lattice question — "does this value fit this borrow shape" — viewed
from either side; they're not separate rules over distinct wrapper types.
Mut-borrow decay to immutable is the `l.mut && !r.mut` sub-branch of `Ref <:
Ref` (step 1 falls through and step 2 takes the covariant-only path).

### What this representation buys us

**The bidirectional inner sweep cannot accidentally invariate a lifetime.**
Lifetimes don't live inside the inner (carriers have no `lt` field), so the
recursive bidirectional `constrain` over `inner` has no `lt` to sweep. The
covariant lifetime constraint is emitted exactly once, by the `Ref` rule
itself, in step 3 above. The lifetime-invariance bug — where the bidirectional
emission accidentally doubles a structural rule's `constrainLt` call into both
directions, forcing equality — is **structurally impossible** rather than
prevented by a special case.

This matters in two specific cases that motivated the design:

- **Multiple mut aliases to the same value with independent lifetimes.**
  Escalier compiles to single-threaded JS and explicitly allows aliased mut
  borrows; their lifetimes are independent per-borrow properties. `Ref{mut:
  true, lt: 'a, R} <: Ref{mut: true, lt: 'b, R}` succeeds when `'a` outlives
  `'b` — no spurious lifetime equality.
- **Nested borrows.** A field that is itself a borrow (e.g.
  `Ref{mut:true, lt:'a, Record{f: Ref{mut:true, lt:'b, ...}}}`) is correctly
  invariant in `'b`: the outer Ref's bidirectional sweep recurses through
  the outer Record's field `f`, hits the inner Ref, and the inner Ref's rule
  fires once per direction — emitting `constrainLt` in both directions, which
  is the correct semantics for "the type of a field of a mut record." The
  top-level `'a` stays covariant (handled directly by the outer Ref rule);
  the nested `'b` is invariant (handled by the bidirectional sweep). Both
  outcomes are correct, and neither requires special casing.

### Why this representation, not the alternatives

Four other shapes were considered and rejected:

- **`mut` wrapper + `lt` field on carriers** (the original draft). The
  bidirectional `Mut`-sweep over the inner Record accidentally invariates the
  carrier's `lt` field, because the structural rule emits `constrainLt(l.lt,
  r.lt)` once per direction of the sweep. The fix requires a "peel" or
  "suppress-lt-emission" special case in the Mut rule; the bug is prevented
  by code discipline rather than by representation.
- **`lt` on `Mut` only, no `Ref` wrapper.** Doesn't cover immutable borrows,
  which Escalier supports with their own lifetimes (`'a {x: number}` is a
  valid type for a read-only borrow).
- **`lt` on both `Mut` and carriers, with a "don't set both" invariant.**
  Discipline is non-local: every construction site and every transformation
  has to migrate the `lt` between locations. Two homes for one piece of
  information, silently inconsistent when the invariant drifts.
- **Both `mut` and `lt` as flags directly on each carrier** (no wrapper at
  all). Re-creates the original "N carriers each duplicate the
  bidirectional-if-mut logic" cost that motivated `mut`-as-wrapper to begin
  with — plus each carrier still has to handle the lt-peel logic to avoid
  the original invariance bug. Worst of both worlds; the wrapper centralizes
  both axes in one rule.

The unified `Ref` consolidates the borrow concern in a single wrapper with a
single rule. Carrier rules (`Record`, `Tuple`, `Alias`, `Class`) stay focused
on structural subtyping and know nothing about mut or lifetimes. The flag
form for `mut` parallels the choices already made for `exact` (on
`Record`/`Tuple`/`Union`) and `final` (on `Class`); the wrapper form for the
borrow itself reflects that `Ref` is a structurally distinct *kind* of type
(it has different rules), not just an annotation on a value-type.

### Costs

The representation is not free; the costs are small and contained, but worth
calling out so they're not surprises later:

- **One degenerate cell to police.** `Ref{mut: false, lt: nil, inner}` is
  semantically equivalent to bare `inner` (immutable, no borrow). Construction
  must go through a smart constructor that returns the bare `inner` rather than
  wrapping it. If a future code path bypasses the constructor and produces a
  raw `Ref{false, nil, x}`, every downstream rule that pattern-matches on
  `*Ref` would see it as a borrow-shaped value that isn't really a borrow —
  the bugs are subtle (e.g. an `if r, ok := t.(*Ref); ok` branch fires when it
  shouldn't). Mitigation: a single `NewRef(mut, lt, inner)` constructor that
  enforces the invariant, and an assertion in the printer / `lifetimeOf` /
  `isMutableType` that the degenerate cell is never observed.
- **Slightly less direct type-switches at call sites.** "Is this a mutable
  borrow?" today is a one-line type assertion (`_, ok := t.(*Mut)`); under
  the unified `Ref`, it's a two-line check (`r, ok := t.(*Ref); ok && r.mut`).
  Cosmetic loss, but readers reaching for `*Mut` muscle memory will have to
  adjust. The wins on `lifetimeOf` / `isMutableType` more than compensate.
- **Two axes encoded in one node.** Reading a `Ref` in test fixtures, printer
  output, or debugger views requires holding both axes in mind: "this is a
  Ref with mut=X and lt=Y wrapping Z." The surface syntax (`mut 'a Point`,
  `'a Point`, `mut Point`) doesn't change, but the internal representation is
  slightly more conceptually dense per node than a separate-types form would
  be. Mostly a non-issue once the team has internalized "`Ref` is *the*
  borrow wrapper."
- **Construction sites have one new step.** Every place that today produces a
  borrowed value (`mut` parameter binding, address-of-style construction,
  field write inference, multi-source return joining) now constructs a `Ref`
  wrapper around an owned inner, rather than a carrier-with-lt directly.
  That's a real refactor surface for the eventual port, and the construction
  code has to remember which `mut` and which `lt` to pass. With Mut as a
  separate wrapper and lt on the carrier (today's spike), some of these sites
  just set `lt` on the carrier in place; under the unified `Ref`, they
  explicitly construct the wrapper. More code per site, but more
  type-checkable code per site.
- **Tooling that expects "the type of a value" to flow through one shape
  doesn't quite work.** A piece of code that today destructures a `Record`
  to inspect its fields can no longer assume the value-typed binding's type
  is a `Record` directly — it might be `Ref{...,inner: Record{...}}`. Every
  field-inspection or member-access code path has to first peel any `Ref`
  wrapper to reach the carrier. The peel is one line, but it has to appear
  everywhere a Record (or Tuple, etc.) is consumed. Helpers (`unwrapRef`,
  `carrierOf`) keep this from being ugly, but they have to exist and be
  used consistently. **Provenance is not affected by this peel** — `unwrapRef`
  just navigates to the existing `inner` pointer, so both the wrapper's and
  the carrier's `Prov` entries are independently preserved. Downstream
  consumers (errors, hovers) choose *which* of the two to surface based on
  what they're reporting (carrier Prov for field-shape errors, wrapper Prov
  for mutability/lifetime errors), but neither is ever lost.
- **Lifetime elision is per-wrapper, not per-occurrence.** Today, a `Record`
  with a parameter-only `lt` that connects nothing has its `lt` elided
  in-place. Under the unified `Ref`, the elidable lifetime sits on the
  wrapper — and the question becomes whether to elide just the lifetime
  (leaving a `Ref{mut: true, lt: nil, inner: ...}` for an owned-mutable
  result) or to also drop the `Ref` wrapper entirely (if the result is
  effectively bare). The coalescer needs to know the difference and pick
  the right one. Not hard, but it's one more shape of decision the elision
  pass has to make.

None of these are showstoppers, and most fall under "write a constructor and
a couple of helpers, then use them consistently." The reason they're worth
naming is that they're the kind of cost that compounds quietly — each site
that forgets the smart constructor, or that destructures a value without
peeling its `Ref` first, becomes a paper cut. The team needs to internalize
`Ref` as the standard borrow wrapper and reach for it reflexively, the way
the existing checker reflexively reaches for `MutType`.

Lifetimes are a **second sort** solved by the same machinery:

```go
type LifetimeVar struct { id int; lowerBounds, upperBounds []Lifetime }
type StaticLifetime struct{}           // top of the outlives lattice
```

## Exactness

Escalier's structural formers are **exact by default** — closed, no extra
members — with a trailing `...` opting into inexactness. The full semantics live
in `planning/exact-types/requirements.md` (merged on `main`); this section
records what the checker core must carry. The guiding insight: exactness is the
same architectural shape as lifetimes — a property *carried on the former*, cheap
to add when the former is born, painful to retrofit across `constrain` /
`coalesce` / `analyze` / `freshenAbove` / `extrude` / `print`. So the **flag and
the subtyping rule are introduced with each former (M3–M6)**; the propagation and
utility machinery is deferred (M8) and the value-level conversion is codegen (M9).

**Representation** — an `exact` flag on each structural former, plus `final` on
classes (a class instance is exact iff `final`):

```go
type Record struct { fields map[string]Type;            exact bool }
type Tuple  struct { elems  []Type;                     exact bool }
type Func   struct { /* params, ret, ... */             exact bool } // call-site only
type Union  struct { types  []Type;                     exact bool }
type Class  struct { name string; args []Type;          final bool } // final ⇒ exact instance
// Ref{mut, lt, inner} wraps a borrowed/mutable value — see "soltype" above.
```

**`Intersection` carries no `exact` flag.** Exactness is a property of *value
shapes* (objects, tuples, functions) and *closed alternative-sets* (unions) —
both about whether the set of inhabitants is *open*. A union is a join, so
"and possibly more" (`A | B | ...`) is meaningful: more alternatives, a sound
widening. An intersection is a meet (a *constraint combinator*), so the dual
"`A & B & ...`" would mean *hidden extra constraints* — fewer inhabitants, no
value-level shape, the opposite of what inexact means everywhere else. So
intersection has no exact/inexact variant; instead the exactness of its *result*
is **derived from its operands** during coalescing/normalization (spec §7.7) —
e.g. exact-object `&` exact-object → an exact object over the union of declared
properties; exact `&` inexact → exact iff the inexact side's declared props are a
subset, else an error. The `exact` flag then lands on the resulting
`Record`/`Tuple`, not on the `Intersection` node.

**Subtyping rules** (added to `constrain`'s structural cases):

- **Objects / tuples / unions** — the one-way rule: exact `<:` inexact, *not* the
  reverse. Exact `<:` exact requires the *same* member set (no width subtyping);
  inexact `<:` inexact is the spike's current structural width subtyping.
- **Functions** — direct calls reject extra args regardless of exactness;
  exactness governs **callback subtyping**. A function type accepts the set of
  arg-counts it can be invoked with (exact `[required, declared]`, inexact
  `[required, ∞)`), and `G <: F` iff `G` accepts every arg-count `F`'s holders
  may invoke with (params contravariant, return covariant). The spike's "fewer
  params is a subtype" is the *inexact* case. (This corrects the merged spec's
  §4.2 — see escalier-lang/escalier#677.)
- **`Ref` is orthogonal** ([exact-types/requirements.md](../exact-types/requirements.md) §7.11): a `Ref` wrapper carries its inner
  carrier's exactness through unchanged; the mut/lifetime axes neither tighten
  nor loosen exactness. The `Ref` rule's mut-driven inner invariance and the
  carrier's `exact` flag compose without interaction.

**Inference defaults:** object/tuple/union *literals* infer as **exact**; a
*usage-inferred* shape (from member access) **also infers as exact** — the row
is closed once body inference completes (Policy A). Row-polymorphism is opt-in
via an `open` parameter marker (keyword provisional): `fn dist(open p) => ...`
keeps `p` inexact so callers can pass records richer than the field set the body
touches. **TS imports are inexact** for all categories
([exact-types/requirements.md](../exact-types/requirements.md) §8).

Rationale: most Escalier code is application code, not generic library code, so
biasing the default toward "exact = catch extra-field bugs at the call site" is
the better fit for the typical audience. Library authors writing row-polymorphic
helpers pay a one-keyword cost. The alternative (Policy B — usage-inferred
shapes default inexact, treating the row variable as the natural meaning of a
lower bound) was considered and rejected on default-audience grounds.

**This decision is not yet reflected in `planning/exact-types/requirements.md`;
the spec needs a section recording it before M3 lands.**

**Day-one behavior.** Escalier code is exact-by-default and TypeScript-imported
types are inexact-by-default *from the moment each former lands* — exact
functions in M3, exact records/tuples in M4, exact class-instances-via-`final`
in M5, exact unions in M6. Tests at each milestone assert what the
implementation produces, so there's no window in which the code is one default
and the tests another. (The spike itself is uniformly inexact-by-default — the
*opposite* of the spec — but that's an artifact of the spike, not the
production direction.)

**Deferred (not core):** `Exact<T>`/`Inexact<T>` type operators and exactness
propagation through `keyof`/mapped/conditional types (M8, type operators);
value-level `exact<T>(v)` lowering and `@escalier-type` JSDoc round-tripping
(M9, codegen); the `std:*`/`dom:*` finality/inexactness annotation effort
([exact-types/requirements.md](../exact-types/requirements.md) §11 +
[exact-types/builtin-classes.md](../exact-types/builtin-classes.md), independent
stdlib track).

## `Info` side table (the AST decoupling — option (a))

The AST is never modified. Node→type associations live here, à la
`go/types.Info`. Nodes are always pointers, so pointer-keyed maps work.

```go
type Info struct {
    types map[ast.Node]soltype.Type
    // (later) defs/uses, scopes, etc. as needed by LSP
}
func (i *Info) TypeOf(n ast.Node) soltype.Type { return i.types[n] }
func (i *Info) setType(n ast.Node, t soltype.Type) { i.types[n] = t }
```

The new checker **never** calls `ast` node `InferredType()`/`SetInferredType()`.
The embedded `inferredType` field stays for the old checker until M11 cleanup.

## Provenance side table (the inverse of `Info`)

`type_system` carries provenance as a `Provenance()` field on every `Type` node.
Soltype goes the other way: provenance lives in a **sparse side table** keyed by
the soltype node's pointer identity, populated by the constraint generator at a
small number of construction sites (param binding, literal inference, scheme
instantiation, application result, etc.).

```go
type Prov map[soltype.Type]Origin   // sparse — shared/synthesized nodes may be absent

// Origin is a tagged sum: each variant names the kind of hop.
type Origin interface{ isOrigin() }

// Leaf: a direct AST cause.
type FromAST struct {
    Node ast.Node
    Kind ASTOriginKind   // ParamBinding | LiteralInference | Application | Return | ...
}

// A bound on this variable was inherited from another variable's bound when
// constrain(α <: β) propagated β's existing upper bounds into α (and α's
// existing lower bounds into β).
type FromBoundPropagation struct {
    Via   *Variable      // the variable whose bound we propagated through
    Bound Type           // the specific bound that propagated
}

// Fresh instance of a scheme variable at a call site (freshenAbove /
// instantiate). CallSite is an AST cause, but indirect — the fresh variable
// "is an instance of" Original, not "is" the call site.
type FromInstantiation struct {
    Original Type
    Scheme   *Scheme
    CallSite ast.Node
}

// Extruded copy of a higher-level type at a lower level, created when a
// variable from a higher level escapes via a constraint at a lower one.
type FromExtrusion struct {
    Source Type
    Level  int
}

// Synthesized by coalescing — the join/meet of contributing bounds.
type FromCoalesce struct {
    Contributors []Type
    Polarity     Polarity
}

func (FromAST) isOrigin()              {}
func (FromBoundPropagation) isOrigin() {}
func (FromInstantiation) isOrigin()    {}
func (FromExtrusion) isOrigin()        {}
func (FromCoalesce) isOrigin()         {}
```

Provenance is a **DAG rooted in AST nodes**: interior edges are typed
type→type links of the four non-leaf kinds; leaves are `FromAST`. Error
renderers walk the DAG until they hit leaves. "Why is `α` constrained to be a
number?" unfolds as: "α got `number` from β via bound propagation
(`FromBoundPropagation`); β got `number` because the literal `42` was
constrained to flow into β (`FromAST: LiteralInference` at line 17)."

**Why a typed sum rather than a single pointer.** `type_system`'s
`Provenance()` field is a single `Type` pointer that may chain through other
types — "X was unified with Y, which was unified with Z, …". Every link has the
same shape (unification), so the pointer alone doesn't tell you *why* the link
exists; you walk pointers and hope the right AST node is at the end. Soltype's
provenance graph has the same multi-hop character (bound propagation, scheme
instantiation, level extrusion, coalescing all introduce type→type edges
without an immediate AST cause), but each hop is a **named operation** with its
own variant. An error renderer can say "propagated through β" or "fresh from
scheme S at call site C" rather than "see also: type 0x4f3a1c". Same DAG
structure, structurally honest about what each edge means.

**Why a side table and not a field.** Soltype relies on sharing/interning for
the common atoms (`Primitive{number}`, the few `Literal`s, `Void`,
`StaticLifetime`). A `Provenance()` field per node either breaks that interning
(two structurally-equal `Primitive{number}` from different ASTs become distinct
because their provenance differs) or lies about provenance on the shared nodes
(every use of `number` reports the same source location). Provenance is
**per-occurrence**; structure is **per-shape**. A sparse map sidesteps the
mismatch: shared atoms get no entry, unique nodes get one.

Synthesized types — a coalesced upper-bound intersection, a freshened instance
of a let-polymorphic scheme, a union built during multi-branch return inference
— don't have a single AST node that produced them. With a field you'd be forced
to invent a fiction (`nil`? the call site? the scheme's decl?); with a sparse
map, a missing entry is honest. The freshening pass that produces an instance
of a scheme is also a natural place to *extend* a `Trail` rather than overwrite
a single pointer.

**Symmetric with `Info`.** `Info: ast.Node → Type` is the forward direction;
`Prov: Type → Origin` is its inverse. Same package, same walk populates both,
same downstream consumers (error messages, LSP hovers, the differential harness
when diagnosing divergence) query both. If it turns out we don't need it,
dropping a map at M11 is one file edit; dropping a field would be a refactor of
every `Type` constructor.

**Population discipline.** Helpers in `infer.go` keep both tables in sync:

```go
func (c *checker) freshVarAt(n ast.Node, kind ASTOriginKind) *soltype.Variable {
    v := soltype.NewVariable(c.level)
    c.prov[v] = FromAST{Node: n, Kind: kind}
    return v
}
```

The non-leaf variants are populated by their respective operations:
`FromBoundPropagation` in the propagation step of `constrain`,
`FromInstantiation` in `freshenAbove`, `FromExtrusion` in the extrusion path,
`FromCoalesce` in the coalescing pass. Each of those operations already has a
single well-defined entry point, so adding one `c.prov[newType] = ...` line
inside each is the entire discipline.

The hot constrain/coalesce loops never consult `Prov`; it's only read on error
paths and by LSP/diagnostic consumers, so map-lookup vs. field-read cost is
irrelevant.

## The constraint-generating AST walk (`infer.go`)

Replaces the spike's hand-built IR. Walks real `ast.Expr`/`ast.Stmt`/`ast.Decl`,
generating constraints and recording results in `Info`. Sketch of the
expression cases (mirrors the spike's `typeTerm`, but over `ast`):

- `*ast.IdentExpr` → look the name up in the environment. Value bindings and
  namespace bindings live in separate slots (mirroring the existing checker's
  `Scope.GetValue` vs. `Scope.getNamespace`).
  - **Value binding**: call `instantiate` on the binding's scheme. Two cases:
    a `MonoScheme` (function params, the current-level `let` RHS during its
    own inference, recursive self-references inside `LetRec`/`LetRecGroup`
    before generalization) is returned as-is — no freshening; a `PolyScheme`
    (top-level `let`/`fn` decls, inner `let`s after their RHS finishes
    inferring and generalization has run) triggers `freshenAbove` and produces
    a `FromInstantiation` entry in `Prov`. The call to `instantiate` is
    uniform; only the `PolyScheme` case actually does work.
  - **Namespace binding in value position is an error.** Namespaces are a
    separate sort — they do not appear in `soltype.Type` and cannot flow
    through value-position expressions. A bare `IdentExpr` that resolves only
    to a namespace produces a `NamespaceUsedAsValueError`. Namespaces are
    legal only as the receiver of a `MemberExpr` (`ns.foo`) and as the prefix
    of a qualified name in type position (`let x: ns.Foo = ...`); both paths
    handle the lookup separately from value-position inference. See the
    `MemberExpr` case below.
  - **Unknown identifier**: if neither slot has a binding, emit
    `UnknownIdentifierError`.
- `*ast.FuncExpr` → fresh var per param (or annotated type via `TypeAnn`),
  infer body, build `Function`; `mut` record params get a fresh lifetime
  (`attachParamLifetimes`).
- `*ast.CallExpr` → `constrain(callee <: Function{args, fresh result})`.
- `*ast.MemberExpr` (read) → two paths depending on the receiver:
  - **Namespace-qualified access**: if the receiver is a bare `IdentExpr` (or
    a chain of them) that resolves to a `NamespaceBinding`, look the property
    up directly in the namespace's directory. A value member becomes
    `instantiate(scheme)` (same rule as `IdentExpr` value-position lookup); a
    sub-namespace member produces a nested namespace lookup; a type member is
    only legal in type position and rejects here. No constraint is emitted —
    the result type comes from the binding itself.
  - **Value-typed receiver**: the usage-inference path —
    `constrain(recv <: Record{field: fresh})`. Applies to every receiver that
    isn't a namespace chain (locals, params, results of calls, etc.).
- assignment to a member (write) → `constrain(recv <: Ref{mut: true, lt:
  freshLt, inner: Record{field: widen(v)}})` and record the written field type
  for read-after-write. The lifetime is a **fresh variable**, not `nil`: a
  `nil` lt on the target would mean "owned mutable slot," which the `Ref` rule
  rejects when the source is a borrow (the "borrow source into owned slot:
  reject (escape)" branch). The fresh var lets the receiver be either an owned
  mutable value or a mutable borrow of any lifetime; the write itself imposes
  no lifetime obligation on the receiver.
- `*ast.ObjectExpr`/`*ast.TupleExpr` → `Record`/`Tuple` over inferred elems.
- conditionals/branches → join branch types; for borrowed records, union their
  lifetimes via a fresh join lifetime var.
- Every node: `info.setType(node, result)`.

Module-level: drive declaration order via the existing `dep_graph` (SCC order,
matching how the old checker handles mutual recursion), generalizing at
binding boundaries.

## Scope / Binding (own, not `type_system`)

A `Scope` of `Binding`s owned by the new package, structurally similar to the
old checker's but over `soltype`. This is what the compiler reads back. During
the differential phase the compiler holds either the old or the new scope behind
the flag (a small interface or two fields on `CheckOutput`).

**Three sorts of binding, three slots.** Scope lookup is multi-sorted:

```go
type Scope struct {
    values     map[string]ValueBinding      // schemes (Mono | Poly)
    types      map[string]TypeBinding       // type aliases, class types
    namespaces map[string]*Namespace        // not a soltype.Type — separate sort
    parent     *Scope
}

func (s *Scope) GetValue(name string) *ValueBinding         { ... }
func (s *Scope) GetType(name string) *TypeBinding           { ... }
func (s *Scope) GetNamespace(name string) *Namespace        { ... }
```

Each call site picks the slot it cares about: value-position `IdentExpr`
queries `GetValue` then `GetNamespace` (the latter only to detect the
namespace-used-as-value error case); type-position name resolution queries
`GetType` then `GetNamespace` for qualified-prefix walks; `MemberExpr` over a
namespace-receiver walks `Namespace` directly.

**Namespaces are not values.** `Namespace` does not appear in `soltype.Type`
and never participates in `constrain`/`coalesce`. Survey of `fixtures/` and
in-line test sources (May 2026) found **no** Escalier code that aliased a
namespace to a variable or passed one to a function, so this restriction is
not a breaking change in practice. It avoids the existing `NamespaceType`'s
status as a foreign object in the value-type space — every part of the value
machinery has to special-case it, and none of `Ref`/lifetimes/subtyping have
anything to say about it. Lifting namespaces to their own sort makes the
multi-sortedness honest: namespace members include type bindings (which no
record-typed upper bound can express) and value bindings (whose schemes need
per-use-site instantiation), neither of which fits the value-record model.

Namespaces still have a runtime representation — codegen lowers them to real
JS objects (`buildNamespaceStatements` in
[internal/codegen/builder.go](../../internal/codegen/builder.go)). The
restriction is purely a *type-system* discipline: you address namespace
members only through qualified names known statically.

## Test coverage for M7

Two mechanisms, picked to match the granularity of what each one tests.
**Granular semantics** lives in table-driven `*_test.go` files in the new
checker package (the spike's existing pattern); **real-package regression**
lives in the existing `fixtures/` tree with a second harness that runs the
new checker. The two together replace what an earlier draft of this doc
called the "conformance corpus" — the corpus's distinctive value (granular
semantic assertions, semantics-not-output, skip-tags for exact-only cases)
is already delivered by these mechanisms without a new authored artifact.

### Granular semantics: `*_test.go` table tests

The spike already uses table-driven Go tests against rendered types and full
error messages. The new checker package continues this pattern — and aligns
on the field shape already used across the existing checker's test suite
(e.g. [internal/checker/tests/let_generalize_test.go](../../internal/checker/tests/let_generalize_test.go),
[internal/checker/tests/pattern_match_test.go](../../internal/checker/tests/pattern_match_test.go)):
`map[string]struct{...}` keyed by test name, with `input` for the source and
`expectedValues` / `expectedTypes` (each `map[string]string`) keying binding
names to rendered types. Errors use `expectedError`.

```go
tests := map[string]struct {
    input          string
    expectedValues map[string]string
    expectedTypes  map[string]string
    expectedError  string
}{
    "TopLevelLetPolymorphism": {
        input: `val id = fn (x) { return x }`,
        expectedValues: map[string]string{
            "id": "fn <T0>(x: T0) -> T0",
        },
    },
    "TypeAliasAndValue": {
        input: `
            type Pair<T> = {fst: T, snd: T}
            val p: Pair<number> = {fst: 1, snd: 2}
        `,
        expectedTypes: map[string]string{
            "Pair": "<T0> {fst: T0, snd: T0}",
        },
        expectedValues: map[string]string{
            "p": "{fst: 1, snd: 2}",
        },
    },
    "AnnotationMismatch": {
        input:         `val x: number = "hello"`,
        expectedError: `<full error message asserted, per CLAUDE.md>`,
    },
}
```

Authored against intended semantics, **not** copied from the old checker.
Where the new checker improves (e.g. `unknown` vs vacuous `<T0>`), the test
asserts the improved form. Dozens or hundreds of these per language-feature
test file; zero per-case package overhead.

The two-map split is load-bearing: Escalier value bindings and type bindings
live in separate scope slots ([scope discussion](#scope--binding-own-not-type_system))
and a test asserting "the type alias `Foo` rendered as X *and* the value `Foo`
rendered as Y" needs both keyed independently. Tests that touch only one slot
omit the other map entirely.

### Real-package regression: second fixture harness

`fixtures/` already encodes whole-package behavior — `package.json` +
`lib/*.esc` + golden `build/` and optional `error.txt`. M7 adds a **second
harness** that runs the new checker over the same fixtures. Two phases match
the codegen-port decision (M9):

- **Phase 1 (M7, before codegen is ported):** new-checker harness runs the
  checker only — no codegen. Acceptance is "checker accepts (or rejects)
  every fixture the way the old checker does, modulo triaged intended
  improvements." Each divergence is bucketed as match / intended-improvement
  / bug; CI fails only on `bug`. Implemented as a sibling to
  `cmd/escalier/fixture_test.go` that loads the same fixtures and runs only
  the check phase.
- **Phase 2 (post-M9, once codegen is decided):** the harness compiles
  end-to-end and diffs `build/` against fixture goldens using the same
  `UPDATE_FIXTURES=true` flow. At that point both checkers can drive the
  full pipeline.

Why both checkers in Phase 1: the differential aspect is the point. Parse
once, run both checkers on the same `*ast.Module`, compare rendered outputs.
Because the old checker writes into the AST's `inferredType` field and the
new checker writes into its own `Info` side table, the two annotations
coexist without clobbering — the side-table design is exactly what makes
this cheap.

### Exact/inexact-only fixtures: skip tag

Some semantics only make sense for the new checker (exhaustive `match` with
no default arm, rejection of an extra member on an exact target,
`Exact<T>`/`Inexact<T>` reduction). These fixtures need a way to declare
"only the new checker should run me." Pick the cheapest mechanism that fits
the existing fixture format:

- A field in `package.json` (e.g. `"escalier": {"applicable_to": ["new"]}`),
  or
- A magic comment header in the fixture's `lib/index.esc`
  (e.g. `// @applicable_to new`).

Both harnesses read the tag; old-checker harness skips, new-checker harness
runs. Strictly cheaper than a per-entry `expected_old`/`expected_new` split.

### Triage discipline

Differential output for Phase 1 is a triaged report bucketed match /
intended-improvement / bug. The bug bucket is the only CI gate; intended
improvements are recorded (a short note per case is enough — no separate
ledger needed) so future contributors don't mistake them for regressions.

## Compiler wiring (M7)

The flag lives at the **3** `checker.NewChecker(ctx)` sites in
`internal/compiler/compiler.go` (`CheckLib`, `Compile`, `CompilePackage`).
the MVP selects the new checker for *checking only* — codegen continues from the old
checker's output (codegen deferred). Simplest form: an env var or build tag
read once and branched at those three sites.

## What gets deleted at M11 (cleanup)

- `internal/checker/` (old package) + its ~38k LoC of tests.
- `internal/ast/ast.go`: `type Type = type_system.Type`.
- The generated `inferredType` field + `InferredType()`/`SetInferredType()` and
  their generation in `tools/gen_ast`.
- `decl.go`'s public `InferredType` field.
- Result: the AST has no remaining `type_system` imports — `BindingOwner` lives
  in `internal/ast` itself (per Settled Decision #2), so once the
  `inferredType` field and the `type_system.Type` alias are gone, the AST is
  fully type-system-agnostic.

## Settled decisions

1. **Package leaf name** — `internal/solver/` (sibling to `internal/checker/`).
2. **`BindingOwner`** — move the marker interface into `internal/ast` itself
   (rather than living in `type_system`). Both the old and new checkers refer
   to `ast.BindingOwner`; neither package owns it. This also clears one of the
   AST→`type_system` imports that M11 cleanup is targeting.
3. **Codegen path (M9)** — **port codegen onto `soltype`**, not a bridge.
   Driver: the `@escalier-type` JSDoc round-tripping for exactness needs the new
   checker's exactness information end-to-end; a `soltype → type_system`
   bridge would either lose the flag or have to re-encode it on the way back
   out. Porting is the safe choice.
4. **Error representation** — reuse the old checker's `Error`/diagnostic
   types where they apply, and add new diagnostic kinds for the
   exactness-driven errors the new checker introduces (extra-member rejection
   on exact targets, exhaustive-match-without-default, `Exact<T>`/`Inexact<T>`
   misuse, etc.). Table tests assert full messages either way.
5. **Scope sharing in `CheckOutput`** — **parallel fields** during the
   differential phase: `ModuleScope *checker.Scope` and
   `NewModuleScope *solver.Scope` (plus the matching `FileScopes` maps), with
   a flag picking which one is active at each downstream call site. The
   branching is confined to LSP entry points and the codegen driver — a
   small, stable set of sites. At M11 cleanup, drop the old field; no
   interface ever needed.
6. **Exact-by-default is day-one behavior.** Escalier code is
   exact-by-default and TypeScript-imported types are inexact-by-default
   *from the moment each former lands* — exact functions in M3, exact
   records/tuples in M4, exact class-instances-via-`final` in M5, exact
   unions in M6. Tests at each milestone assert what the implementation
   produces, so there's no window in which the code is one default and the
   tests another. No M7 deadline.
7. **Exact-only fixture skip-tag location** — **magic comment header** in
   `lib/index.esc` (e.g. `// @applicable_to new`). More visible than a
   `package.json` field; people rarely open the package.json in a fixture.
