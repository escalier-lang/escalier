# Builtins: Implementation Plan

This plan implements the requirements in
[requirements.md](requirements.md). The structure follows the
[Migration phases](requirements.md#migration-phases) of the
requirements, with one phase per `§` here. Within each phase, work
items list the touch points in the existing codebase and the gate
that proves the phase is done.

## Implementation order and status

Status legend: ✅ done, 🚧 partial, ⬜ not started.

| §   | Phase                                                | FRs         | Status | Depends on | Notes                                                                                                                                                                                                                                                |
| --- | ---------------------------------------------------- | ----------- | ------ | ---------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Declaration-printer audit                            | FR14        | ✅      | —          | Audit test lives in [internal/printer/print_decl_audit_test.go](../../internal/printer/print_decl_audit_test.go); every in-scope form round-trips. Notes on converter-side syntax decisions below.                                                  |
| 2   | URI-scheme imports + binding-shape flags             | FR2–FR5     | ✅      | §1         | Parser, resolver, both binding shapes, single-class shortcut, and the `--stdlib-dir` flag (+ env var, sibling-to-exe, repo-relative discovery) all landed. Gate satisfied via `std:math` and `std:array` stubs; unit + fixture coverage in place. One follow-up deferred to §7 — the FR5 "non-class package exports as namespace members on the same binding" surface. |
| 3   | Codegen lowering and `@js` decorators                | FR3         | ✅      | §1         | Decorator parser, `@js` codegen lowering, and loader rules §3.4(1-4) all landed. The §3.5 fixtures that need `std:number` / `std:iterator` stubs (`parseInt`, Symbol re-export, package-private invisibility) moved to §7 where the stubs live.                            |
| 4   | Single `web:dom` package + inter-package imports     | FR6, FR7 (deferred), FR8, FR9 (deferred) | ✅ | §2 | SCC-aware pseudo-package loader (`internal/checker/infer_stdlib_scc.go`) permits cycles among `std:`/`web:` packages (§4.3); §4.4 gate fixtures (closed-registry `keyof T` / `T[K]` narrowing, NS-keyed overloads, cross-package qualified type references, std↔std / web↔web / web↔std cycles, decorator-error URI labels, rollback) pass in `internal/checker/tests/stdlib_import_test.go`. MVP collapses the entire DOM tree (HTML/SVG/MathML/CSSOM/observers/events/…) into one `web:dom` package with closed registries; standalone web APIs (Fetch, Streams, Crypto, Workers, WebGL, …) get sibling `web:*` packages that thread `web:dom` types through via qualified references (§4.2). Well-known symbols stay on `Symbol`; domain packages re-export aliases (FR8). FR7 (per-file cross-package augmentation) and FR9 (its activation semantics) are deferred to a future custom-elements workstream; §4.1 records the spike conclusions. §4.6 (method-elem overload resolution on class/interface declarations) landed via PR-A (#652), PR-B (#653), and PR-C (#656); the NS-keyed-overloads gate fixture is now declared as methods on a `Document` class, matching the shape the real DOM needs. Inheritance + `implements` overload merging is deferred to [#651](https://github.com/escalier-lang/escalier/issues/651). |
| 5   | Converter MVP (`tools/dts_to_esc/`)                  | FR10        | ⬜      | §1, §3     | Two tiny slices: a trio-idiom class (`Boolean`) and a small `declare namespace` block (e.g. `JSON`). AST-to-AST translation; emit to stdout; no partition logic. Exercises trio recognition + namespace flattening; emits `@js` decorators per §3. |
| 6   | Converter productionization                          | FR10        | ⬜      | §5         | Partition table; full output paths under `internal/interop/data/{std,web}/`; `--check` mode; full `lib.*.d.ts` input set; registry/well-known-symbol routing.                                                                                        |
| 7   | Stdlib bootstrap (committed `.esc` files)            | FR1–FR2     | ⬜      | §6         | Run the converter once; review; hand-edit high-value `throws`, lifetimes, mutability; commit. (§4.6 prerequisite for same-named method dispatch — `createElement`, `addEventListener`, `getContext`, … — landed with §4.)                                                                                                                                                                                                                                                                                            |
| 8   | Internal fixture migration                           | (precedes §9) | ⬜ | §4, §7    | Migrate Escalier's own fixtures to `import "std:*"`. Must land **before §9** so the prelude switchover doesn't break the test suite. Requires §7 because the imports resolve against the committed `.esc` files; requires §4 for any fixture that touches inter-package imports / the single-`web:dom` package + cross-package type references. The legacy prelude still resolves previously-ambient names side-by-side until §9 deletes it. |
| 9   | Prelude switchover + override deletion               | FR11, FR12  | ⬜      | §2, §4, §7, §8 | Replace `lib.*.d.ts` walking in [prelude.go](../../internal/checker/prelude.go) with the per-file shape loader. Delete the legacy `BuildBuiltinStore` / `loadGlobalDefinitions` / `populateSelfParams` / `UpdateMethodMutability` / `mergeReadonlyVariant` / `mutabilityOverrides` paths in the same PR — pre-1.0, no deprecation cycle. Also migrate loader rule §3.4(4) (`@js` arg validation): move it out of the loader (currently reads `GlobalScope.Namespace.Values` in [js_globals.go](../../internal/checker/js_globals.go)) into a CI-only test under [internal/checker/tests/](../../internal/checker/tests/) that freshly parses the pinned `lib.*.d.ts` and validates every `@js("...")` arg across the committed stdlib. Delete `js_globals.go` and the rule-4 branch in [js_decorator.go](../../internal/checker/js_decorator.go) in the same PR. Same CI-only test should add **rule §3.4(5): `@js` decl shape matches lib target** — locate the lib member named by each `@js("...")` and assert: `readonly` / getter-only lib member ⇒ Escalier decl is `val` (or `get`), never `var`; setter-only ⇒ `set`; method ⇒ `fn`. Catches stdlib stubs that silently make readonly things look writable (today `@js("Math.PI") export declare var PI: number` compiles and lowers to a `Math.PI = ...` that TypeErrors at runtime). Rule 5 shares the lib parser with rule 4, so doing them separately would duplicate the parse. |
| 10  | Intrinsics, adaptive rendering, LSP support          | FR13, FR15, FR16 | ⬜ | §9      | Implement adaptive diagnostic rendering (FR15) and the auto-import quick-fix (FR16); verify `Awaited<T>` source-level definition with documented-fallback policy; confirm intrinsic handlers stay checker-resident (FR13).                                                                                                          |

**Dependency graph** (edges are "must land before"; only direct
edges shown — transitive deps omitted for clarity):

```
                  ┌─ §2 ── §4 ──────────────┐
§1 (audit) ───────┤                         ├── §8 ── §9 ── §10
                  └─ §3 ── §5 ── §6 ── §7 ──┘
```

Two lanes diverge from §1 and reconverge at §8: the upper lane
(§2 → §4) builds the resolver and inter-package import
machinery (including the cycle handling needed for sibling
`web:*` packages that mutually reference `web:dom`); the lower
lane (§3 → §5 → §6 → §7) builds the decorator parser, the
converter, and the committed `.esc` files. §8 needs both lanes
— fixtures import the `.esc` files via the resolver, and any
cross-package-typed fixture relies on §4.2b qualified type
references. §9 cuts over the prelude; §10 adds LSP/diagnostic
tooling on top.

**Step ordering rationale.** §1 is first because a failed audit
forces parser work that gates everything else. §2 (resolver),
§3 (decorator parser + codegen lowering), and §5 (converter MVP)
have no ordering dependency on each other after §1 — they share
no internal dependency beyond the audit, so the implementer is
free to land them in any order (or interleave). §3 must land
before §5 lands its decorator
emission step; §3 must also land before any fixture exercises
codegen end-to-end. §4 lands after §2 because the inter-package
import / cycle tests need real `import` statements. §7 produces
the source-of-truth
`.esc` files. §8 migrates internal fixtures to `import "std:*"`
while the legacy prelude is still live; §9 then swaps the prelude
and deletes the legacy paths in a single cut (pre-1.0, no
deprecation cycle); §10 adds the LSP / diagnostic tooling on top.

**Why §8 precedes §9.** §9 deletes the legacy path in the same
change that swaps the prelude, so existing fixtures must already
be migrated to `import "std:*"`. §8 is feasible once §7 has
committed the `.esc` files (so imports actually resolve) and §4
has landed (so any DOM-touching fixture can use the
single-`web:dom` package + cross-package type references); the
resolver from §2 is in place transitively via §7. The legacy
prelude still resolves previously-ambient names side-by-side
during the fixture-rewriting commit, then §9 removes it. §10
(LSP, adaptive rendering, intrinsic verification) has no
ordering constraint relative to §9 other than building on the
post-switchover code.

---

## §1. Declaration-printer audit (FR14)

**Goal.** Establish, before writing the converter, that every
declaration form the converter needs to emit round-trips through
parser + printer.

**Scope.** Mirror the prior `type_system.Type` audit
([print_type_audit_test.go](../../internal/type_system/print_type_audit_test.go))
but for declaration-level forms:

- `declare class` (including generic parameters and `extends`)
- `declare fn` (including generic constraints, overloads, optional
  / rest parameters, `this` parameter)
- `declare type <alias>` (including conditional types referring to
  `infer T`, mapped types with `as` rename clauses, intersections,
  unions, indexed access)
- `declare var` / `declare val`
- Open `interface` declarations and interface merging
- **Decorator syntax** (`@js("...")`) on every
  decorator-eligible declaration form, in combination with the
  `export` modifier — see §3.3 for the grammar. Decorators are
  new to the parser; the audit confirms the lexer, parser, AST,
  and printer round-trip `<decorators> export declare <kind>`
  (the canonical pseudo-package shape) and reject decorators
  placed between `export` and `declare`.
- Ambient module syntax (`declare module "..."` is *out* of scope —
  pseudo-packages are files, not nested ambient modules)

**Explicitly not in the audit:** `declare namespace`. Per FR10
step 2, the converter flattens TS `declare namespace` blocks into
top-level declarations in the output `.esc` file. The printer
does not need to emit nested namespace syntax.

**Work items.**

1. Write `TestPrintDeclAudit_RoundTrip` (parallel to
   `TestPrintTypeAudit_RoundTrip`) covering every declaration form
   listed above. Source-input form, print, re-parse, double-print
   idempotency.
2. For each variant that fails to round-trip, file a follow-up to
   extend the parser or printer; gate §5 on those follow-ups
   landing.
3. Document any decisions ("TS form X is mapped to Escalier form
   Y by the converter") in a short section of this file, since
   they constrain the converter implementation.

**Touch points.**

- [internal/parser/decl.go](../../internal/parser/decl.go)
- [internal/printer/](../../internal/printer/)
- [internal/type_system/print_type.go](../../internal/type_system/print_type.go)

**Gate.** All `TestPrintDeclAudit_*` tests pass; the audit
section below lists any unsupported forms with their
follow-up work.

**Audit results.** The audit lives in
[internal/printer/print_decl_audit_test.go](../../internal/printer/print_decl_audit_test.go).
`TestPrintDeclAudit_RoundTrip` covers every form in the §1 scope
including `declare class`, constrained type parameters (`<T: U>`),
and `@js(...)` decorators on every decoratable decl kind, all of
which the initial pass identified as gaps. Three follow-ups
landed alongside the audit to close those gaps:

- **`<T: U>` constraint printing.** `printTypeParams` in
  [internal/printer/printer.go](../../internal/printer/printer.go)
  now emits `: U` instead of the TypeScript `extends U`.
- **`declare class` printer.** Added `printClassDecl` and
  `printClassElem` in
  [internal/printer/printer.go](../../internal/printer/printer.go);
  emits fields, methods (with `self`/`mut self` receivers),
  getters/setters, and constructors.
- **`@js(...)` decorator parsing and printing.** New `AtSign`
  token in
  [internal/parser/token.go](../../internal/parser/token.go) and
  [internal/parser/lexer.go](../../internal/parser/lexer.go); new
  `ast.Decorator` node
  ([internal/ast/decorator.go](../../internal/ast/decorator.go))
  with a `Decorators []*Decorator` field added to `VarDecl`,
  `FuncDecl`, `TypeDecl`, `InterfaceDecl`, and `ClassDecl`;
  parsing in
  [internal/parser/decl.go](../../internal/parser/decl.go) `Decl()`
  collects leading `@name(args...)` decorators and attaches them to
  the parsed decl (decorators between `export` and `declare` are a
  parse error per §3.3); printer emits each decorator on its own
  line above the modifiers.

Intentionally out of scope (no follow-up):

- **`declare namespace`.** Per work-item scope, the converter
  flattens TS `declare namespace` blocks into top-level
  declarations; the printer never needs to emit nested namespace
  syntax.
- **`declare module "..."`.** Pseudo-packages are files, not
  ambient nested modules. The parser accepts the form; the audit
  does not require print round-trip.

Converter-side decisions taken during the audit (these constrain
the §5 / §6 converter implementation):

- TS `T extends U ? A : B` maps to Escalier `if T : U { A } else { B }`.
- TS `T extends Array<infer U> ? U : never` maps to
  `if T : Array<infer U> { U } else { never }`.
- TS `{ [K in keyof T]: T[K] }` maps to
  `{[K]: T[K] for K in keyof T}` (Escalier's `for ... in` form);
  rename clauses use the bracket-name slot, e.g.
  `` {[`prefix_${K}`]: T[K] for K in keyof T} ``.
- Interface methods print as `name(self, ...) -> R` (and
  `name(mut self, ...)` for mutating methods); getters/setters
  use the leading `get`/`set` modifier with a `self` receiver.

---

## §2. URI-scheme imports + binding-shape flags (FR2–FR5)

**Goal.** End-to-end resolution and scope-binding for
`import "std:math"`, including the three binding-shape flags and
the single-class shortcut.

### 2.1 Parser change

- Accept **bare-string imports** with no binding clause:
  `import "std:math"`. Currently
  [internal/parser/decl.go](../../internal/parser/decl.go) (look
  for `parseImport` / equivalent) requires either a namespace
  alias or a `{ ... }` clause.
- Accept the `?flag` and `?flag1&flag2` suffix on the
  module-specifier string literal. Preserve the suffix in the
  AST; do not strip it at parse time (the resolver strips it).
- AST: extend `ImportStmt` with a representation that distinguishes
  bare from named/aliased imports, and that carries the parsed
  `?flag` set. Round-trip via the printer.
- **Rejection:** named imports from a scheme-prefixed URI must
  parse (so we can emit a clear semantic error in §2.2) but the
  resolver rejects them. See "Error taxonomy" below.

### 2.2 Resolver change

Touch point:
[internal/checker/infer_import.go](../../internal/checker/infer_import.go)
(`resolveImport`, `resolveExportModulePath`).

- Detect `std:`, `web:`, `node:` schemes before the
  `node_modules/<pkg>` walk. Route them to the **stdlib data
  directory** on disk (resolution scheme below).
- Mapping: `std:math` → `<stdlib>/std/math.esc`;
  `web:http` → `<stdlib>/web/http.esc`. Multi-word packages use
  underscores in both URI and filename
  (`std:typed_arrays` → `std/typed_arrays.esc`,
  `web:web_rtc` → `web/web_rtc.esc`). Hyphens never appear in
  pseudo-package URIs or filenames; there is no `-` → `_`
  substitution at this layer (that rule belongs to the
  third-party workstream).
- Strip the `?flag` portion before path lookup; pass the flag set
  to the binding step.
- `node:` resolves but always errors with "node:* is reserved;
  not yet populated" until Node support lands.

### 2.2a Stdlib data directory resolution

The `.esc` files under `internal/interop/data/` are **loaded
from disk at compile time, not embedded into the binary**.
This keeps them editable by compiler users — adding a new
builtin or tweaking a return type does not require rebuilding
the compiler.

Discovery order, first hit wins:

1. **`--stdlib-dir <path>` CLI flag** on `escalier check` /
   `escalier build` / `lsp-server`. Highest precedence
   (standard CLI convention: explicit flags beat ambient
   configuration).
2. **`ESCALIER_STDLIB_DIR` environment variable.** Absolute
   path to a directory containing `std/` and `web/`
   subdirectories. Used only when `--stdlib-dir` is not
   supplied. Intended for contributors testing alternative
   stdlibs and for tooling that ships its own.
3. **Sibling to the executable.** `<exe-dir>/../share/escalier/data/`
   (Unix convention; resolves on the typical
   `bin/`+`share/` install layout). Falls back to
   `<exe-dir>/data/` for single-directory installs.
4. **Repo-relative.** When the binary is run from a build tree
   (detected by walking up for a `go.mod` whose module path
   matches the Escalier module), use `internal/interop/data/`
   relative to the repo root. Makes `go run ./cmd/escalier`
   work without setup.

If none resolve, emit a fatal startup error pointing at the
discovery order and the `ESCALIER_STDLIB_DIR` env var. The
error is **not** a per-file diagnostic — it fires before any
user file is parsed.

**User customization.** Users who want a tweaked stdlib copy
the install's `share/escalier/data/` tree to a writable
location, edit the `.esc` files, and point
`ESCALIER_STDLIB_DIR` at the copy. No recompile of the
compiler. Adding a new builtin is just a new `.esc` file in
the appropriate subdirectory; the resolver picks it up on
next compile.

**`node/` directory.** Created empty in the source tree with a
`README.md` explaining the reserved scheme. No empty-pattern
constraint to satisfy — the loader simply reports
"node:* is reserved" when asked.

Touch point: a new `internal/interop/stdlib_dir.go` (or
similar) hosts the discovery logic; the resolver in
[infer_import.go](../../internal/checker/infer_import.go) calls
it lazily on first scheme-prefixed import.

### 2.3 Binding-shape application

- Implement the flag rules from FR4. Each pseudo-package import
  contributes a binding entry under `?local` (the only shape today):
  binding name = lowercased last URI segment (or capitalized class
  name when the single-class shortcut FR5 fires).
- Internal bookkeeping (whether a file has loaded a package's
  declarations) keys on the package's full URI (`web:fetch`),
  independent of binding-shape flag.
- **Extensible flag slot.** The grammar reserves the `?flag` /
  `?flag1&flag2` shape for future flags (`?type-only`, `?lazy`,
  …). Unknown flags currently error per the taxonomy; the
  resolver factors flag recognition into a per-flag table so
  future flags slot in without restructuring. **Note:** the
  earlier `?nested` shape (bound under `<scheme>.<package>`) was
  removed once it became clear the dep_graph's cycle detection
  only matched canonical `<pkg>.<name>` binding keys; cross-stdlib
  collisions can be addressed later via file-local renaming.

### 2.4 Single-class shortcut (FR5)

- Detect activation: the package declares a top-level class whose
  name matches the lowercased last URI segment case-insensitively.
- When active **and** the import is `?local`, bind the class name
  with its original capitalization (`Array`, `Date`, `Promise`).
  Other package exports remain accessible as namespace members on
  the same binding.
- `Array.isArray(xs)`, `Array<number>` (type position),
  `Array(5)` (construct, no `new`).
- Static methods on the class take precedence over namespace
  members on name collision.

### 2.5 Tests and gates

- **Parser tests.** Bare-string imports with and without `?flag`,
  including `?flag1&flag2`. Round-trip via the printer.
- **Resolver tests.** Each scheme; unknown scheme; known scheme +
  unknown package; `?flag` stripping. Place the
  `internal/interop/data/std/math.esc` stub with
  `let PI: number = 3.14` so end-to-end resolution has something
  to find. Stdlib-discovery tests cover all four discovery
  paths from §2.2a (env var, `--stdlib-dir`, sibling-to-exe,
  repo-relative) plus the "no stdlib found" fatal error.
- **Binding-shape fixtures** under [fixtures/](../../fixtures/):
  one per shape, plus the mutually-exclusive error case.
- **Single-class shortcut fixture:** `std:array` stub declaring
  `Array<T>`; assert `Array.isArray`, `Array<number>` type
  position, and `Array(5)` construct all bind correctly.

**Gate.** Stub `std:math` resolves end-to-end with both
binding-shape flags; flag collision errors. Single-class
shortcut works for the `std:array` stub.

**Deferred to later sections.** One item from §2.5 requires
material that does not yet exist:

- The **FR5 "non-class package exports as namespace members on
  the same binding"** surface is not implemented yet — the
  current `std:array` stub has only the class. The shortcut
  itself works; merging companion exports onto the class binding
  is wired up in §7 once real packages pair a class with helpers.
  A TODO marker lives in
  [bindStdlibLocal](../../internal/checker/infer_stdlib_import.go).

---

## §3. Codegen lowering and `@js` decorators (FR3)

**Goal.** Lower references to pseudo-package members to the
correct JS runtime expression, and erase pseudo-package `import`
statements at codegen. The lowering mapping is carried by
per-declaration `@js` decorators inline in the pseudo-package
`.esc` source.

Pseudo-package imports are **type-system-only, runtime-erased**.
The codegen drops `import` statements whose specifier carries a
`std:`, `web:`, or `node:` scheme before emitting JS. Zero
import-line artifact.

References to pseudo-package members must still lower to the
correct JS runtime expression, and the Escalier-side binding name
is not generally the JS-side name (`math.sin(x)` → `Math.sin(x)`;
`parseInt(s)` from `std:number` → bare `parseInt(s)`;
`iterator.iteratorKey` re-export → `Symbol.iterator`; etc.). The mapping
is carried by **per-declaration `@js` decorators** authored
inline in the pseudo-package `.esc` source.

### 3.1 `@js` decorator semantics

Every **exported** top-level declaration in a pseudo-package
`.esc` file carries an `@js` decorator whose argument is the JS
expression that the declaration lowers to. Pseudo-package files
follow the regular Escalier module rule: visibility outside the
file requires explicit `export`, and only exported declarations
participate in the package's namespace. Internal helper
declarations (used only inside the file) are not exported and
carry no `@js`. Examples:

```escalier
// std/math.esc
@js("Math.sin")
export declare fn sin(x: number) -> number

@js("Math.PI")
export declare val PI: number

// std/number.esc — hoisted globals share a package with Number
@js("parseInt")
export declare fn parseInt(s: string, radix?: number) -> number

@js("Number")
export declare class Number { … }

// std/iterator.esc — Symbol re-export
@js("Symbol.iterator")
export declare val iteratorKey: unique symbol

// std/array.esc — single-class shortcut package
@js("Array")
export declare class Array<T> { … }

// std/async.esc — package-private helper, no export, no @js
declare type Thenable<T> = { then(onfulfilled: (v: T) => void): void }
```

There is no package-level default. Every exported declaration is
annotated explicitly. The converter (§5–§6) emits `export` and
`@js` on every declaration it produces; hand-authored
declarations at §7 (`Symbol.customMatcher`, Symbol re-exports,
etc.) write both keywords explicitly. The loader rejects an
exported declaration missing `@js` (§3.4); an unexported
declaration with `@js` is also rejected as nonsensical
(the decorator only matters at codegen sites, which only see
exported names).

### 3.2 Lowering rules

- **Member access through a package binding** (`math.sin(x)`,
  `std.math.sin(x)` under `?nested`) collapses to the underlying
  declaration's `(package, name)`
  identity and lowers to that declaration's `@js` expression
  applied to the call's arguments. Binding shape is purely an
  Escalier-side concern; codegen never sees it.
- **Single-class shortcut bindings** (`Array`, `Date`, …) resolve
  to the class declaration and lower via its `@js` decorator.
  `Array.isArray(xs)` lowers to `Array.isArray(xs)` via the class
  declaration's `@js("Array")` decorator plus the static member.
- **`@js` arguments are JS expressions, not just identifiers.**
  Dotted forms like `"Math.sin"`, `"Symbol.iterator"` are valid;
  the codegen pastes them in textually. This keeps the
  representation tiny — no parsed JS-side AST needed for the 99%
  case.
- **Class construction with `new`** is **not** carried by `@js`.
  The checker knows whether a callable is a class; the codegen
  inserts `new` at the construction site based on the callee's
  type, regardless of how the class declaration's `@js` is
  spelled. So `Date()` in Escalier lowers to `new Date()` even
  though the decorator just says `@js("Date")`.

### 3.3 Parser dependency

The Escalier parser does **not** currently support decorator
syntax. This phase adds it:

- Lex `@<ident>` as a new decorator-introducer token.
- Parse a decorator as `@ident(<arg>)` where `<arg>` is, for
  this workstream, a single string literal. The grammar leaves
  room for richer decorator arguments in the future (named args,
  identifier args) without committing to them now.
- **Placement.** Decorators sit **above** any modifier keywords
  on the declaration they target. The canonical ordering is
  `<decorators> export declare <kind> ...`:
  ```escalier
  @js("Math.sin")
  export declare fn sin(x: number) -> number
  ```
  Decorators between `export` and `declare` are a parse error.
  Multiple decorators on one declaration stack top-to-bottom;
  ordering preserved for printer round-trip.
- Decorators are allowed on the **value-introducing** decl kinds
  — `declare fn`, `declare class`, `declare val` / `declare var`
  — in both exported and unexported positions, with the loader
  rule in §3.4 catching unexported declarations carrying `@js`
  in pseudo-package files. Decorators are **rejected at parse
  time** on `declare type` and `declare interface` because those
  forms erase at codegen and have no runtime reference for `@js`
  to lower; the parser reports the error at the decorator's span
  ("decorators are not allowed on type/interface declarations
  (type aliases / interfaces have no runtime form)"). Decorators
  are also disallowed on inner declarations (members,
  parameters) — out of scope for this workstream; revisit if a
  concrete need surfaces.
- Printer round-trips decorators (FR14 audit must cover them,
  in combination with the `export` modifier).

Touch points:
[internal/lexer_util/](../../internal/lexer_util/),
[internal/parser/decl.go](../../internal/parser/decl.go),
[internal/ast/](../../internal/ast/) (new `Decorator` AST node
and field on declarations),
[internal/printer/](../../internal/printer/).

The decorator grammar must land in the FR14 audit scope (§1) so
the converter (§5) can rely on round-trip behavior.

### 3.4 Codegen

Touch point:
[internal/codegen/](../../internal/codegen/) — at every
pseudo-package symbol reference, resolve the binding to the
underlying declaration, read its `@js` decorator, and emit the
decorator's argument as the JS expression. Import statements
carrying a `std:`/`web:`/`node:` scheme are dropped (no JS
output).

**Loader rules** (enforced after `.esc` parse, before
type-check):

1. Every **exported** top-level declaration in a pseudo-package
   file must carry an `@js` decorator. Missing `@js` is an
   internal-compiler-error naming the file and declaration.
2. An **unexported** top-level **value-level** declaration
   (`declare val`, `declare var`, `declare fn`, `declare class`)
   in a pseudo-package file is **rejected**. Pseudo-package
   files exist to expose runtime-visible JS surface; an
   unexported value-level declaration has no runtime mapping
   and is invisible to importers — almost certainly a typo
   (someone forgot `export`). Error message tells the user to
   add `export` (and `@js`).
3. An unexported **type-level** declaration (`declare type`,
   unexported `interface`) is allowed and must not carry `@js`
   — purely a checker-internal helper, no runtime presence.
4. **`@js` target validation.** The argument of every `@js`
   decorator is checked against the set of known JS globals
   extracted from the pinned TypeScript `lib.*.d.ts`. A typo
   like `@js("Mat.sin")` errors at load time with the file,
   declaration, and the decorator's argument named in the
   diagnostic. The extraction is mechanical: walk
   `internal/dts_parser/` output for top-level names + their
   members, materialize the dotted-path set once at compiler
   startup, and check decorator arguments against it.
   Hand-authored Escalier-specific names not in TS lib
   (`Symbol.customMatcher`) are listed in a small allow-list
   alongside the loader.

All four rules apply only to files under the resolved stdlib
data directory (§2.2a). User code is free of these constraints.

### 3.5 Gates

- Parser round-trips `@js("...")` decorators above `export
  declare` on every decorator-eligible declaration form (folded
  into the FR14 audit, §1). Decorator between `export` and
  `declare` is a parse error.
- Codegen fixture under [fixtures/](../../fixtures/) covers:
  - Namespace member: `math.sin(x)` → `Math.sin(x)`.
  - Single-class shortcut: `Array.isArray(xs)` →
    `Array.isArray(xs)`; `Date()` (construct) → `new Date()`.
  - Binding-shape independence: the same call lowers identically
    under `?local` and `?nested`.
  - The `parseInt`, `Symbol.iterator`, and package-private
    invisibility fixtures need hand-authored `std:number` /
    `std:iterator` stubs; they live with §7's stdlib bootstrap
    rather than blocking §3.
- Loader checks fire on (a) an exported pseudo-package
  declaration missing `@js`, and (b) an unexported pseudo-package
  declaration carrying `@js`. Both are negative tests.
- Generated JS contains no scheme-prefixed `import` lines.

---

## §4. Single `web:dom` package + inter-package imports (FR6, FR7 deferred, FR8, FR9 deferred)

**Goal.** Inter-package imports between pseudo-packages work,
including cycles. The entire DOM tree (Document, Element, every
HTML/SVG/MathML element class, CSSOM, observers, animations,
events, custom elements, etc.) lives in a **single `web:dom`
package** with all its registries (`HTMLElementTagNameMap`,
`SVGElementTagNameMap`, `MathMLElementTagNameMap`,
`HTMLElementEventMap`, …) declared closed alongside the methods
that key on them. Standalone web APIs that ship in
`lib.dom.d.ts` without DOM coupling (Fetch, Streams, Crypto,
Workers, WebGL, Web Audio, WebRTC, WebCodecs, IndexedDB, Service
Workers, WebSocket, Storage, URL, Encoding, File, Performance,
WebAuthn, Payments) occupy sibling `web:*` packages and reference
`web:dom` types via qualified names. Cross-package augmentation
is sidestepped entirely. (Well-known symbols live on `Symbol`'s
static side in `std:symbol`; domain packages re-export them as
plain aliases per FR8, no augmentation machinery involved.)

**FR7 / FR9 are deferred for MVP.** True per-file cross-package
augmentation — the original §4.2 design where `web:canvas`
augmented `web:dom`'s `HTMLElementTagNameMap`, as specified in
[requirements.md FR7](requirements.md#fr7-dom-packaging-cross-package-type-references-open-augmentation-deferred)
and [requirements.md FR9 (deferred spec in appendix)](requirements.md#appendix-deferred-fr9-spec) —
is **not** implemented. The §4.1 spike found that achieving FR9
(per-file activation) would require two distinct new pieces of
checker machinery (per-file composition layer + call-site
re-resolution of `keyof T` / `T[K]`), neither of which is a small
wrapper around existing code. Rather than build those subsystems,
the partition is restructured so cross-package augmentation is
not needed at all: the DOM is one cohesive package, and the only
cross-package coupling is the standalone-web-API edge cases that
qualified type references handle cleanly.

### 4.1 Spike findings (informing the MVP punt)

The §4.1 spike was originally a gate on the §4.2 augmentation
implementation. Its output reshaped §4 instead: the work is
deferred and §4.2 is rewritten around closed registries. The
findings below remain the authoritative reference for what true
cross-package augmentation would require if a future workstream
(custom elements; third-party DOM extensions) needs it.

The prototype staged a two-file stdlib (`web/dom.esc` +
`web/canvas.esc`) under a temp `ESCALIER_STDLIB_DIR`: `web:dom`
declared an empty `HTMLElementTagNameMap` registry and a generic
`createElement<K: keyof HTMLElementTagNameMap>(tag: K) ->
HTMLElementTagNameMap[K]`; `web:canvas` declared
`HTMLCanvasElement` and a same-named `HTMLElementTagNameMap`
with a `canvas: HTMLCanvasElement` member, intending to augment
the registry. The scenarios below were run against that staging.
The spike code has since been removed; the conclusions are what
matter.

**Q1: Can the existing `internal/interop/` merge primitive be
parameterized by a per-importing-file active augmentation set?**
**No.** Two findings:

1. The interop `Merge`/`Collapse` pipeline
   ([merge.go](../../internal/interop/merge.go)) operates at a single
   global collapse point producing one static `OverrideStore`; it
   has no notion of a per-importer view.
2. The Escalier-internal interface merge in
   [infer_module.go:1026-1052](../../internal/checker/infer_module.go)
   merges only *within the same namespace* (mutates a shared
   `ObjectType.Elems`). Two `interface HTMLElementTagNameMap { … }`
   declarations in two different `.esc` packages produce two
   **distinct, unrelated** type aliases today — under `?nested`
   imports, `web.dom.HTMLElementTagNameMap["canvas"]` and
   `web.canvas.HTMLElementTagNameMap["canvas"]` are separate types.

If we wanted the per-element-family split, §4.2 would need
**new** machinery (not a reuse of interop's merge): a
per-importing-file pass that, given F's resolved scheme-imports,
builds a composed view of each registry interface by collecting
all contributing declarations across the active set. The MVP
sidesteps the requirement by collapsing the DOM tree into a
single `web:dom` package — see §4.2.

**Q2: Does indexed access (`HTMLElementTagNameMap[K]`) re-resolve
against the per-file augmentation set, or snapshot at
registry-declaration time?**
**Snapshot.** With both packages imported,
`createElement<K: keyof HTMLElementTagNameMap>(tag: K) -> …`
resolves its bound `keyof HTMLElementTagNameMap` at *declaration
time inside `web:dom`*, against the empty registry — yielding
`K: never`. The caller's import set has no effect: both the
importer-with-canvas scenario and a sibling file that imports
only `web:dom` reject `createElement("canvas")` with the
identical `"canvas" cannot be assigned to never`.

Even if Q1's per-file merge machinery existed, `createElement`'s
constraint would still be the snapshot. The deferred augmentation
workstream would additionally need to teach the indexed-access /
`keyof` machinery to re-resolve against the caller-file's active
augmentation view — or rewrite registry-keyed APIs to thread the
merged registry in differently (e.g. by defining them in a way
that the merged view substitutes through). MVP avoids both by
declaring the registries closed inside `web:dom`, where the
snapshot is against the populated map and Just Works (§4.2).

**Implications for §4 sizing.** Both gates failed; rather than
take on the new checker machinery, §4 is reshaped around a
**single `web:dom` package** with closed registries (see §4.2).
The augmentation work is recorded here for the deferred
custom-elements workstream:

- **(Deferred) per-file composition layer:** would need new
  loader/composition code. Likely its own PR.
- **(Deferred) call-site re-resolution of `keyof T` / `T[K]`
  when T is an augmentable interface:** would need new checker
  plumbing. Likely its own PR.

### 4.2 Single `web:dom` package + standalone web siblings (replaces the original FR7 / FR9 design)

**Single `web:dom` package.** The entire DOM tree — Document,
Element, Node, Window, Navigator, every HTML / SVG / MathML
element class, every registry interface
(`HTMLElementTagNameMap`, `SVGElementTagNameMap`,
`MathMLElementTagNameMap`, `HTMLElementEventMap`,
`SVGElementEventMap`, `MathMLElementEventMap`, …), CSSOM,
XML/XPath/parsing, selection, range, history, navigation,
input/pointer/keyboard/touch events, drag-and-drop, observers
(Intersection/Resize/Mutation), Web Animations, custom
elements, fullscreen, picture-in-picture, view transitions —
lives in **one** `web:dom` package. The registries are declared
**closed** alongside the methods that key on them:

```escalier
// In web:dom
interface Document {
    fn createElement<K: keyof HTMLElementTagNameMap>(
        self, tag: K
    ) -> HTMLElementTagNameMap[K]

    fn createElementNS<K: keyof SVGElementTagNameMap>(
        self,
        ns: "http://www.w3.org/2000/svg",
        qualifiedName: K
    ) -> SVGElementTagNameMap[K]

    fn createElementNS<K: keyof MathMLElementTagNameMap>(
        self,
        ns: "http://www.w3.org/1998/Math/MathML",
        qualifiedName: K
    ) -> MathMLElementTagNameMap[K]

    // ... XHTML overload, generic fallback overload
}

interface HTMLElementTagNameMap {
    canvas: HTMLCanvasElement,
    div: HTMLDivElement,
    // ... every HTML tag
}

interface SVGElementTagNameMap {
    circle: SVGCircleElement,
    path: SVGPathElement,
    // ... every SVG tag
}
```

`createElementNS` stays one method on `Document` with its
NS-keyed overload set declared once — matching WebIDL exactly,
no API rename, no cross-package method merge.

**Standalone web siblings.** Families that ship in
`lib.dom.d.ts` but have no DOM coupling (no dependency on
`Document` / `Element` / `Event`) split into their own
pseudo-packages. Initial set, drawn from a survey of
`lib.dom.d.ts` (final list in §6.1's partition table):

| Package | Surface |
|---|---|
| `web:fetch` | Request, Response, Headers, Body, FormData, XHR |
| `web:streams` | Readable/Writable/Transform streams + queuing strategies |
| `web:crypto` | Crypto, SubtleCrypto, algorithm dicts, JsonWebKey |
| `web:workers` | Worker, SharedWorker, MessagePort/Channel, BroadcastChannel |
| `web:webgl` | WebGL 1/2 contexts + extensions |
| `web:web_audio` | Web Audio API + WebCodecs Audio* |
| `web:web_rtc` | RTC*, MediaStream, MediaDevices |
| `web:web_codecs` | Video/AudioEncoder/Decoder, EncodedChunk, VideoFrame |
| `web:indexeddb` | IDB* |
| `web:service_worker` | ServiceWorker, Cache, Push, Notifications |
| `web:websocket` | WebSocket, EventSource |
| `web:storage` | localStorage, sessionStorage, StorageManager |
| `web:url` | URL, URLSearchParams |
| `web:encoding` | TextEncoder, TextDecoder |
| `web:file` | Blob, File, FileReader, FileSystemHandle / FS Access |
| `web:performance` | Performance*, PerformanceObserver |
| `web:webauthn` | Credentials, PublicKey |
| `web:payments` | Payment Request API |

(Small one-offs — geolocation, gamepad, clipboard, permissions,
speech, midi, locks, idle, screen/wake-lock, sensors — live in
`web:dom` for MVP unless a user needs to import a narrow slice
without the full DOM surface. Promote to standalone packages
later if that need is real. See §6.1.)

**Why one big `web:dom` works.** The §4.1 spike showed that
splitting along element-family lines would require two new
checker subsystems. Collapsing the DOM tree into one package
gets the same user-visible narrowing for `createElement(tag) →
ConcreteElement` and `createElementNS(svgNS, tag) →
SVGConcreteElement` using only the existing type system — every
registry is a normal closed `ObjectType`, every `keyof T` / `T[K]`
resolves at declaration time against the populated map. The
DOM is genuinely a cohesive surface (`Element.parentNode` walks
across HTML / SVG / MathML; events bubble across the same tree);
splitting it into per-family packages was a partition that
didn't reflect the API.

**What's lost.** Two cases that the original split-DOM design
contemplated are not expressible in the single-`web:dom` model:

1. **User-side custom-element registration** — code like
   `declare module "web:dom" { interface HTMLElementTagNameMap
   { "my-widget": MyWidget } }`. Punted to a later workstream
   (custom elements). Users can type custom-element results
   manually until then.
2. **Third-party packages adding overloads to `web:dom` methods**
   or extending its event maps. Out of scope for the builtins
   workstream regardless; would be a third-party concern.

### 4.2a No `override declare` for builtins

The `override declare` syntax — designed for the third-party
workstream's override mechanism — is **not** used for any
builtin `.esc` file. All builtin declarations are plain
`declare class` / `declare interface` / `declare fn`. The
converter (§6) emits in plain form; reviewers of §7 verify on
the committed files.

### 4.2b Cross-package type references (primary pattern, supersedes old fallback)

Any API in one pseudo-package that needs to mention a type from
another pseudo-package does so via a **qualified type reference**.
The owning package exports the type; the consuming package
references it through its imported namespace. No augmentation
machinery involved.

Example shapes (drawn from the actual partition):

1. **Standalone web API referencing a `web:dom` type** —
   `web:webgl`'s context constructor takes an
   `HTMLCanvasElement`:
   ```escalier
   // In web:webgl
   import "web:dom"

   declare fn getContext(
       canvas: web.dom.HTMLCanvasElement,
       contextId: "webgl"
   ) -> WebGLRenderingContext
   ```
2. **Standalone web API referencing another standalone API** —
   `web:fetch`'s `Response.body` returns a stream from
   `web:streams`:
   ```escalier
   // In web:fetch
   import "web:streams"

   interface Response {
       body: web.streams.ReadableStream | null,
       // ...
   }
   ```
3. **`web:dom` referencing a standalone API** — `web:dom`'s
   `HTMLCanvasElement.getContext` returns a WebGL context from
   `web:webgl` (creating a mutual import; cycles between
   pseudo-packages are permitted per FR6 / §4.3). In practice
   the converter often inverts the direction here — keeping
   `getContext` typed in `web:dom` with a forward reference and
   the implementation type living in `web:webgl` — to keep
   cycle-count minimal.

Tracking: the partition table (§6.1) records every
cross-package type reference so reviewers can confirm the
direction (which package owns the type) during §7 bootstrap.

The original "string-overload APIs without a registry" fallback
is dropped — the single-`web:dom` rule eliminates this case by
construction (every string-overload API and its registry
co-locate in `web:dom`).

### 4.3 Inter-package imports + cycles (FR6)

- Pseudo-packages `import` other pseudo-packages explicitly
  (e.g. `web/webgl.esc` does `import "web:dom"` to reference
  `web.dom.HTMLCanvasElement` in a parameter type per §4.2b;
  `web/fetch.esc` does `import "web:streams"` to reference
  `web.streams.ReadableStream` in `Response.body`).
- **Cycles between pseudo-packages are permitted.** The
  resolver / `internal/dep_graph/` must special-case `std:`,
  `web:`, `node:` schemes to skip cycle reporting when both
  endpoints of an edge live under these schemes. Cycles are
  expected (e.g. `web:dom` ↔ `web:webgl` for the
  `HTMLCanvasElement.getContext` round-trip).
- Cycles among user packages, and user-package-to-pseudo-package
  cycles, remain disallowed.

### 4.4 Tests and gate

- **Closed-registry fixture:** `web/dom.esc` declares
  `HTMLElementTagNameMap` populated with at least two entries
  (`canvas: HTMLCanvasElement`, `div: HTMLDivElement`) plus
  `createElement(tag)`. Importing file calls
  `createElement("canvas")` and gets `HTMLCanvasElement`;
  `createElement("does-not-exist")` errors with a typed-key
  diagnostic.
- **`createElementNS` NS-keyed overloads fixture:** the same
  `web/dom.esc` also declares `SVGElementTagNameMap` with
  `circle: SVGCircleElement` plus the
  `createElementNS<K: keyof SVGElementTagNameMap>(ns:
  "http://www.w3.org/2000/svg", qualifiedName: K)` overload.
  `document.createElementNS(svgNS, "circle")` narrows to
  `SVGCircleElement`. Verifies that NS-keyed overloads of a
  single method co-located in `web:dom` resolve correctly.
- **Cross-package type reference fixture:** a `web/fetch.esc`
  declares `Response.body: web.streams.ReadableStream | null`
  (qualified reference into `web:streams`). Importing file uses
  the stream; type-checks.
- **Cycle fixtures:** the resolver accepts mutual imports between
  any pair of pseudo-packages — covered by three fixtures:
  `std:*` ↔ `std:*`, `web:*` ↔ `web:*` (the realistic case,
  modeled on `web:dom` ↔ `web:webgl` per §4.2b), and `web:*` ↔
  `std:*` (cross-scheme). The same shape between two user
  packages, and between a user package and a pseudo-package, still
  errors.

(Symbol re-export aliasing — `iterator.iteratorKey` as an alias of
`Symbol.iterator` via the `@js` decorator — is covered by §3.5's
codegen fixtures and the §7 bootstrap review.)

**Gate.** All four fixtures pass; the §4.1 spike note is
committed and the §4.2 single-`web:dom` decision is reflected
in the partition table (§6.1).

### 4.5 Deferred: true cross-package augmentation

The original split-DOM design — feature packages augmenting
`web:dom`'s registry interfaces with per-file activation per
FR9 — is **not** implemented for MVP. The two use cases that
would need it:

1. **User-side custom-element registration.** Users define
   `class MyWidget extends HTMLElement` and want
   `createElement("my-widget") → MyWidget`. Today: users type
   the result manually; no compiler help.
2. **Third-party packages extending DOM/event maps.** Out of
   scope for the builtins workstream regardless; would be a
   third-party concern.

If/when this workstream resumes, the §4.1 spike findings size
the work: per-file composition layer + call-site re-resolution
of `keyof T` / `T[K]` over the merged view. The spike
scaffolding stays committed as a regression harness for any
future implementation.

### 4.6 Method-elem overload resolution on class/interface declarations ✅

**Status.** Landed via PR-A (#652, `MethodElem.Signatures` slice),
PR-B (#653, specificity comparator + free-fn intersection sort),
and PR-C (#656, `MergeMethodOverloads` + class/interface
elaboration call). Subtype-based specificity follow-up: #657.
Inheritance + `implements` overload merging (the original PR-D)
is tracked separately in
[#651](https://github.com/escalier-lang/escalier/issues/651).
The narrative below is preserved as the design record.

The `FuncOverloads` path that resolves same-named free
`declare fn`s by literal-narrowed arg types has no MethodElem
analogue: two same-named methods inside a single class/interface
declaration collapse to the last one, with the earlier elem
silently discarded on insertion. Verified by direct probe — a
`Document` class declared as

```escalier
@js("Document")
export declare class Document {
    createElement(self, tag: "canvas") -> HTMLCanvasElement,
    createElement(self, tag: "div") -> HTMLDivElement,
}
```

dispatches every `doc.createElement(...)` call to the `"div"`
variant, surfacing as `"canvas" cannot be assigned to "div"` /
`HTMLDivElement cannot be assigned to HTMLCanvasElement`.

**Scope clarification — methods only, not free fns.** Free
top-level `declare fn` overloads already work, both in user
code and inside pseudo-package files: the `OverloadDecls`
collection path runs the same way for `web/*.esc` / `std/*.esc`
as for user files, and the resulting `IntersectionType` of
`FuncType`s dispatches correctly via
[infer_expr.go:1059](../../internal/checker/infer_expr.go#L1059).
`TestStdlibImport_NSKeyedOverloads` proves this for a pseudo-
package — it declares two `export declare fn createElementNS<K:
…>` arms in `web/dom.esc` and they dispatch by NS literal
without §4.6. §4.6 is **only** about the method-elem case
(same-named methods inside a single `class` / `interface` /
`declare class` / `declare interface` body). The cleavage line
is free-fn vs. method-elem, not user-code vs. pseudo-package.

Two free-fn-in-pseudo-package edge cases that are *not* covered
by today's tests but are also not §4.6's problem: (1) overload
arms split across multiple files inside one pseudo-package —
doesn't apply, each `std:*` / `web:*` URI resolves to one
`.esc` file; (2) an overloaded free fn re-exported through
another pseudo-package — should propagate naturally via the
§4.2b cross-package qualified-reference path, but no fixture
yet proves the intersection survives the re-export boundary.
Add the fixture if/when a real `web:*` re-export shows up.

**Why this gates §7.** A converted `web:dom` (and a fair amount
of `std:*`) is dense with overloaded methods: `createElement`,
`createElementNS`, `getElementsByTagName`, `addEventListener` /
`removeEventListener` (per-event-name overloads via the event
maps), `HTMLCanvasElement.getContext` (`"2d"` / `"webgl"` /
`"webgl2"` / `"bitmaprenderer"`), `Document.createEvent`,
`querySelector` / `querySelectorAll` (tag-keyed overloads),
`URLSearchParams.append`, `Headers.set`, and the
`String.prototype.replace` / `replaceAll` pairs in `std:string`.
§4.4's `createElementNS` gate fixture had to be rewritten as
free `declare fn` overloads (see
[stdlib_import_test.go:858](../../internal/checker/tests/stdlib_import_test.go#L858))
to express the shape §4.2 actually wants on a `Document`
method; that workaround can't survive §7's converter output.

**Representation: intersection-of-FuncTypes, mirroring free-fn
overloads.** Free `declare fn` overloads are already represented
as an `IntersectionType` of `FuncType` arms
([generalize.go:473-478](../../internal/checker/generalize.go#L473)),
and call-site dispatch at
[infer_expr.go:1059](../../internal/checker/infer_expr.go#L1059)
already iterates the intersection's arms, tries each, and falls
back to `NoMatchingOverloadError`. Method overloads collapse to
the same shape: a single `MethodElem` per name whose `Fn` field
is widened from `*FuncType` to a callable `Type` that may carry
an intersection of per-overload signatures. This matches TS's
own surface semantics — in TS, `interface Foo { bar(x: A): A;
bar(x: B): B }` is *one* property `bar` typed as an intersection
of two call signatures — and means dispatch, printer, hover,
`keyof T` / `T[K]` lookup all see one member with one
(intersected) type. Inheritance / `implements` becomes
"intersect the parent's `Fn` with the new signature"; the
existing `check_implements.go` MethodElem path
([:109](../../internal/checker/check_implements.go#L109),
[:285](../../internal/checker/check_implements.go#L285)) checks
assignability of intersected callables on both sides — the same
machinery used anywhere else.

**Scope of the work.**

1. Detect same-named MethodElems at class/interface elaboration
   time. The current insertion path keys by method name and
   overwrites; replace with an overload-aware insertion that
   merges the new signature into an intersection-typed `Fn` on
   the existing elem.
2. Replace `MethodElem.Fn *FuncType` with `MethodElem.Signatures
   []*FuncType` (length 1 for non-overloaded, length > 1 for
   overloads, ordered most-specific-first). The slice-of-FuncType
   shape makes the "arms are FuncTypes" invariant a compile-time
   guarantee — there's no `Type`-typed field that could hold an
   arbitrary intersection or anything else. Add `SingleSig()` for
   call sites that genuinely cannot handle overloads (panics on
   misuse) and `AsType()` for sites that need a Type view (returns
   the lone arm or `NewIntersectionType(arms...)`). Audit and
   update: `ReceiverIsMut`, the array-mutating-method scan
   ([expand_type.go:1734,1756](../../internal/checker/expand_type.go#L1734)),
   mutability checks, `findCustomMatcherMethod`
   ([checker.go:133](../../internal/checker/checker.go#L133)),
   codegen.
3. Make `getObjectAccess` / method-call resolution route the
   intersection-typed callable through the existing free-fn
   overload path at
   [infer_expr.go:1059](../../internal/checker/infer_expr.go#L1059)
   (no parallel implementation). Must cover both literal-typed
   dispatch (`createElement("canvas") → HTMLCanvasElement`) and
   bounded-generic dispatch (`<K: keyof T>(tag: K) -> T[K]`
   chosen over a `string` fallback).
4. Inheritance / `implements`: a subclass adding overloads
   produces a new intersection that intersects the parent's
   `Fn` with the subclass's new arm(s); `check_implements.go`
   walks both sides as intersected callables. Same machinery for
   `interface` `extends`.

**Design decisions pinned for the implementation (not open
questions — these are the chosen behaviours):**

- **Receiver mutability must be identical across all arms.**
  Reject at class-elaboration time if a class declares both
  `foo(self, …)` and `foo(mut self, …)` for the same name. The
  diagnostic should point at the first mismatching arm and name
  the receiver shape of the earlier arm. Rationale: overload
  resolution is about argument shape, not receiver mutability;
  splitting receiver-mutability across arms would force callers
  to know the dispatch outcome before they know whether the
  call requires a `mut` binding, which defeats the whole point
  of letting `mut` propagate naturally.
- **Arm ordering: most-specific first.** When constructing the
  intersection from the source-declared arms, sort (or require
  source order to already satisfy) most-specific-first so the
  resolver at infer_expr.go:1059 picks the most specific match
  on its first hit. Concretely: literal-typed parameter arms
  before string/number-typed arms before fully generic arms;
  bounded-generic arms (`<K: keyof T>(tag: K)`) before unbounded
  generics or `string` fallbacks. The §4.6 spike must pin the
  exact specificity ordering — start from TS's "more specific
  overload wins" rule and codify the comparator. The intersection
  construction path must preserve this order (verify
  `NewIntersectionType` / `NormalizeIntersectionType` at
  [expand_type.go:2235](../../internal/checker/expand_type.go#L2235)
  don't sort or dedupe arms behind our back; thread a
  preserve-order flag if they do).
- **Generalization is deferred, not in scope for §4.6.** Free-fn
  overloads collect call-site `FuncType`s during generalization
  and merge post-hoc. That path has known gaps for both free-fn
  and (future) inferred-method overloads — tracked in
  [#650](https://github.com/escalier-lang/escalier/issues/650).
  §4.6 only handles **statically-declared** overloads in
  `declare class` / `declare interface` / `class` / `interface`
  bodies, where the arms are visible at elaboration time and the
  intersection can be constructed directly from the AST without
  going through call-site collection.

**Where "last wins" actually happens.** Insertion is *not* the
overwrite point — every site that builds class/interface object
types just `append`s `MethodElem`s
([infer_module.go:591,599](../../internal/checker/infer_module.go#L591),
[infer_class_decl.go:182,185](../../internal/checker/infer_class_decl.go#L182),
[infer_type_ann.go:408](../../internal/checker/infer_type_ann.go#L408),
[infer_stmt.go:693](../../internal/checker/infer_stmt.go#L693)).
The "last wins" surfacing is the reverse-iteration lookup in
[expand_type.go:1195](../../internal/checker/expand_type.go#L1195):
`getObjectAccess` scans `objType.Elems` in reverse, returns at
the first name match, and stops. With two same-named MethodElems
both present, the later one shadows the earlier one at every
read site — but both arms are still in the type, so a merge pass
that runs at the class-elaboration boundary (between elem build
and the `NewObjectType` call that wraps them) is enough to fix
this; we don't need to rewrite the insertion path.

**The merge helper.** Add `MergeMethodOverloads(elems
[]ObjTypeElem, reportErr func(...)) []ObjTypeElem` in
`internal/type_system/` (alongside object-type construction
helpers). Algorithm:

1. Walk `elems` once, building `byName map[ObjTypeKey][]int` of
   MethodElem indices. PropertyElem / GetterElem / SetterElem /
   ConstructorElem / CallableElem / IndexSignatureElem /
   RestSpreadElem / MappedElem pass through unchanged. A
   PropertyElem and a MethodElem sharing a name is a separate
   pre-existing error, not §4.6's concern; leave it alone here.
2. For each name with `len(indices) > 1`:
   - Verify all arms agree on receiver mutability via
     `ReceiverIsMut`. On mismatch, emit
     `OverloadReceiverMutMismatchError{Name, FirstArm, MismatchedArm}`
     and drop the mismatched arm (keep the first arm's shape so
     downstream code still type-checks).
   - Sort arms by the specificity comparator (below); preserve
     source order as the tiebreaker.
   - Collect the arm `*FuncType`s into a slice, build
     `NewIntersectionType(nil, arms...)`. The intersection
     constructor at
     [expand_type.go:2235](../../internal/checker/expand_type.go#L2235)
     currently dedupes / flattens — add a `preserveOrder bool`
     param (or a sibling `NewOrderedIntersectionType`) so the
     specificity-sorted order survives, since dispatch at
     [infer_expr.go:1059](../../internal/checker/infer_expr.go#L1059)
     relies on iteration order being most-specific-first.
   - Replace the first occurrence's `MethodElem` with one whose
     `Fn` is the intersection. Remove the other occurrences.
3. Return the rewritten slice. Idempotent: a slice with no
   duplicates round-trips unchanged.

Call this from every MethodElem-collection site listed above,
immediately before the `NewObjectType` call that consumes the
elems. Also from `unify.go:2610` (which reconstructs a MethodElem
during unification — verify whether the surrounding context
already guarantees uniqueness; if so, a debug `require` instead
of a merge call is enough).

**Specificity comparator.** Implement
`compareOverloadArms(a, b *FuncType) int` in the checker
(alongside [infer_expr.go:1059](../../internal/checker/infer_expr.go#L1059)).
Returns -1 when `a` is more specific than `b`. Rules in
descending priority:

1. **Literal-typed params before non-literal.** Count the
   number of `*LitType` params (after `Prune`) in each arm; the
   arm with more literal params is more specific. This handles
   `createElement(tag: "canvas")` vs.
   `createElement(tag: "div")` (tied) vs.
   `createElement(tag: string)` (less specific).
2. **Bounded generics before unbounded / `string` / `number`
   fallbacks.** For type-param-bearing arms, the arm whose
   bound is a `keyof T` / `T[K]` / union of literals is more
   specific than an unbounded `<T>` or a `string` param. Probe
   each type param's `Constraint`: a non-nil bound that isn't
   `NeverType` ranks ahead of a missing or `NeverType` bound.
3. **Param count.** Fewer required params is more specific
   (matches TS's "more required args before fewer / optional");
   optional params (`Optional: true` or those with default) and
   `...rest` params don't count as required.
4. **Source order tiebreaker.** When the above don't
   discriminate, keep declared order. Stable sort.

Pin these rules with a table-driven test in
`internal/checker/tests/overload_specificity_test.go` before
wiring the comparator into `MergeMethodOverloads` — the test
should cover each rule and a tie that falls through to source
order. The free-fn overload path at infer_expr.go:1059 should
*also* sort its intersection arms with this comparator (today
the order is just whatever the generalize-time intersection
construction yielded); doing both in the same PR avoids
divergent semantics between free-fn and method overloads.

**MethodElem widening: a `Signatures []*FuncType` field.**
Replace the `Fn *FuncType` field with a slice of FuncType arms.
A non-overloaded method has exactly one arm; an overloaded
method has the arms ordered most-specific-first. The invariant
"arms are FuncTypes" is a compile-time guarantee, not a
documented one — there is no way to store a non-FuncType in
this slot.

```go
type MethodElem struct {
    Name       ObjTypeKey
    Signatures []*FuncType
}

// SingleSig returns the sole signature; panics on overload. Used
// at call sites that genuinely cannot handle overloads (e.g.
// Symbol.iterator, custom matchers). Returns nil if Signatures
// is empty.
func (m *MethodElem) SingleSig() *FuncType { /* asserts len==1 */ }

// AsType returns the lone FuncType for single-sig methods,
// or NewIntersectionType(arms...) for overloaded methods.
// Used by member-access resolution (e.g. getObjectAccess) and
// anywhere a Type-valued view of the method shape is needed.
func (m *MethodElem) AsType() Type { /* returns FuncType or IntersectionType */ }
```

Sites that just want to walk the arms call `m.Signatures`
directly (no method call). Sites that need a Type for downstream
plumbing call `m.AsType()`. The deprecated `Fn` field is gone.

Sites to audit and update (all in `internal/checker/`):

| File:line | Today | After |
|---|---|---|
| `getObjectAccess` MethodElem branch in `expand_type.go` | `return elem.Fn, errors` | `return elem.AsType(), errors` — call-site dispatch at infer_expr.go:1058 picks up the IntersectionType case automatically |
| array-mutating-method scan in `expand_type.go` | `ReceiverIsMut(method.Fn)` | `ReceiverIsMut(method.SingleSig())` — mutability must be uniform across arms (enforced at merge time in PR-C) |
| `findCustomMatcherMethod` in `checker.go` | reads `methodElem.Fn` | use `SingleSig()` — custom matcher is single-signature by convention |
| `Symbol.iterator` shape check in `unify.go` | `methodElem.Fn.Params` | use `SingleSig()` — iterator/asyncIterator are single-sig by spec |
| MethodElem reconstruction during ObjectType unification in `unify.go` | builds new MethodElem with widened `Fn` | walk `Signatures` and widen each arm; only allocate a new slice if any arm changed |
| pattern-match custom-matcher lookup in `exhaustiveness.go` | `methodElem.Fn.Params[0]` | `SingleSig()` — custom-matcher is single-sig |
| self-param + iterator-protocol fixup in `method_helpers.go` | mutates `e.Fn.SelfParam` directly | iterate `e.Signatures` and mutate each arm in place (PR-A: one arm; PR-C: all arms share receiver mutability so the per-arm fixup is uniform) |
| method-body inference in `infer_module.go` / `infer_class_decl.go` | reads `methodType.Fn.Params` etc. | hoist `methodSig := methodType.SingleSig()` once per method — body inference runs per AST elem **before** the merge pass collapses arms, so single-sig is the right shape |
| deep-clone MethodElem in `generalize.go` | clones `.Fn` as `*FuncType` | walk `Signatures` and deep-clone each arm |
| `collectUnresolvedTypeVarsImpl` MethodElem branch in `generalize.go` | walks `.Fn` | walk each arm in `Signatures` |
| spread / read-set collection in `unify.go` | adds `elem.Fn` to the effective-values map | use `elem.AsType()` so overloaded methods spread as their IntersectionType |
| interface-merging in `infer_module.go` (`existingProps`, `newType`) | stores `elem.Fn` as the property's type | use `elem.AsType()` |
| printing MethodElem in `print_type.go` | prints one signature | iterate `Signatures` and print each arm; single-arm output identical to before |
| `objElemMatch` and `fillMemberSet` in `internal/interop` | reads `e.Fn` as a Type | `e.AsType()` |
| completion item detail in `cmd/lsp-server/completion.go` | `elem.Fn.String()` | `elem.AsType().String()` |
| `.d.ts` emitter in `codegen/dts.go` | `b.buildFuncTypeAnn(elem.Fn)` | `b.buildFuncTypeAnn(elem.SingleSig())` at PR-A; PR-C fans out one `MethodTypeAnn` per arm |

Construction sites (currently `&MethodElem{Name: k, Fn: fn}`)
must be updated to `&MethodElem{Name: k, Signatures: []*FuncType{fn}}`;
`NewMethodElem(name, fn)` continues to wrap a single arm, so any
caller that goes through the helper is unaffected.

The `codegen/builder.go:2279-2382` `e.Fn` references are
unrelated — those read AST `FuncExpr.Fn`, not type-system
`MethodElem`. JS codegen for class bodies doesn't see
overload arms (TS doesn't either — overload arms are
declaration-only and collapse to one runtime method).

**Inheritance + `implements` — deferred to
[#651](https://github.com/escalier-lang/escalier/issues/651).**
The §4.6 scope below covers same-class method overloads only.
Cross-hierarchy overload merging (subclass adding arms to a
parent's overloaded method, `interface extends`, and `implements`
arm-vs-arm assignability) lands in a follow-up. Sketch retained
here for reference:

For `class B extends A` where
`A` declares `foo(x: A1) -> R1` and `B` adds `foo(x: A2) -> R2`:

- During B's elaboration, after the local merge, walk B's
  parent chain to find any inherited MethodElem with the same
  name. If found, build an intersection
  `[B's arms..., A's arms...]` (subclass arms first so they
  win the specificity sort when equally specific, matching TS).
- Sort the combined intersection with the specificity
  comparator and re-emit B's MethodElem with the merged `Fn`.
- For `interface` `extends`: identical, but operate on the
  declared `Extends` chain instead of class hierarchy.
- For `implements`: `check_implements.go`
  ([:109](../../internal/checker/check_implements.go#L109),
  [:285](../../internal/checker/check_implements.go#L285))
  walks elem-by-elem. Switch the MethodElem branch to call
  `Signatures()` on both sides and require that **every arm
  on the interface side has at least one assignable arm on
  the class side** (TS rule: the implementer must provide a
  signature for each declared overload, but may add more).
  Reuse the existing pairwise `Unify` call for the
  arm-vs-arm check.

A subtle case: `extends` brings in arms whose `SelfParam` is
typed to the *parent* class. After merging into B, those arms
must have `SelfParam` retyped to B (or the comparator and
dispatch will see incompatible receiver types across arms,
which contradicts the receiver-mutability-uniform invariant
even if mutability matches). Add a `retargetSelfParam(arm,
newSelf)` helper and apply it during the inheritance merge.

**Error types.** New diagnostics (add to `internal/checker/errors.go`):

- `OverloadReceiverMutMismatchError{Name, FirstArm, MismatchedArm, span}` — for the receiver-mutability uniformity check.
- `OverloadArmShapeMismatchError{Name, Side, OtherSide}` — for `unify.go:2610` when two `MethodElem`s being unified disagree on arm count or specificity ordering in a way the unifier can't reconcile. (Soft error path: pick the first side and continue, but report.)

`NoMatchingOverloadError` is reused as-is for call-site
dispatch failure — the existing intersection-arm path at
infer_expr.go:1082 already constructs it.

**PR phasing.** Suggest splitting §4.6 into three PRs to keep
review surface manageable. (The original PR-D — inheritance and
`implements` — is deferred to
[#651](https://github.com/escalier-lang/escalier/issues/651).)

1. **PR-A — Replace `MethodElem.Fn *FuncType` with
   `Signatures []*FuncType`, add `SingleSig()` and `AsType()`,
   audit all consumer sites.** Zero behavior change: the merge
   pass is not yet introduced, so every `MethodElem` has exactly
   one arm at runtime. This PR exists purely to make the type-
   system shape ready and to prove the audit table above is
   complete (CI green with no panics is the gate). Includes the
   print_type / codegen printer updates with snapshot churn
   limited to printing one-arm methods identically.
2. **PR-B — Specificity comparator + free-fn intersection
   sort.** Adds `compareOverloadArms`, the
   `overload_specificity_test.go` table tests, and applies
   the comparator to free-fn overload intersections at
   generalize.go:478 / infer_expr.go:1058 iteration. Catches
   any drift in existing free-fn overload behavior before
   methods enter the picture.
3. **PR-C — `MergeMethodOverloads` + class/interface
   elaboration call.** Wires the merge into every MethodElem
   collection site listed above. Receiver-mut mismatch
   diagnostic. Method-call dispatch starts going through the
   IntersectionType path automatically (because `getObjectAccess`
   now returns the intersection). Gate fixtures (below) flip
   to method-shape in this PR.
   - **Printer separator fix.** [print_type.go:454](../../internal/type_system/print_type.go#L454)
     emits overload arms with `", "`, the same separator
     `printObjectType` uses between top-level elements. Once
     methods can have multiple arms, the output
     `{ foo(x: A), foo(x: B), bar: number }` is ambiguous —
     `bar` reads as a third arm. Switch the arm separator to
     `"; "` (or restructure to emit one arm-line per arm) when
     `len(Signatures) > 1`. Add a print snapshot for an
     overloaded method as part of this PR.
PR-A and PR-B are independent and can land in either order.
PR-C depends on both. The deferred inheritance work
([#651](https://github.com/escalier-lang/escalier/issues/651))
depends on PR-C.

**Gate fixtures (live in `internal/checker/tests/`).**

- Rewrite `TestStdlibImport_NSKeyedOverloads` so the two
  `createElementNS<K: …>` overloads are declared as **methods on
  a single `Document` class** (per §4.2 lines 694–700), not as
  free `declare fn`s. The fixture currently uses free fns only
  because method-elem overload resolution doesn't work yet
  ([stdlib_import_test.go:840-887](../../internal/checker/tests/stdlib_import_test.go#L840));
  flipping it to the method shape is the canary that §4.6 is
  actually done. Call sites become `doc.createElementNS(svgNS,
  "circle")` / `doc.createElementNS(mathNS, "mfrac")` against a
  `declare val doc: dom.Document`. The placeholder
  `@js("parseInt")` / `@js("parseFloat")` decorators drop —
  methods on a `@js("Document")` class don't need their own
  per-arm `@js` targets.
- Add a `Document.createElement` fixture mirroring
  `TestStdlibImport_ClosedRegistryNarrowing` /
  `TestStdlibImport_ClosedRegistryUnknownTag` but with two
  literal-keyed `createElement` methods (no generic) — pins the
  simplest literal-narrowed method overload case end-to-end.
- Add an `addEventListener` fixture: a small event-map type plus
  per-event-name overloads of `addEventListener` on a single
  class, verifying the handler param narrows to the
  event-specific type for each literal name.
- Add a receiver-mutability mismatch fixture: a class declaring
  the same method with both `self` and `mut self` receivers
  should produce an elaboration-time error naming both arms.

Once these pass, the §4.4 fixtures move back to the method
shape and the §4 row's "Open" note clears.

---

## §5. Converter MVP (FR10)

**Goal.** A minimal `tools/dts_to_esc/` Go binary that translates
two tiny TS-lib slices to readable, parseable `.esc`:

1. **A trio-idiom class.** `Boolean` from `lib.es5.d.ts` (~10
   lines: `interface Boolean { … }` + `interface BooleanConstructor
   { … }` + `declare var Boolean: BooleanConstructor`). Exercises
   work item 3 (class-via-trio recognition) and confirms the
   emitted form is `@js("Boolean") export declare class Boolean
   { … }`.
2. **A `declare namespace` block.** A small namespace from
   `lib.es5.d.ts` (e.g. `JSON` declared as
   `declare namespace JSON { fn parse(...); fn stringify(...); }`,
   or `Reflect` — pick whichever is smaller in the pinned TS
   version). Exercises work item 4 (namespace flattening). Each
   member becomes a top-level `export declare fn` in the output
   file, carrying `@js("<Namespace>.<fn>")` per work item 8 —
   e.g. `@js("JSON.parse") export declare fn parse(…) -> …`.

Covering both shapes in the MVP means the two highest-risk
translations (trio recognition and namespace flattening) each
have a concrete fixture by the time §6 productionizes the
converter against the full lib set.

**Location.** New directory `tools/dts_to_esc/` alongside
existing `tools/gen_ast/` and `tools/gen_types/`.

### 5.0 Precursor: `dts_parser` JSDoc retention ✅

Landed. Leading JSDoc (`/** ... */`) is now attached to top-level
declarations (`VarDecl`, `FuncDecl`, `ClassDecl`, `InterfaceDecl`,
`TypeDecl`, `EnumDecl`, `NamespaceDecl`, `ModuleDecl`, `GlobalDecl`)
and to interface / class / object-type members (`MethodDecl`,
`PropertyDecl`, getters/setters, `ConstructorDecl`, `IndexSignature`,
and their `*Signature` interface analogues) via a `Doc string` field
and a `Documented` interface (`SetDoc(string)`). Pre-existing tests
(misnamed `TestCommentsInObjectTypes` / `TestRealWorldSymbolInterface`)
verified comments did not crash the parser; they did not assert
retention, and the snapshots showed comments were discarded everywhere.
A new `TestTopLevelJSDocRetention` in
[internal/dts_parser/comment_test.go](../../internal/dts_parser/comment_test.go)
locks in the retention rules: only `/** ... */` blocks immediately
preceding a declaration attach; line comments and plain `/* */`
blocks do not; intervening non-doc comments reset the captured doc;
the most recent contiguous JSDoc wins; `/**/` is not JSDoc.

**Work items.**

1. Read a single `.d.ts` file via the existing
   [internal/dts_parser/](../../internal/dts_parser/).
2. AST-to-AST translation: map TS declaration AST nodes to
   Escalier declaration AST nodes directly, bypassing
   `type_system.Type` and the checker. **No type resolution
   involved** — no prelude bootstrap cycle.
3. Recognize the **class-via-trio idiom** at AST level:
   `interface Foo` + `interface FooConstructor` +
   `declare var Foo: FooConstructor` collapses to one
   `declare class Foo` (instance members from `Foo`, statics +
   constructor from `FooConstructor`, `declare var` dropped).
   The recognition rules mirror
   [internal/interop/class_shapes.go](../../internal/interop/class_shapes.go)
   `tryFuseTrio` — same predicates, different substrate:
   - The `FooConstructor.new(...)` signature must return the
     instance type `Foo`.
   - Both `Foo` and `FooConstructor` interface bodies must be
     object-shaped (no other variants).
   - The `declare var Foo: FooConstructor` binding must match
     the `FooConstructor` interface name exactly.
   The Escalier-style sibling shape recognized by
   `tryFuseEscalierClass` is not expected in `lib.*.d.ts` and
   does not need handling. Trios that do not satisfy the
   predicates pass through unchanged.
4. **Flatten `declare namespace` blocks.** Per FR10 step 2,
   TS `declare namespace Foo { … }` becomes top-level
   declarations in the output file (each pseudo-package file
   is itself a namespace). The converter does not emit nested
   namespace syntax; the FR14 audit (§1) excludes
   `declare namespace` from the supported declaration forms.
5. **Receiver mutability seeding.** Run `interop.Classify`
   (tiers 3/5/6 from the interop_mutability workstream) at
   conversion time to seed `self` vs `mut self` on each method.
6. **JSDoc pass-through.** Leading JSDoc on a TS declaration
   carries through to the emitted Escalier declaration as a
   doc comment. Strip TS-specific tags (`@override` dropped;
   `@param` syntax adjusted where Escalier differs); pass the
   rest verbatim. Precursor: §5.0 above. The JSDoc tag
   stripping/rewriting table is a small in-tree config inside
   `tools/dts_to_esc/`, easy to extend as cases surface.
7. **Intrinsic stripping.** `intrinsic`-typed declarations are
   skipped (FR13). The parser does not learn the `intrinsic`
   keyword.
8. **`export` and `@js` decorator emission** (§3). Every emitted
   top-level declaration is `export`-prefixed (pseudo-package
   files follow the regular Escalier module visibility rule) and
   carries an `@js(...)` decorator naming the JS expression it
   lowers to. The canonical shape is
   `<decorators> export declare <kind> ...`. The MVP slices
   exercise both rule branches:
   - Trio-idiom class → `@js("<ClassName>")` (`Boolean` →
     `@js("Boolean") export declare class Boolean { … }`).
   - Namespace member → `@js("<Namespace>.<fn>")` after the
     namespace flattening of step 4 (`JSON.parse` →
     `@js("JSON.parse") export declare fn parse(…) -> …`).
   The general `@js` rule also covers declarations hoisted from
   the global scope into a partition package (e.g. `parseInt` →
   `std:number`), which get `@js("<bare name>")` — exercised in
   §6 against the full lib set, not in the MVP. The converter
   does not emit unexported declarations — every TS-side
   top-level declaration that the partition table maps to a
   package is exposed. Symbol re-exports and other hand-authored
   declarations are §7 territory.
9. Emit via the (now-audited) declaration printer and
   [internal/type_system/print_type.go](../../internal/type_system/print_type.go).
   Emit to stdout. No file layout, no partition table yet.

**Gate.** Output for both MVP slices (the trio-idiom class and
the small namespace):

- Parses through `parser.ParseFile`.
- Reads naturally to a human (snapshot-tested via `go-snaps`).
- Two consecutive conversions produce byte-identical output.
- The namespace slice emits zero `declare namespace` blocks in
  the output — every former-namespace member is a top-level
  declaration with `@js("<Namespace>.<fn>")`.
- The trio slice emits exactly one `declare class` and zero
  `declare var` (the constructor's `declare var` is consumed by
  the trio recognizer).

---

## §6. Converter productionization (FR10)

**Goal.** Convert the full pinned `lib.*.d.ts` set into the
committed package partition.

### 6.1 Partition table

A hand-maintained Go map in the converter source: TS-lib
declaration name → target pseudo-package. Drives both file
output and the LSP name-index (§10.3). Driven by the
[FR1 partition list](requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings).

**`std/` (full enumeration).**

| Package           | Type                | Members / notes                                                                                                                  |
| ----------------- | ------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `std:array`       | per-class           | `Array<T>`                                                                                                                       |
| `std:string`      | per-class           | `String`                                                                                                                         |
| `std:number`      | per-class           | `Number`; also `parseInt`, `parseFloat`, `isNaN`, `isFinite` (numeric-parsing domain)                                            |
| `std:boolean`     | per-class           | `Boolean`                                                                                                                        |
| `std:bigint`      | per-class           | `BigInt`                                                                                                                         |
| `std:regexp`      | per-class           | `RegExp`; re-exports the regex-related well-known symbols (`Symbol.match`, `replace`, `search`, `split`, `matchAll`) as `regexp.matchKey`, `regexp.replaceKey`, etc.        |
| `std:symbol`      | per-class           | `Symbol`, including **all** well-known symbols (`Symbol.iterator`, `Symbol.asyncIterator`, `Symbol.match`, `Symbol.toPrimitive`, …) declared directly on `Symbol`'s static side |
| `std:object`      | per-class + utility | `Object`; `Partial`, `Required`, `Readonly`, `Pick`, `Omit`, `Record`, `Exclude`, `Extract`, `NonNullable`                       |
| `std:function`    | per-class + utility | `Function`; `Parameters`, `ConstructorParameters`, `ReturnType`, `InstanceType`, `ThisParameterType`, `OmitThisParameter`, `ThisType` |
| `std:date`        | per-class           | `Date`                                                                                                                           |
| `std:map`         | per-class           | `Map`                                                                                                                            |
| `std:set`         | per-class           | `Set`                                                                                                                            |
| `std:weak_ref`    | per-class           | `WeakRef`                                                                                                                        |
| `std:iterator`    | bundled             | `Iterator<T>`, `Iterable<T>`, `IterableIterator<T>`, `IteratorResult<T>`, `Generator<T,R,N>`; re-exports `Symbol.iterator` as `iteratorKey`         |
| `std:async`       | bundled             | `Promise<T>`, source-level `Awaited<T>`, `AsyncIterator<T>`, `AsyncIterable<T>`, `AsyncGenerator<T,R,N>`, `AggregateError`; re-exports `Symbol.asyncIterator` as `asyncIteratorKey`; depends on `std:iterator`. `Promise` lives here (not in a dedicated `std:promise`); under `?local` access is `async.Promise.all(…)`. |
| `std:error`       | bundled             | `Error`, `TypeError`, `RangeError`, `SyntaxError`, `ReferenceError`. `URIError` → `std:url`; `AggregateError` → `std:async`. `EvalError` dropped |
| `std:url`         | bundled             | `URIError`, `encodeURI`, `decodeURI`, `encodeURIComponent`, `decodeURIComponent`                                                 |
| `std:math`        | namespace           | unchanged from existing layout                                                                                                   |
| `std:json`        | namespace           | unchanged                                                                                                                        |
| `std:console`     | namespace           | unchanged                                                                                                                        |
| `std:typed_arrays`| bundled             | unchanged                                                                                                                        |
| `std:reflect`     | namespace           | unchanged                                                                                                                        |
| `std:proxy`       | per-class           | unchanged                                                                                                                        |
| `std:intl`        | bundled             | unchanged; needs `import "std:date"`                                                                                             |
| `std:temporal`    | bundled             | unchanged                                                                                                                        |
| `std:wasm`        | bundled             | unchanged                                                                                                                        |

**`web/` partition.** Per §4.2, the DOM partition is **one big
package + standalone web siblings**:

- **`web:dom`** — the entire DOM tree. Document, Element, Node,
  Window, Navigator; every HTML / SVG / MathML element class;
  every tag-name-map and event-map registry (closed); CSSOM;
  XML/XPath/parsing; selection; range; history; navigation;
  input/pointer/keyboard/touch events; drag-and-drop;
  observers (Intersection/Resize/Mutation); Web Animations;
  custom elements; fullscreen; picture-in-picture; view
  transitions. One large `.esc` file; one `import "web:dom"`
  gets the whole DOM surface.

- **Standalone web siblings** — families that ship in
  `lib.dom.d.ts` but have no DOM coupling. Initial set:
  `web:fetch`, `web:streams`, `web:crypto`, `web:workers`,
  `web:webgl`, `web:web_audio`, `web:web_rtc`,
  `web:web_codecs`, `web:indexeddb`, `web:service_worker`,
  `web:websocket`, `web:storage`, `web:url`, `web:encoding`,
  `web:file`, `web:performance`, `web:webauthn`, `web:payments`.

Small one-offs (geolocation, gamepad, clipboard, permissions,
speech, midi, locks, idle, screen/wake-lock, sensors) land in
`web:dom` for MVP; promote to standalone packages later if a
real user needs to import them without the full DOM surface.
Additional standalone `web:*` packages may be added in §7
review as the partition is exercised against the full lib
input.

A typical browser program imports `web:dom` plus 1–2 sibling
packages (`web:fetch` for HTTP, `web:storage` for
localStorage, etc.).

**Single-class shortcut eligibility (FR5).** Per FR5, the
single-class shortcut applies to:

`std:array → Array`, `std:string → String`,
`std:number → Number`, `std:boolean → Boolean`,
`std:bigint → BigInt`, `std:regexp → RegExp`,
`std:symbol → Symbol`, `std:object → Object`,
`std:function → Function`, `std:date → Date`, `std:map → Map`,
`std:set → Set`, `std:weak_ref → WeakRef`. The converter does
not mark this explicitly — the shortcut activates structurally
when the package's lowercased URI segment matches a top-level
class name case-insensitively. `std:async` does **not**
qualify (multiple top-level classes including `Promise`,
`AsyncIterator`, …; no class named `Async`), so `Promise.all`
is accessed as `async.Promise.all` under `?local`.

**Drops.** `globalThis` and `eval` drop entirely — `eval` has
no good use case; `globalThis` was the union of every
previously-ambient name and has nothing to take its union over
in the new model. The converter recognizes both and skips
emission with a logged note. `intrinsic`-typed declarations
(FR13) skip the same way.

**Unmapped-symbol fail-safe.** Per FR10 step 4: any top-level
TS-lib declaration name absent from both this partition table
and the explicit drop list above causes the converter to abort
with a diagnostic naming the symbol, its source `.d.ts` file,
and this partition-table file. No catch-all "unmapped" package
and no silent drop. The check lives at the partition-table
lookup site so misses surface where the routing decision is
actually made.

**`node:*`.** Partition deferred until Node support lands. The
`internal/interop/data/node/` directory is created empty.

### 6.2 Registry routing

- Per §4.2 (single `web:dom` package), every registry interface
  and every element class lands in `web:dom`:
  `HTMLElementTagNameMap`, `SVGElementTagNameMap`,
  `MathMLElementTagNameMap`, `HTMLElementEventMap`,
  `SVGElementEventMap`, `MathMLElementEventMap`, and every
  element class they index (`HTMLCanvasElement`,
  `SVGCircleElement`, `MathMLElement`, …) all route to `web:dom`
  alongside the methods that key on them (`createElement`,
  `createElementNS`, `addEventListener`). No cross-package
  augmentation block is emitted; no per-NS package split.
- Standalone web families that ship in `lib.dom.d.ts` without
  DOM coupling route to their sibling `web:*` packages per the
  §6.1 partition table — Fetch / Streams / Crypto / Workers /
  WebGL / Web Audio / WebRTC / WebCodecs / IndexedDB / Service
  Worker / WebSocket / Storage / URL / Encoding / File /
  Performance / WebAuthn / Payments. Standalone-package
  declarations that reference DOM types (e.g. WebGL's
  `getContext` taking an `HTMLCanvasElement`) emit qualified
  type references per §4.2b.
- Well-known symbol declarations on `SymbolConstructor` stay
  in `std/symbol.esc` — they are **not** rerouted (FR8). The
  domain-package re-export aliases (`iterator.iteratorKey`, `async.asyncIteratorKey`,
  `regexp.matchKey`, …) are hand-authored at §7 bootstrap, not
  emitted by the converter.

### 6.3 Output layout

Per [FR2](requirements.md#fr2-pseudo-package-layout):

```
internal/interop/data/std/  — std/*.esc
internal/interop/data/web/  — web/*.esc
internal/interop/data/node/ — reserved, empty
```

**Distribution.** Files are shipped alongside the compiler
binary (typically under `share/escalier/data/` on Unix-style
installs) and discovered at runtime per §2.2a — **not** embedded
via `//go:embed`. Editable post-install so users can tweak
builtins or add new ones without rebuilding the compiler.
Release packaging (`make`, install scripts, distro packages)
copies the tree alongside the binary; CI verifies the
post-install layout discovers correctly.

### 6.4 Re-run semantics and `--check` mode

Re-running the converter against committed files is **additive
and signature-checked**, not a wholesale overwrite. The hand-edits
under [§7](#7-stdlib-bootstrap) (`throws`, lifetimes, mutability
refinements) must survive a re-run.

**Default re-run (write mode):**

- **Add** declarations present in the `.d.ts` but missing from the
  committed `.esc` (new TS-lib symbols since last bump).
- **Add** members on existing classes / interfaces that the `.d.ts`
  has but the `.esc` is missing.
- **Never overwrite** an existing declaration's body, signature, or
  hand-added annotations. Hand-edits are sticky.
- **Report** declarations present in the `.esc` but absent from the
  `.d.ts` (likely TS-side removal) — informational, no automatic
  deletion.

**`--check` mode (CI):** read-only verification that fails CI on
any of:

1. **Missing declarations.** A `.d.ts` declaration with no
   corresponding `.esc` declaration in the partition's target file.
2. **Incompatible signature drift.** An `.esc` function / method
   signature whose param or return types are not assignable to /
   from the `.d.ts` original (per the checker's assignability
   rules, applied to the converted-from-TS form). Catches
   accidental hand-edits that change the meaning of a signature
   rather than refining it.
3. **Incompatible property-type drift.** Same check for properties
   on classes / interfaces.

Adding `throws`, lifetimes, mutability, or narrowing a parameter /
return type within the `.d.ts` shape is **not** incompatible and
does not trip `--check`. The compatibility check is one-directional
in the obvious sense (Escalier-side may be stricter than TS-side,
not looser).

This is the TS-version-bump review tool — **never auto-applies**
deletions or signature changes.

### 6.5 `throws` annotations (bootstrap policy)

Per [FR10](requirements.md#fr10-bootstrap-converter-tools-dts_to_esc):
hand-curate the high-value ~50 entries (`JSON.parse`,
`decodeURI*`, `BigInt`, `fetch`, `Response.json`, etc.). Scraping
MDN is rejected (prose-not-data, brittle, copyleft).
WebIDL `[Throws]` extraction (`@webref/idl`) is a plausible
future automation lever for `web:*` but is **out of scope for
the bootstrap**. ECMARKUP extraction for `std:*` is similarly
deferred.

### 6.6 TS-version-bump workflow

**CLI shape.** `tools/dts_to_esc/` is a single Go binary with
subcommands:

- `dts_to_esc check` — read-only verification (§6.4); CI uses this.
- `dts_to_esc regenerate` — additive write mode (§6.4); adds new
  declarations / members from upstream TS without overwriting
  existing bodies, signatures, or hand-edits.
- `dts_to_esc bootstrap` — one-time initial seeding from a fresh
  TS-lib input set (no committed `.esc` tree assumed); used by §7.

Document the bump workflow in `tools/dts_to_esc/README.md`:

1. Bump the pinned TS dependency.
2. Run `dts_to_esc check`. Output is a unified diff against
   current committed files showing TS-side adds / removes /
   changes plus any compatibility errors.
3. Optionally run `dts_to_esc regenerate` to apply additive
   changes; review the diff and commit.
4. Contributor ports any signature / removal changes by hand and
   commits the result.

**Optional CI nudge.** An action that annotates a PR with "TS
lib changed since last bump" when the diff exceeds some
threshold. Out of scope for the initial workstream, but the
hook point is identified in this doc so it can be added later
without re-architecture.

**Gate.** Every output `.esc` file under
`internal/interop/data/{std,web}/` parses; the converter is
idempotent (byte-identical on re-run); the partition matches
[FR1](requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings)
member-for-member.

---

## §7. Stdlib bootstrap

**Goal.** Commit the initial generated-then-hand-edited `.esc`
files as the source of truth.

**Work items.**

1. Run the converter (§6) once, producing the full tree under
   `internal/interop/data/{std,web}/`.
2. Human review of every file. Hand-edit:
   - Obvious mis-classifications.
   - High-value `throws` annotations (the ~50 from §6.5).
   - Lifetimes where applicable (the existing
     [planning/lifetimes/](../lifetimes/) work feeds in here).
   - Mutability refinements not captured by the
     `interop.Classify` seeding.
   - `Symbol.customMatcher` (Escalier-specific, not in
     `lib.*.d.ts`) hand-authored in `std:symbol`, written as
     `@js("Symbol.customMatcher") export declare …` per §3.
   - **Symbol re-exports** per FR8 (`iterator.iteratorKey`, `async.asyncIteratorKey`,
     `regexp.matchKey`, …) hand-authored in their owning
     packages, written as
     `@js("Symbol.<name>") export declare val <name>Key: unique symbol`.
     The converter does not emit these because they are not part
     of any `lib.*.d.ts`.
   - **`export` + `@js` decorator review.** Verify every
     exported top-level declaration has an `@js` decorator with
     a real JS-runtime expression as argument. Spot-check that
     declarations meant to be package-private (helper types,
     internal aliases) are correctly unexported and carry no
     `@js`. Missing-decorator, missing-`export`, or
     typo'd-target bugs ship to users otherwise; the §3 loader
     check catches them at compile time but humans should catch
     obvious cases here.
3. **`Awaited<T>` source-level definition.** Write `Awaited<T>`
   in `std:async` as the recursive conditional type (the same
   shape as TypeScript's definition). Exercise against a
   representative fixture (nested promises, thenables, mixed
   `T | Promise<T>`, generic propagation). If a concrete
   blocker surfaces, fall back to a checker-resident intrinsic
   and document the specific failure.
4. **FR5 finalization — non-class package exports as namespace
   members.** §2's single-class shortcut binds the class itself
   when activated; the FR5 spec also calls for other package
   exports to remain accessible *as namespace members on the
   same binding*, with static methods winning on name collision.
   The §2 implementation left this as a TODO in
   [bindStdlibLocal](../../internal/checker/infer_stdlib_import.go)
   because the §2-era stubs (`std:math`, `std:array`) have only
   a single export. Once the §7 bootstrap produces real packages
   that pair a class with non-class exports (e.g. `std:array`
   gaining helper functions or constants alongside `Array`),
   wire the merge: copy `pkgNs.Values` and `pkgNs.Types` onto
   the class binding's static surface, and add a unit test pinning
   the static-method-wins tiebreaker.
4. Commit. After this point, the converter persists only as
   `--check` review tool; ongoing edits are direct to the
   committed `.esc` files.
5. **§3.5 codegen fixtures deferred from §3.** Once `std:number`
   and `std:iterator` exist as committed packages, add the
   fixtures §3 couldn't author without them:
   - Hoisted global: `parseInt(s)` → `parseInt(s)`.
   - Symbol re-export: `iterator.iteratorKey` → `Symbol.iterator`.
   - Package-private declaration (unexported, no `@js`) is
     invisible to importers — referencing it from outside the
     pseudo-package errors as unbound.

**Gate.** Humans review the committed files; `go test ./...`
passes — the existing checker still resolves these declarations
through the legacy `lib.*.d.ts`-walking prelude. §8 then
migrates fixtures and tests to `import "std:*"` while both
resolution paths are live, and §9 deletes the legacy path in a
single PR. The §7 commit on its own changes no checker
behavior; it just lands the source-of-truth `.esc` files.

---

## §8. Internal fixture migration

**Goal.** Migrate Escalier's own fixtures and tests to
`import "std:*"` so that §9 can delete the legacy lib-walking
prelude without breaking the suite.

**Prerequisites.** §7 (the `.esc` files must be committed for
imports to resolve) and §4 (the single-`web:dom` package +
cross-package type references must be in place if any fixture
touches DOM). §2's resolver is in place transitively via §7.

Update every fixture and test under
[fixtures/](../../fixtures/) and
[internal/checker/tests/](../../internal/checker/tests/) that
relied on previously-ambient symbols (`Math`, `JSON`, `console`,
`Promise`, `Error`, `Array.from`, `parseInt`, …) to use
`import "std:*"` statements. The legacy prelude is still live
during this phase, so the migrated and not-yet-migrated files
type-check side-by-side until §9 removes the legacy path.

The auto-import quick-fix from §10.3 is the migration aid when
it is available; otherwise migration is by hand. Ordering between
this phase and §10.3 is not strict — fixture migration can proceed
without the quick-fix, but having the quick-fix first lets the
fixture rewrite exercise the same tooling external users will
rely on.

**Gate.** `go test ./...` passes with every fixture using
explicit `import "std:*"` statements; no fixture relies on
ambient resolution — except for the third-party carve-out below.

**Carries-over from §2.** §2 landed three binding-shape fixtures
under [fixtures/](../../fixtures/) (`stdlib_import_local`,
`stdlib_import_nested`, `stdlib_import_single_class`). One §2.5
fixture was deferred to this phase because it needs material
that does not exist until §7:

- A **single-class shortcut fixture with non-class package exports**
  on the same binding. §2's `std:array` stub has only the class;
  once §7 populates `std:array` with companion helpers (or another
  package mixes a class with constants/functions), add a fixture
  that exercises both the class and a non-class export through the
  same shortcut binding, including the static-method-wins
  tiebreaker (the work itself lives in §7 — this fixture is the
  end-to-end gate).

### 8.1 Third-party `.d.ts` fixture carve-out

A small set of fixtures and tests exercises **third-party
`.d.ts`** content (vendored or stub `node_modules` packages).
These cannot be migrated to explicit `import "std:*"` in §8: the
migration mechanism is the third-party converter's import-header
injection (third-party FR7), which is gated behind the entire
builtins workstream and therefore not yet available.

See [../third_party/requirements.md § Tests broken by the builtins → third-party gap](../third_party/requirements.md#tests-broken-by-the-builtins--third-party-gap)
for the authoritative statement. The mechanics required here:

- **Skip helper.** Add a single `testutil.SkipUntilFR7(t, reason
  string)` helper (concrete package path TBD; likely
  [internal/testutil/](../../internal/testutil/) or a new
  `internal/testskip/`). It is the **only** sanctioned skip call
  site for this gap — every affected test calls it, no scattered
  `t.Skip(...)` strings. Re-enabling is one grep + one deletion.
- **Audit pass.** Before §9 lands, walk every fixture and test
  that loads a third-party `.d.ts` (the existing runtime interop
  pipeline's consumers — `interop.ConvertModule` call sites and
  fixtures with `.d.ts` payloads). Anything that would fail with
  the legacy prelude removed gets `SkipUntilFR7`. Do not try to
  rewrite these to `import "std:*"`; the right migration path
  for them lives in third-party Phase 1.
- **CI guard.** Add a one-line CI check (a `grep -r
  SkipUntilFR7` in the pre-merge job) that fails the build if
  the helper is still referenced after third-party FR7's
  tracking issue is closed. The closed-issue check can be
  hard-coded against the issue number at the time the guard
  lands. Prevents the skip from rotting silently.
- **Helper lifecycle.** The helper and the CI guard are deleted
  together as part of third-party FR7's definition-of-done. The
  helper exists purely to bridge the builtins → third-party gap;
  it has no permanent home in the test infrastructure.

The audit is not zero-cost — somebody has to enumerate the
affected tests — but it is strictly cheaper than building a
compatibility shim in the prelude that keeps the legacy lib-walking
alive solely for the runtime interop pipeline. The skip approach
also keeps §9's cut-over PR clean: any remaining ambient-resolution
failures in CI are genuine §8 misses, not third-party fallout.

---

## §9. Prelude switchover + override deletion (FR11)

**Goal.** Replace the `lib.*.d.ts`-walking prelude with the lazy
per-file shape loader, and delete the legacy override / builtin
machinery in the same change. Pre-1.0; no deprecation cycle, no
build flag, no parallel paths.

**Hard prerequisite: §8 lands first.** Deleting the legacy
prelude removes the resolution path that previously made `Math`,
`JSON`, `console`, `Promise`, `Error`, `Array.from`, `parseInt`,
… visible without imports. Every fixture and test that names
those symbols must already be rewritten to use
`import "std:*"` — that is §8's job. §8 is feasible after §3 and
§4 land (resolver + closed registries + cross-package type
references in place) and runs while the
legacy prelude is still live, so migrated and not-yet-migrated
files type-check side-by-side throughout the §8 work. §9 is the
cut-over: the PR that opens §9 must have a green test suite on
`HEAD` *with* the legacy prelude still active, and must keep it
green *without* it once the legacy paths come out. If a fixture
slipped past §8, the §9 PR will fail CI on the unbound-name
diagnostics for the previously-ambient symbols.

Note: fixtures that load **third-party `.d.ts`** content are
intentionally not migrated in §8 — they are marked
`SkipUntilFR7` per §8.1 and stay skipped through §9. The §9 PR
should not attempt to un-skip or migrate them; that work belongs
to third-party FR7. See §8.1 for the helper and CI guard.

### 9.1 Lazy shape-loader

Touch point:
[internal/checker/prelude.go](../../internal/checker/prelude.go).

For each file F being checked, the checker inspects F's syntax
and shape-loads only the needed `std:*` packages (no name
bindings added to scope). Trigger map per FR11:

| Trigger                                                            | Shape-loaded package(s)              |
| ------------------------------------------------------------------ | ------------------------------------ |
| String/number/boolean/bigint literal or operator on a primitive    | `std:string`/`number`/`boolean`/`bigint` |
| Array literal                                                      | `std:array`                          |
| Regex literal                                                      | `std:regexp`                         |
| `async fn` / `await` / `for await x of xs`                         | `std:async` (covers Promise, Awaited, and async iteration) |
| `for x of xs` / generator                                          | `std:iterator`                       |
| `try` / `catch` / `throw` / `throws` clause **naming** a `std:error` class (`Error`, `TypeError`, …) | `std:error` — bare `throw "x"` or a `throws` listing only user-defined errors does not trigger |

Multiple files share one parsed copy of each package.
Shape-loading is idempotent and additive.

### 9.2 Explicit import loading

Reuses §2's resolver path. The shape-load and named-import paths
share the same parsed declarations; they differ only in whether
identifiers land in F's scope. Multiple files in a compilation
share **one parsed copy** of each `std:*` package; the
shape-loader memoizes by package URI. No bootstrap cycle: each
`std:*` package contains only declarations (no value-level
expressions needing their own prelude).

### 9.2a Cross-package references between pseudo-packages

**All cross-package references inside `std:*` / `web:*`
packages require explicit `import` statements** — the same
rule as user code. `std/async.esc` writes
`import "std:iterator"` if it references `Iterable<T>`;
`std/array.esc` writes `import "std:iterator"` for the
iteration protocol; `web/canvas.esc` writes
`import "web:dom"` to extend `HTMLElement`. There is no
implicit "all sibling packages visible" rule and no shape-load
side effect that pulls in another package's names. The
shape-loader's only job at the F-level is to decide which
packages F's *user-level* syntax depends on; inside each
loaded package, name resolution is ordinary.

**Cycles between `std:*` / `web:*` packages are permitted**
because everything in these schemes is, at runtime, a
pre-existing builtin — the cycle is purely type-level and
runtime-erased, so there is no initialization-order concern.
The resolver / `internal/dep_graph/` special-cases the `std:`,
`web:`, and `node:` schemes to skip cycle reporting when both
endpoints of an edge live under these schemes (already
specified in §4.3). Cycles among user packages, and any cycle
that touches a user package, remain disallowed.

Test: a fixture using only an `async fn` + a `for of` loop
triggers shape-loading of `std:async`; resolving `Promise<T>`'s
internal reference to `Iterable<T>` succeeds because
`std/async.esc` itself imports `std:iterator` — not because
shape-loading magically pulled `std:iterator` into F's scope.
A mutual-import fixture between two `std:*` packages confirms
the cycle-allowance rule.

### 9.3 Delete the legacy paths (same PR)

Delete in the same change that swaps the prelude — no flag, no
parallel paths, no waiting. The compiler is pre-1.0; the
breaking change lands cleanly. Internal fixtures must already
have been migrated to `import "std:*"` (§8) before this PR
opens, otherwise the test suite breaks.

Touch points to delete or empty out:

- `loadGlobalDefinitions` ([prelude.go](../../internal/checker/prelude.go))
- `populateSelfParams`
- `UpdateMethodMutability`
- `mergeReadonlyVariant`
- the `mutabilityOverrides` Go map
- `BuildBuiltinStore` (empty the function body; delete the call
  site; delete the function in a follow-up if no callers
  remain)
- `internal/interop/data/builtins/` (if present) — no override
  fragments for builtins remain
- The Escalier-specific
  `SymbolConstructor.customMatcher` injection at
  [prelude.go:804–836](../../internal/checker/prelude.go#L804-L836)
  (now sourced from `std:symbol` per §7 step 2)

**No `override declare` for builtins.** That syntax stays
reserved for the third-party workstream's override mechanism;
no builtin pseudo-package uses it.

**Gate.** `go test ./...` passes on the new path alone; the
legacy code is gone, not behind a flag.

---

## §10. Intrinsics; adaptive rendering; LSP support (FR13, FR15, FR16)

**Goal.** Ship the diagnostic + LSP tooling users need under the
new model, and confirm intrinsic handlers stay checker-resident.

### 10.1 Intrinsic types (FR13)

Confirm that `Uppercase`, `Lowercase`, `Capitalize`,
`Uncapitalize`, `NoInfer` remain checker-resident handlers — no
source file under `internal/interop/data/`. The four string-case
utilities are pure `Type → Type` resolvers; `NoInfer` is an
inference-machinery hook. Tracked in escalier-lang/escalier#631.

`Awaited<T>` source-level definition lives in `std:async` per
§7 step 3 (the recursive conditional type matching TS's
definition; tracked in escalier-lang/escalier#630). Fallback to
a checker-resident intrinsic only on documented blocker —
recursive conditionals don't reduce correctly, pathological
performance, or a soundness issue. **The fallback decision
must be committed with a documented description of the specific
failure that motivated it.** Concretely: a Go doc comment on the
checker-resident `Awaited` handler citing the failing fixture
under `internal/checker/tests/` (or `fixtures/`) that motivated
the fallback. Not duplicated in this plan.

The bootstrap converter strips `intrinsic`-typed declarations
encountered in TS source (FR13) — no `.esc` output is produced
for them, which means no `export` and no `@js` either. The
parser does **not** learn the `intrinsic` keyword. Verify the
parser still rejects `intrinsic` after this workstream lands
(regression guard).

### 10.2 Adaptive diagnostic rendering (FR15)

Replace the global `renderType(t)` with
`renderTypeForLocation(t, scope)`. The renderer picks the
shortest unambiguous form for `t` given the bindings in scope at
the diagnostic's source location:

1. **Single-class shortcut.** If the file has a `?local` import
   whose package qualifies for the single-class shortcut (FR5),
   render as the capitalized class binding (`Array<number>`,
   `Date.now()`) — matching what the user would write.
2. **Namespace member.** `?local` without shortcut → `math.Foo`;
   `?nested` → `std.math.Foo`.
3. **Not imported.** Fully-qualified canonical name
   (`std:array.Array`) plus a "did you mean to
   `import "std:array"`?" hint pointing at the FR16 quick-fix.

**Tie-breaking.** When multiple forms are simultaneously in
scope (e.g. the file has both `import "std:array"` and
`import "std:array?nested"`), the renderer picks the shortest;
ties break in the order 1 → 2 → 3 above. The rendering is
per-diagnostic, not per-compilation — same type can render
differently in two files.

(Named imports from pseudo-packages are out of scope per
Non-goals, so the renderer has no "bare name" case to handle.)

Touch points: every diagnostic site that currently calls
`renderType(t)` needs threading of the file scope through the
diagnostic pipeline.

### 10.2a Diagnostic-assisted migration

When a name that used to be ambient is referenced without an
import under the new default, the **unbound-name diagnostic**
includes a suggestion ("did you mean to `import "std:async"`?")
whenever the unbound name matches a known pseudo-package export.
The suggestion list is derived mechanically from the LSP
name-index (§10.3); the diagnostic path reuses the same index.
This is the **fallback for command-line use** — users in a
supported editor get the FR16 quick-fix instead. Suggestion
text routes through the error-message taxonomy entries; spans
point at the bare reference, not the surrounding statement.

### 10.3 Auto-import quick-fix (FR16)

LSP first-class. Quick-fix on an unbound-name diagnostic that:

1. Adds the appropriate namespace import statement
   (`import "std:async"`, `import "std:math"`, …).
2. **Single-class shortcut packages:** leaves the bare reference
   unchanged (`Array.isArray`, `Date.now`, `Error(...)` already
   match the imported binding name).
3. **Other packages:** rewrites the bare reference to qualify
   through the resulting namespace (`sin(x)` → `math.sin(x)`;
   `Promise.all([...])` → `async.Promise.all([...])` since
   `Promise` lives in `std:async`, which is not a single-class
   shortcut package).

Named imports are out of scope. Quick-fix only adds namespace
imports.

Implementation:

- **Name → owning pseudo-package index.** Build at LSP startup
  by walking the resolved stdlib data directory (§2.2a) and
  reading top-level declaration names from each `.esc` file.
  Cache; **refresh on file change** via filesystem watch on
  the data directory — users editing their stdlib copy see the
  index update without restarting the LSP. Same index serves
  §10.2a diagnostic suggestions and §10.4 `--explain-type` hints.
- **Per-file binding-shape preference.** Default `?local`;
  user-configurable. The quick-fix follows the file's existing
  convention if any of its imports already pick a flag — e.g.
  if every other import in the file uses `?nested`, the
  quick-fix emits `?nested` too.
- **Name-collision suggestion ordering.** When the same name is
  exported by more than one pseudo-package (rare but possible
  for `Error` subclasses, etc.), the quick-fix offers each
  candidate as a separate fix; ranking by canonical
  alphabetical order, with `std:*` ranked before `web:*`.

Touch points: [cmd/lsp-server/](../../cmd/lsp-server/),
[internal/lsp/](../../internal/lsp/) (or wherever the LSP code
actually lives).

### 10.4 `--explain-type` diagnostic refinement

When a tag-keyed return is wider than expected
(`createElement` returning the union element type instead of
`HTMLCanvasElement`), the diagnostic suggests likely `web:*`
imports to widen the file's view. Complements the FR16
quick-fix for the type-narrowing case.

### 10.5 Source-map / diagnostic provenance

Per requirements §"Source-map and diagnostic provenance for
stdlib pseudo-packages":

- **Real filesystem path.** Spans on declarations parsed from
  stdlib `.esc` files carry the **actual resolved path**
  (e.g. `/usr/local/share/escalier/data/std/string.esc`), since
  the file is on disk and the user can open it directly. No
  virtual URI scheme is needed. When the resolved path lies
  under a well-known install prefix, diagnostics may render it
  as `<stdlib>/std/string.esc` for compactness, but the
  underlying span still carries the real path so editor
  click-through works.
- **Preserved line/column.** Line/column information from the
  parser is preserved as for any other file. The `Span` shape
  already carries this; no change.
- **LSP go-to-definition.** Clickthrough opens the resolved
  file directly — no materialization, no custom URI scheme.
  If the file is read-only (system install) the editor opens
  it in read-only mode; users who want to edit point
  `ESCALIER_STDLIB_DIR` at a writable copy.

Touch points: span construction in
[internal/parser/parser.go](../../internal/parser/parser.go)
already takes a filename; the resolver passes the resolved
path through unchanged.

**Gate.** LSP quick-fix integration test green; renderer fixture
per case (`?local` shortcut, `?local` non-shortcut, `?nested`,
no-import) passes; parser still rejects `intrinsic`.

---

## Cross-cutting

### Error taxonomy (per requirements §"Error-message taxonomy")

Each diagnostic ties back to the offending `import` statement,
ideally to the URI string literal (and within it, to the flag
portion when the failure is flag-shaped):

- **Unknown scheme** — names the scheme and lists the
  recognized set.
- **Unknown package within a known scheme** — names scheme +
  package; suggests near-spelling matches if cheap.
- **Invalid flag combination** — names the specific pair;
  explains mutual exclusion.
- **Unknown flag** — names the flag; lists recognized set.
- **Named import from a pseudo-package URI** — explains
  namespace-only; suggests the rewrite.
Fixtures under [fixtures/](../../fixtures/) exercise each with
full message-text assertions per CLAUDE.md test conventions.

### Testing strategy summary

Per requirements §"Testing strategy":

- Parser, resolver, binding-shape (§2).
- Closed registries, cross-package type references, inter-package
  cycles (§4); Symbol re-export aliases via `@js` (§3.5 codegen
  fixtures + §7 bootstrap review).
- Prelude switchover (§9) — internal fixtures, migrated to
  `import "std:*"` ahead of the switchover commit (§8), keep
  type-checking under the new resolver. No parity check against
  the legacy path: the legacy path is deleted in the same PR
  rather than kept behind a flag (pre-1.0).
- Adaptive diagnostic rendering (§10.2), auto-import quick-fix
  (§10.3), named-import rejection (§2 parser/resolver).
- Snapshot tests on converter output via `go-snaps`;
  `tools/dts_to_esc/ --check` runs in CI to catch upstream TS
  changes (§6.4).

### Non-functional requirements

- **Filesystem-resident stdlib data.** `.esc` files under
  `internal/interop/data/` ship alongside the compiler binary
  and are loaded from disk at compile time, **not** embedded
  via `//go:embed`. Discovery per §2.2a. Editability of
  builtins (tweaking a type, adding a package) without
  recompiling the compiler is the priority.
- **Zero runtime cost.** Pseudo-package imports erase at
  codegen.
- **Soundness of activation.** With closed registries (§4.2),
  this reduces to "a file sees a name iff it imported the
  package that owns it." The original FR9 per-file augmentation
  semantics are deferred along with FR7.
- **Ergonomics.** `?local` default; single-class shortcut keeps
  per-class access terse.

### Risks (from requirements §"Risks")

Tracked here for visibility; mitigations are baked into the
phasing above:

- **FR14 printer fidelity** — gated by §1; if the audit
  surfaces unsupported forms, parser/printer follow-ups
  precede §5.
- **Ergonomic cost of imports** — mitigated by auto-import
  quick-fix (§10.3, hard requirement), suggestion-bearing
  diagnostics (FR15/§10.2), and the single-class shortcut
  (FR5/§2.4).
- **Initial bootstrap quality** — mitigated by human review
  pass at §7 and by `--check` mode at §6.4.
- **Cross-package augmentation mechanism** — investigated by
  §4.1 spike; the conclusion was to deferred FR7 and ship
  closed registries (§4.2) for MVP. Risk re-emerges only when
  custom-element support is added (§4.5).
- **Polyfill story is separate.** This workstream assumes
  polyfill insertion at lowering is tractable (per FR12). No
  polyfill work happens here.

### Backwards-compatibility

**Not applicable — pre-1.0.** Escalier has no released compiler
yet, so there are no external users to migrate and no
deprecation cycle to manage. §9 swaps the prelude and deletes
the legacy paths in one PR; internal fixtures are migrated
(§8) in the commit immediately preceding so the suite stays
green.

Diagnostic-assisted migration (§10.2a) and the FR16 auto-import
quick-fix (§10.3) are still implemented — not for migration, but
because they are first-class ergonomics features under the new
model (FR15 / FR16 are hard requirements). No automatic codemod
for user code is included.

(The requirements doc's "Backwards-compatibility" section has
been updated in step with this plan: no deprecation cycle, no
build flag. FR15/FR16 ergonomics are framed as first-class
features, not as migration aids.)

---

## FR coverage matrix

A satisfaction check: every functional requirement maps to one
or more phases above.

| FR    | Topic                                      | Covered in                  |
| ----- | ------------------------------------------ | --------------------------- |
| FR1   | No ambient set; two-mode loading           | §6.1 (partition), §9 (lazy shape-load + legacy-path deletion), Drops subsection in §6.1 (`globalThis`/`eval`/`EvalError`) |
| FR2   | Pseudo-package layout                      | §2.2 (resolver mapping + underscore convention), §2.2a (stdlib data directory discovery), §6.1 (full enumeration), §6.3 (output layout + distribution) |
| FR3   | URI-scheme import grammar; runtime erasure | §2.1 (parser), §2.2 (resolver), §3 (decorator-based lowering + import erasure)                                |
| FR4   | Binding-shape flags                        | §2.3 (both shapes, mutual exclusion, extensibility, URI-keyed bookkeeping) |
| FR5   | Single-class shortcut                      | §2.4; eligibility list in §6.1                                                                                   |
| FR6   | Inter-package imports                      | §4.3 (cycles permitted within pseudo-package layer)                                                              |
| FR7   | DOM packaging; cross-package type references; open augmentation deferred | §4.2 (single `web:dom` package + standalone web siblings; closed registries; `createElementNS` stays one overloaded method on `Document`), §4.2b (qualified cross-package type references), §4.5 (deferred augmentation work scoped for the future custom-elements workstream), §4.6 (method-elem overload resolution on class/interface declarations — open prerequisite for §7 so converted DOM methods dispatch correctly). Spike (§4.1) showed achieving the old per-file-activation design needs two new checker subsystems; MVP sidesteps by collapsing the DOM tree into one package. |
| FR8   | Well-known symbol re-exports               | §7 step 2 (hand-authored re-export aliases with `@js("Symbol.<name>")`), §3 (decorator semantics carry the alias) |
| FR9   | Augmentation activation semantics          | N/A for MVP — single-`web:dom` partition (§4.2) requires no activation semantics. Original spec preserved in [requirements.md appendix](requirements.md#appendix-deferred-fr9-spec) for the deferred custom-elements work. |
| FR10  | Bootstrap converter                        | §5 (MVP, trio idiom, namespace flattening), §5.0 (JSDoc precursor), §6.1 (partition), §6.2 (routing), §6.4 (`--check`), §6.5 (`throws`), §6.6 (TS-bump workflow) |
| FR11  | Prelude changes; lazy shape loading        | §9.1 (trigger map), §9.2 (shared parsed copies), §9.2a (cross-package verification), §9.3 (legacy-path deletion in same PR) |
| FR12  | Always-current API; polyfills at lowering  | Acknowledged as out-of-scope dependency in cross-cutting; type checker sees modern surface unconditionally       |
| FR13  | Intrinsic types checker-resident           | §10.1 (handlers stay, `Awaited<T>` source-first with documented-fallback requirement, parser rejects `intrinsic`) |
| FR14  | Declaration printer audit                  | §1 (entire phase)                                                                                                |
| FR15  | Adaptive diagnostic rendering              | §10.2 (renderer + tie-breaking), §10.2a (migration suggestions)                                                    |
| FR16  | Auto-import (LSP first-class)              | §10.3 (quick-fix, name-index, binding-shape preference, name-collision ordering)                                  |

**Non-functional / cross-section coverage:**

- **Ergonomics, soundness of activation, zero runtime cost,
  filesystem-resident stdlib data** — Cross-cutting "Non-functional requirements".
- **`--explain-type` diagnostic** — §10.4.
- **Source-map and diagnostic provenance** — §10.5.
- **Error-message taxonomy** — Cross-cutting "Error taxonomy"
  (six failure modes).
- **Testing strategy** — Cross-cutting "Testing strategy
  summary".
- **Risks** — Cross-cutting "Risks", each tied to a mitigating
  phase.
- **Backwards-compatibility** — Cross-cutting
  "Backwards-compatibility".

**Things the requirements explicitly leave out** (so the
absence is correct, not a gap): lazy `.d.ts` → `.esc`
conversion for third-party npm packages,
`node_modules/.cache/escalier/`, per-dep / user-project
overrides, `escalier cache clean` CLI, original steps 10–11
(third-party lazy cache; deletion of the runtime interop
pipeline), `node:*` content, polyfill insertion at lowering,
and **named imports from pseudo-packages** (rejected with a
helpful diagnostic per the error taxonomy). None of these
appear as work items above.
