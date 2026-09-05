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
| 5   | Converter MVP (`tools/dts_to_esc/`)                  | FR10        | ✅      | §1, §3     | CLI at [tools/dts_to_esc/](../../tools/dts_to_esc/) wraps `dts_to_esc.ConvertToStandaloneModule` ([internal/dts_to_esc/dts_to_esc.go](../../internal/dts_to_esc/dts_to_esc.go)). Boolean-trio fusion + namespace flattening + `@js("...")` decoration land; gate fixtures in [internal/dts_to_esc/dts_to_esc_test.go](../../internal/dts_to_esc/dts_to_esc_test.go) (printed output parses; idempotent re-conversion; trio yields one `ClassDecl` and zero `VarDecl`; namespace slice emits zero nested namespaces). |
| 6   | Converter productionization                          | FR10        | 🚧      | §5         | PR A landed: hand-maintained partition map ([internal/dts_to_esc/partition.go](../../internal/dts_to_esc/partition.go)) with `Route` + DOM residual + unmapped-symbol fail-safe; partition pipeline ([internal/dts_to_esc/partition_writer.go](../../internal/dts_to_esc/partition_writer.go)) that buckets, interface-/namespace-merges across input files, converts each bucket, and writes the partitioned tree under `<out>/std/`, `<out>/web/`, `<out>/node/`; the seeding subcommand `dts_to_esc bootstrap <lib-dir> <out-dir>`. The whole pinned lib set now routes and converts: 49 packages under `std/` and `web/`, every emitted file reparses, and two runs produce identical trees. #1333 and #1340 closed the last printer and parser gaps. PR D landed: the AST-producing conversion moved to [internal/dts_to_esc/](../../internal/dts_to_esc/), leaving `internal/interop` with the runtime override store alone. The converter reads recorded receiver mutability through the `dts_to_esc.OverrideLookup` interface, which the store implements, so no converter file imports `type_system`. One transitive link survives through `internal/ast`, which names `type_system.Type` and `type_system.BindingOwner`; M12 re-homes those two references whether or not the converter moves. PR B landed check 1 (missing declarations and members) plus additive write mode, wired as `dts_to_esc check` and `dts_to_esc regenerate`; its §6.4 checks 2 and 3 run the solver's `constrain` over `soltype` and wait on SimpleSub M7.5. PR C landed the three pinned-lib subcommands `bootstrap` / `regenerate` / `check`, the unified-diff `check` report over `go-udiff`, and the bump walkthrough in [tools/dts_to_esc/README.md](../../tools/dts_to_esc/README.md). PR E ([#1341](https://github.com/escalier-lang/escalier/issues/1341)) supersedes B and C: a generated `.esc` becomes a build output written by one `generate` subcommand from the three inputs of §6.4, so [#1345](https://github.com/escalier-lang/escalier/issues/1345) removed the additive write mode, the `check` / `regenerate` split, and the `go-udiff` dependency, and regenerating and diffing subsumes B's outstanding checks 2 and 3 without waiting on SimpleSub M7.5. §6.8 sequences the four derived determinations onto the generated declarations and records the three trio shapes that do not fuse yet — 625 inline-constructor pairs deferred with `web:*` fusion, `Array`, and `Symbol` / `BigInt`. |
| 7   | Stdlib bootstrap (committed `.esc` files)            | FR1–FR2     | 🚧      | §6         | Run `generate` once; review; commit the tree together with the overlay and `curated.json` entries that produce it. Review never edits the output — a wrong determination costs a `curated.json` entry, an inexpressible shape costs an overlay `replace`, a systematic error is a converter fix (§7 step 2). The output is checker-agnostic `.esc` source, so this can land before SimpleSub M7.5 ingests it. The tree is committed: 49 packages, 40,312 lines, byte-identical on a re-run. Nothing type-checks it yet — the prelude loads the ES2015 lib subset while `generate` reads all 88 lib files, so every post-ES2015 global the tree declares fails the §3.4(4) `@js` target check ([#1402](https://github.com/escalier-lang/escalier/issues/1402)). Four `TestStdlibImport_*` tests and the `stdlib_import_local` and `stdlib_import_single_class` fixtures are disabled against it until M7.5. (§4.6 prerequisite for same-named method dispatch — `createElement`, `addEventListener`, `getContext`, … — landed with §4.)                                                                                                                                                                                                                                                                                            |
| 8   | Internal fixture migration                           | (before M12)  | ⬜ | §4, §7, M7.5 | Migrate Escalier's own fixtures to `import "std:*"`. The solver has no ambient surface, so this is what lets SimpleSub M8's second fixture harness run the `fixtures/` tree at all. Requires §7 because the imports resolve against the committed `.esc` files; requires §4 for any fixture that touches inter-package imports / the single-`web:dom` package + cross-package type references. The old checker keeps resolving previously-ambient names while it exists, so the added imports are additive and both harnesses stay green. |
| 9   | Per-file shape loading in `internal/solver`          | FR11, FR12  | ⬜      | §2, §4, §7, §8, M7.5 | Add the FR11 trigger map on top of M7.5's import ingestion, so a file gets a literal's or language feature's method surface without naming the owning package. There is no switchover: the solver never had an ambient lib, and the legacy `internal/checker/` machinery goes out with the M12 flip, so §9.3 is an audit rather than a deletion PR. Re-home the §3.4 loader rules: rules 1–3 are AST-only and move to the pseudo-package load path; rule 4 (`@js` arg validation) needs a parsed TS lib, which only the old checker has via `GlobalScope.Namespace.Values` in [js_globals.go](../../internal/checker/js_globals.go), so it becomes a CI-only test that freshly parses the pinned `lib.*.d.ts` and validates every `@js("...")` arg across the committed stdlib. Same test adds **rule §3.4(5): `@js` decl shape matches lib target** — locate the lib member named by each `@js("...")` and assert: `readonly` / getter-only lib member ⇒ Escalier decl is `val` or `get`, never `var`; setter-only ⇒ `set`; method ⇒ `fn`. Catches stdlib stubs that silently make readonly things look writable. Today `@js("Math.PI") export declare var PI: number` compiles and lowers to a `Math.PI = ...` that TypeErrors at runtime. Rule 5 shares the lib parse with rule 4, so doing them separately would duplicate it. |
| 10  | Intrinsics, adaptive rendering, LSP support          | FR13, FR15, FR16 | ⬜ | M7.5, M9, M11, M11.5 | Implement adaptive diagnostic rendering (FR15) over the `soltype` printer and the auto-import quick-fix (FR16) on the solver-backed LSP (SimpleSub M11); verify the `Awaited<T>` source-level definition with documented-fallback policy; confirm the intrinsic handlers stay solver-resident (FR13). |

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
references. §9 adds the per-file shape loader; §10 adds
LSP/diagnostic tooling on top.

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
`.esc` files. §8 gives every fixture explicit `import "std:*"`
statements, which the solver requires and the old checker
tolerates; §9 adds the lazy shape-load path so a file gets a
literal's method surface without importing it; §10 adds the LSP /
diagnostic tooling on top.

**Why §8 precedes §9.** §9's trigger map is only observable once
fixtures stop leaning on ambient names. A fixture that still
expects a bare `Math` fails on the solver whether or not the
trigger map exists, and that failure masks the trigger map's own
bugs. §8 is feasible once §7 has committed the `.esc` files so
imports resolve and §4 has landed so any DOM-touching fixture can
use the single-`web:dom` package plus cross-package type
references; the resolver from §2 is in place transitively via §7.
The old checker still resolves previously-ambient names during the
fixture-rewriting commit, so added imports are additive and both
checkers stay green. §10 (LSP, adaptive rendering, intrinsic
verification) does not depend on §9 at all — nothing in it needs
the trigger map. Its constraints are milestone-shaped instead:
M7.5 so a diagnostic has a real stdlib type to render, M9 for the
type-level operators behind the intrinsic surface, and M11 for the
solver-backed LSP the quick-fix attaches to. §9 and §10 can
therefore proceed in either order or in parallel.

## Target checker: `internal/solver`

Everything from §6 PR B onward lands against
[internal/solver](../../internal/solver/), not
[internal/checker](../../internal/checker/). The SimpleSub
migration ([planning/simple_sub/](../simple_sub/)) replaces the
unification-based checker with an algebraic-subtyping one, and its
M12 flip deletes `internal/checker/` and `internal/type_system/`
wholesale. §1 through §6 PR A landed against the old checker
because it was the only checker at the time; they are done and are
not revisited here. Every phase below assumes the solver is the
only checker.

Three consequences shape the remaining phases.

1. **There is no ambient global lib to switch away from.** The
   solver does not walk `lib.*.d.ts` and has no `globalThis`
   surface. A stdlib type is reached through a `std:` / `web:` /
   `node:` import, or through the handle the solver holds on the
   fixed well-known protocol set — `Promise` for `await`,
   `Iterable` for `for (x in xs)`, `Generator` for `yield`. See
   [01-milestones.md](../simple_sub/01-milestones.md) §M7.5. FR1's
   no-ambient-set requirement is the solver's starting posture
   rather than a cut-over, so §9 shrinks from "swap the prelude and
   delete the legacy paths" to "add the per-file shape-load trigger
   map on top of M7.5's ingestion".
2. **The legacy builtin machinery is deleted by the M12 flip, not
   by this workstream.** `loadGlobalDefinitions`,
   `populateSelfParams`, `UpdateMethodMutability`,
   `mergeReadonlyVariant`, `BuildBuiltinStore`, and `js_globals.go`
   all live in `internal/checker/` and go out with that tree. The
   `nonMutatingOverrides` table sits in `internal/dts_to_esc/`, and
   `UpdateMethodMutability` is the only thing that applies it, so
   nothing applies it after the flip. The ecma-262 §6 validation diff
   reads it to report on, which is what keeps it compiling until that
   diff retires. `Classify` reads the override store and the name
   heuristics, never that table. §9.3 keeps the
   list as an audit that nothing in it grew a solver-side twin.
3. **`type_system.Type` is not a target representation.** The
   converter emits `*ast.Module`, which both checkers consume, so
   §5 and §6 PR A need no rework at the source level. §6.7 PR D
   moved the conversion into `internal/dts_to_esc`, so no converter
   file imports `type_system`. The one link left is transitive:
   `internal/ast` names `type_system.Type` and
   `type_system.BindingOwner`, and M12 re-homes those two
   references. Everything downstream of the AST is written against
   `soltype` and the solver's `constrain`: the overlay `replace`
   drift check (§6.4), shape loading (§9), and type rendering
   (§10.2).

**SimpleSub milestone dependencies.** The remaining builtins phases
sequence against the milestones in
[01-milestones.md](../simple_sub/01-milestones.md):

| Builtins phase | Needs                | Why                                                                                                                             |
| -------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| §6 PR B        | M7.5                 | The signature-drift check compares two declaration modules through the solver's `constrain`, which needs stdlib ingestion (§6.4). |
| §6 PR C        | —                    | CLI subcommands and docs over PR A and PR B's behavior.                                                                          |
| §6 PR D        | before M12           | Landed. No converter file imports `type_system` any more; the residual link runs through `internal/ast`, which M12 re-homes (§6.7 PR D). |
| §7             | M9 for `Awaited<T>`  | Committing `.esc` files is checker-independent; the recursive conditional needs the type-level operators from M9 (§7 step 3).    |
| §8             | M7.5, M8             | The second fixture harness runs the solver over `fixtures/`, where imports are mandatory because nothing is ambient.             |
| §9             | M7.5, §8             | The trigger map loads packages through M7.5's ingestion path.                                                                    |
| §10            | M7.5, M9, M11, M11.5 | A real stdlib type to render, type-level operators for the intrinsic surface, the LSP on the solver, and the diagnostics-rendering capstone. Not gated on §9. |

§7 is the one phase that can land well ahead of its consumer: it
commits source files that nothing type-checks until M7.5 arrives.

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
- **Converter symmetry (#664).** The same "decorators only on
  decls that lower to a JS reference" rule governs the `.d.ts`
  converter. `attachJSDecorator` in
  [internal/dts_to_esc/dts_to_esc.go](../../internal/dts_to_esc/dts_to_esc.go)
  stamps `@js("...")` only on `VarDecl`, `FuncDecl`, and
  `ClassDecl`; `TypeDecl` and `InterfaceDecl` are excluded by
  design and carry no `Decorators` field, so those branches are
  intentional no-ops. A bare exported type alias or interface is
  therefore emitted *unmarked*, which is correct: it erases at
  codegen and has no runtime reference to point at (and a printed
  `@js("...")` on it would not even re-parse, given the
  parse-time rejection above). `TestStandalone_TypeAliasExportedNoDecorator`
  pins this.

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
5. **Receiver mutability seeding.** Run `dts_to_esc.Classify`
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
| `std:disposable`  | bundled             | `Disposable`, `AsyncDisposable`, `DisposableStack`, `AsyncDisposableStack` — the `using` / `await using` protocol. `SuppressedError` is an `Error` subclass and goes to `std:error` |
| `std:decorators`  | bundled             | The TC39 decorator context types (`DecoratorContext`, `ClassMethodDecoratorContext`, …) and the `Symbol.metadata` types. TypeScript's legacy `experimentalDecorators` aliases are dropped instead — see Drops |

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

`ClassDecorator`, `PropertyDecorator`, `MethodDecorator`, and
`ParameterDecorator` from `lib.decorators.legacy.d.ts` join
them. They type the calling convention `tsc` emitted before
TC39 decorators, so they describe a compiler output shape
rather than a runtime one.

**Dropped source files.** Two source files contribute nothing,
dropped whole rather than name by name.

`lib.scripthost.d.ts` declares the Windows Script Host surface
— `ActiveXObject`, `WScript`, `VBArray`, the `TextStream`
types, and the COM value wrappers `SafeArray` and `VarDate` —
and Escalier targets browsers and Node. It also augments
`Date` with `getVarDate(): VarDate` and `DateConstructor` with
`new (vd: VarDate): Date`. Both of those names route to
`std:date` by name, so dropping `VarDate` alone would leave
`std/date.esc` referring to a type nothing declares. Dropping
the file takes the augmentations with it.

`lib.webworker.*.d.ts` is the Web Worker host lib. TypeScript
ships it and `lib.dom` as alternatives that a `tsconfig.json`
picks between, so the two restate every shared global and
differ only in the surface each host has. `interface
ReadableStream<R = any>` is byte-identical in both. The
partition covers the browser, and neither half of the worker
lib belongs in it. The restatement would let the within-bucket
declaration merging below concatenate two member lists into
one interface carrying each member twice. The remainder names
what only a worker has — `ServiceWorkerGlobalScope`,
`FetchEvent`, `importScripts` — which no browser program can
reach.

Serving workers means a set of pseudo-packages scoped to that
host, the same question Node raises, and both are deferred.
Until then `web:workers` carries the document side alone: what
a page constructs and the events it gets back. The scope a
worker runs inside is declared only by the ignored lib, so no
entry for it would match.

See `DroppedSources` in
[internal/dts_to_esc/partition.go](../../internal/dts_to_esc/partition.go).

**Unmapped-symbol fail-safe.** Per FR10 step 4: any top-level
TS-lib declaration name absent from both this partition table
and the explicit drop list above causes the converter to abort
with a diagnostic naming the symbol, its source `.d.ts` file,
and this partition-table file. No catch-all "unmapped" package
and no silent drop. The check lives at the partition-table
lookup site so misses surface where the routing decision is
actually made.

**`node:*`.** Partition deferred until Node support lands. The
`internal/interop/data/node/` directory is scaffolded with a README
explaining its reserved status; no `.esc` files are emitted there
until the Node workstream begins.

**Within-bucket declaration merging.** TypeScript spreads the
declaration of stdlib types across many `lib.*.d.ts` files —
`interface Array<T>` is augmented by `lib.es5`, `lib.es2015.core`,
`lib.es2015.iterable`, `lib.es2015.symbol`,
`lib.es2016.array.include`, … and similarly for `Date`,
`Promise`, `String`, `Math`, etc. Every contributing file routes
to the same target package, so once the routing pass has placed
each top-level declaration in its bucket the converter performs
TS-style declaration merging *inside* the bucket: same-named
`InterfaceDecl`s have their `Members` concatenated, and same-named
`NamespaceDecl`s have their `Statements` concatenated (recursively)
before trio detection runs. This guarantees the trio detector sees
exactly one merged `interface Array` + one merged `interface
ArrayConstructor` + one `declare var Array`, regardless of how many
lib years contributed members. See `mergeDecls` in
[internal/dts_to_esc/partition_writer.go](../../internal/dts_to_esc/partition_writer.go).
Distinct-name interfaces (e.g. `interface Array<T>` and `interface
ReadonlyArray<T>`) are *not* merged by this pass — they are
separate TS types and stay distinct after routing. The Readonly-
twin pass below handles them.

**Readonly-twin fusion (mirrors `mergeReadonlyVariant`).** This
runs in the converter, not the checker, so it survives the deletion
of `internal/checker/` — the mutability it bakes into the emitted
`.esc` files is what replaces the legacy prelude's merge pass.
Per-class
buckets often contain a `Readonly<X>` companion (`ReadonlyArray`,
`ReadonlyMap`, `ReadonlySet`, …). The legacy prelude
[`mergeReadonlyVariant`](../../internal/checker/prelude.go) treated
presence on the readonly twin as positive evidence the method does
not mutate; the new converter does the same at emission time:

1. Detect any `interface ReadonlyFoo` whose mutable counterpart
   `interface Foo` is also in the bucket. Detection is purely
   structural (the `Readonly` prefix strip + companion lookup);
   no enumeration table is needed.
2. Append any `ReadonlyFoo` member not already present on `Foo`
   to `Foo.Members` so the fused class carries the readonly
   surface too.
3. Drop the `ReadonlyFoo` declaration from the bucket, with
   nothing standing in for it. Escalier spells the immutable view
   `Foo<…>` and the mutable one `mut Foo<…>`, so once the rewrite
   below respells every reference the readonly name has nothing
   left to denote, and an alias would only offer a second spelling
   for a type that already has one.
4. After trio fusion produces `class Foo`, post-process each
   instance `MethodElem`: if the method name appears on the
   readonly twin's member set, set `Receiver.Mut = false`;
   otherwise leave whatever `ClassifyMethodByName` chose
   (defaulting to `mut self`). Symbol-keyed methods
   (`[Symbol.iterator]`) participate in the merge via dotted-
   name stringification of the computed key.

See `fuseReadonlyTwins` / `applyReadonlyTwinReceivers` /
`appendReadonlyAliases` in
[internal/dts_to_esc/partition_writer.go](../../internal/dts_to_esc/partition_writer.go).

**Step 4 is retired.** The twins were a proxy for receiver
mutability: presence on `ReadonlyFoo` said the method does not
mutate. The ECMA-262 derived facts answer that from the
specification instead, and `generate` reports zero disagreements
between the two over the pinned lib set, so the twin adds nothing
the facts do not already settle. §7 recorded the decision and
[#1408](https://github.com/escalier-lang/escalier/issues/1408)
carries the removal. Steps 1 through 3 stay: the twin's members
still reach the fused class, and `ReadonlyFoo` still leaves the
bucket.

**The reference rewrite runs over every bucket at once.** A twin's
declarations and the references to them sit in different packages.
`Array` and `ReadonlyArray` are declared in `std:array`, and 35
other packages reference them without declaring either — `web:dom`
alone writes 119. Rewriting a bucket against only the twins it
declares leaves every other package spelled the TypeScript way: a
bare `Array<T>` where Escalier means `mut Array<T>`, and a
`ReadonlyArray<T>` naming nothing the tree declares. So
`ConvertBuckets` fuses every bucket before converting any of them
and passes the whole twin set to the rewrite. Receiver flips stay
bucket-local, since only the declaring bucket holds both member
lists.

An `extends` clause keeps the mutable name as written, and that
name denotes the whole definition rather than the immutable view of
it.
A definition holds both `self` and `mut self` methods, and
extending it inherits all of them, so `interface RegExpMatchArray
extends Array<string>` gives `RegExpMatchArray` every `Array`
member including `push`. Whether a given instance may call `push`
is settled where it is bound, because `push` takes `mut self`.
Reading the bare name as the immutable view would silently drop
the mutating half of the inherited surface. `mut` has no place to
go here in any case: an `extends` clause is typed
`*ast.TypeRefTypeAnn`, which carries no wrapper.

The same reading covers an `extends` clause the rename touched.
`interface RTCStatsReport extends ReadonlyMap<string, any>`
becomes `extends Map<string, any>`, which inherits `set` as well
as `get`. Nothing is lost, because `set` takes `mut self` and a
`RTCStatsReport` binding is immutable unless it says `mut`.

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

### 6.4 Generation model and its inputs

A generated `.esc` file is a **build output**. A run writes each
generated package from scratch and reads no generated file back, so
`git diff` is the review surface for a TS-version bump and the tool
carries no differ of its own. A run also deletes the generated
packages it no longer emits, so a package that stops being routed
leaves the tree instead of lingering as a stale file no input
accounts for. Hand-authored packages are exempt, per the explicit
list §6.6 describes. Tracked as PR E in [#1341](https://github.com/escalier-lang/escalier/issues/1341).

Every fact in a generated file comes from one of three committed
inputs:

1. **Upstream shape** — the pinned `lib.*.d.ts` set today, WebIDL
   for `web:*` later.
2. **Derived facts** — [internal/ecma262/](../../internal/ecma262/),
   which resolves receiver mutability, parameter disposition, return
   borrow, `throws`, and `rejects` from the committed `cfg.json`.
   `curated.json` answers, per determination, what the control-flow
   graph does not settle.
3. **Overlay** — hand-written `.esc` fragments under
   `internal/interop/overlay/`, supplying declarations no upstream
   source has, standing in for ones it expresses wrongly, and removing
   ones Escalier has no home for.

**The operation is in the filename, not the file.** An overlay file is
ordinary `.esc`, and every declaration in it takes that file's
operation:

```
internal/interop/overlay/std/symbol.add.esc
internal/interop/overlay/std/array.replace.esc
```

The alternative was a per-declaration marker, and the natural one does
not exist. Decorators are the only annotation the parser has, and
[decl.go](../../internal/parser/decl.go) rejects them on `interface`,
`type`, `enum`, and `namespace` declarations because those have no
runtime form. Over the pinned lib set that is 1352 interfaces and 339
type aliases out of 2787 generated top-level declarations, so a
decorator could not mark 61% of the tree. The class-member parser does
not read decorators at all, so a member could not be marked either.
Overlay files are the ones the generator parses, so marking them means
changing the parser first. Filenames avoid that.

**`add` reuses the merge that already exists.** `mergeDecls` in
[partition_writer.go](../../internal/dts_to_esc/partition_writer.go)
collapses same-named interfaces across input files by concatenating
their members — it is how the seven `interface Array<T>` declarations
spread across lib years become one. An `.add.esc` file enters as one
more input to that merge:

```
// overlay/std/symbol.add.esc
export declare interface SymbolConstructor {
    readonly customMatcher: unique symbol,
}
```

No decorator is needed on the member, because
`export declare var Symbol: SymbolConstructor` already carries
`@js("Symbol")` and members lower through it. The
[FR8](requirements.md) symbol re-exports are the flat form of the same
file and do carry their own, being top-level:

```
@js("Symbol.iterator")
export declare val iteratorKey: unique symbol
```

**`replace` is member-granular**, and takes the same file shape as
`add`. The overlay declares the members it is standing in for, and each
one substitutes the converted member sharing its key rather than being
appended beside it:

```
// overlay/std/array.replace.esc
export declare interface Array<T> {
    sort(compareFn?: fn (a: T, b: T) -> number) -> Self,
}
```

`add` and `replace` therefore differ only in what happens on a key
collision, not in syntax. A member's key is
[memberSlot](../../internal/dts_to_esc/decl_key.go): its name, which
side of the class it lives on, and its kind. Names cover idents, string
literals, and `[Symbol.*]` computed keys.

The substitution runs in
[ApplyOverlay](../../internal/dts_to_esc/overlay_apply.go), after the
conversion and the trio fusion, so the overlay is matched against the
converted declarations rather than against the `.d.ts` shapes. A trio
TypeScript spells as `interface Foo` + `interface FooConstructor` +
`declare var Foo` is therefore addressed as the single `class Foo` the
generated file holds. Substituting in place rather than appending is
what keeps regeneration byte-identical.

Whole-declaration replacement stays available for a shape the converter
gets structurally wrong, where restating members one at a time does not
express the fix.

Two rules the granularity needs. An overlay replaces a name's **entire
overload set** and restates every signature in it, since a name alone
cannot pick one of `Array.find`'s two signatures apart and a
per-signature key would have to tiebreak the twelve overload sets that
differ only in parameter types. Restating fewer signatures than the
converted declaration holds fails the run and names the member, and an
`add` file contributes a whole set the same way. And the key carries
member kind, so a `readonly x: T` and a `get x()` do not collide
silently. An overlay that writes a name under another kind fails rather
than substituting across kinds, so changing a member's kind is a `drop`
and an `add`. A `get x()` and a `set x()` are the one pair that shares a
name, and an overlay adds the missing half of one.

Granularity shrinks the staleness hazard rather than removing it. A
replaced member is still forked from upstream, but a change to any
other member of the same declaration still flows through, which
declaration-granular replacement swallows. What stays forked is pinned
by a digest. Beside each `replace` file sits a `.digests.json` sidecar
recording the printed Escalier form of every converted declaration and
member that file stands in for, and a run whose converted form no
longer matches the recorded one fails and names the member.
`dts_to_esc generate --update-digests` rewrites the sidecars, which is
how a contributor records a new entry or accepts a moved one.

The digest is taken over the printed member with its doc comment left
out, so the prose churn of a version bump moves nothing. A printer
formatting change does invalidate every entry at once.

Taking it over the converted form rather than over any one input is
what lets it widen on its own. It covers the `.d.ts` shape, the trio
fusion, and every derived determination the generator has wired in, so
a fact that starts shaping a declaration starts moving its digest with
no change here. §6.8 keeps that true by applying each determination
before the overlay. The eventual answer is the comparison this section defers to
SimpleSub M7.5: infer both sides and ask the solver's `constrain`
whether the overlay member is still compatible with the converted one.
That is robust to formatting, and it is the one check §6.4 still wants
`constrain` for.

**`drop` names what the generator must not emit.** A drop file's
declarations are read for their **names alone** — every type
annotation, signature, and body in one is ignored, so a drop is written
in the smallest form that parses:

```
// overlay/drop.esc — whole symbols, package-less
export declare val eval
export declare val globalThis

// overlay/std/date.drop.esc — members of a package's declarations
export declare interface Date {
    getYear: unknown,
}
```

`export declare val <name>` needs no type annotation and `<name>:
unknown` is the shortest member form, so neither invites inventing a
signature that would then be ignored. Generation rejects a drop entry
carrying more than that, which is what keeps "the rest is ignored" from
becoming a trap for whoever writes a real signature and expects it to
matter. A member drop removes every member under that name, overload
set included, matching the rule `replace` follows.

Drops were a Go set, `ExplicitDrops` in
[partition.go](../../internal/dts_to_esc/partition.go). Moving them to
the overlay is what lets a user drop something: §6.3 ships the tree
alongside the binary so builtins can be tweaked post-install, and a set
compiled into the binary can never be extended that way. The tiered
override precedence the runtime store already implements — user project
over user dependency over builtin — is the shape a user-supplied
overlay would slot into later.

Two consequences to carry into the implementation. A whole-symbol drop
resolves in `Route`, before a package is assigned, which is why
`overlay/drop.esc` sits at the overlay root rather than under a package
— `eval` and `globalThis` belong to no package. And the generator must
therefore parse the overlay *before* routing, where today `ExplicitDrops`
is a compile-time constant available to `Route` for free.

The unmapped fail-safe still enforces totality over the lib set: every
top-level name must be routed, dropped, or caught by the DOM residual,
or the run fails. That check is dynamic either way, so it survives the
invariant now spanning a Go map and a data file. `UnmappedError`'s text
has to stop naming `ExplicitDrops` and point at the overlay instead.
[#1357](https://github.com/escalier-lang/escalier/issues/1357) migrates
the 19 entries and makes both changes.

`DroppedSources` is separate and stays in Go. It names whole `.d.ts`
files the partition skips, which is input selection rather than a
statement about any declaration.

**Why the tree is not hand-edited.** An earlier design made a re-run
additive so hand-edits to the committed tree would survive it. Every
read of a committed `.esc` followed from that promise: parsing each
package file, diffing it by name against converter output, merging,
the `check` / `regenerate` split, and an error taxonomy for a tree
the tool cannot re-read. Dropping the promise removes all of it.
[#1345](https://github.com/escalier-lang/escalier/issues/1345)
retires the machinery.

**Drift detection no longer waits on the solver.** The additive
model needed three checks: missing declarations, incompatible
signature drift, and incompatible property-type drift. Checks 2 and
3 compared both sides through [internal/solver](../../internal/solver/)'s
`constrain`, which needs the solver to ingest a declaration module —
SimpleSub M7.5. Regenerating and diffing catches all three at once
and needs no checker, so drift detection comes off M7.5's critical
path. One check still wants `constrain`, comparing an overlay
`replace` against the upstream declaration it stands in for. Until
M7.5 lands, the digest sidecars above stand in for it: they catch a
converted form that moved, at the cost of reporting a printer
formatting change as movement too.

**Declarations the generator drops on purpose** are the overlay's
`drop` files above — `globalThis`, `eval`, and the `intrinsic`-typed
declarations per the Drops subsection in §6.1. They are an input like
any other, so the regenerate-and-diff check pins them: changing what is
dropped changes the tree.

### 6.5 `throws` annotations

Per [FR10](requirements.md#fr10-bootstrap-converter-tools-dts_to_esc),
`throws` was originally a hand-curation job: the high-value ~50
entries such as `JSON.parse`, `decodeURI*`, `BigInt`, `fetch`, and
`Response.json`. Scraping MDN was rejected as prose-not-data,
brittle, and copyleft.

For `std:*` that curation is now a fallback rather than the plan.
ECMA-262 §9.1 derives throw sets from the control-flow graph, §9.2
filters the coercion sites a declared type rules out, and §9.3
splits synchronous throws from asynchronous rejections. A curated
entry answers what the graph does not settle, per §6.8.

For `web:*` there is no derived source yet. WebIDL `[Throws]`
extraction through `@webref/idl` is the plausible lever and arrives
with the web-specs work described in §6.8; until then a `web:*`
`throws` annotation is an overlay `replace`.

### 6.6 TS-version-bump workflow

**CLI shape.** `tools/dts_to_esc/` is a single Go binary with one
generating subcommand:

- `dts_to_esc generate <lib-dir> <esc-dir>` — writes the whole tree
  from the three inputs of §6.4 and reads no generated file back.

It replaces `bootstrap`, `regenerate`, and `check`. Seeding an empty
tree and re-running against a populated one are the same operation
once the run reads no committed output.
[#1342](https://github.com/escalier-lang/escalier/issues/1342) lands
it alongside the overlay.

The bump workflow in `tools/dts_to_esc/README.md`:

1. Bump the pinned TypeScript version in `package.json` and run
   `pnpm install`.
2. Run `dts_to_esc generate node_modules/typescript/lib internal/interop/data`.
3. Review `git diff` and commit. A removal upstream shows up as a
   deletion in the diff rather than as a report, because the run
   does not carry the old tree forward.
4. An overlay `replace` or `drop` naming a declaration the upstream
   source no longer has fails the run and names it. That is the
   TS-side-removal signal, keyed on the overlay rather than on the
   output tree.

**CI.** A job runs `generate` and then fails if the tree changed. A
dirty tree means the committed output does not match its inputs,
and the failure prints the diff. It catches an upstream change
nobody regenerated and an in-place edit of a generated file with
the same test.

`git diff --exit-code` is the wrong check here: it compares tracked
files only, so a run that emits a **new** package leaves it
untracked and the check passes. Stage first, then diff the index:

```
dts_to_esc generate node_modules/typescript/lib internal/interop/data
git add -A internal/interop/data
git diff --cached --exit-code internal/interop/data
```

That reports all three cases — a modified package, a package the
run no longer emits, and a package the run newly emits. The job
needs coverage for each, since only the modified case is the one a
naive check gets right.
[#1344](https://github.com/escalier-lang/escalier/issues/1344).

The check is
[.github/scripts/check-generated-tree.sh](../../.github/scripts/check-generated-tree.sh),
and the `check_generated_tree` job runs it. The tree that job
reads is one it seeds, since `internal/interop/data` holds the
§2-era stubs rather than generated packages, so what it gates is
generator idempotence over the pinned lib set. Pointing the check
at the committed tree gates that tree against its inputs and lands
with §7, [#1393](https://github.com/escalier-lang/escalier/issues/1393).

**Review ergonomics.** `web/dom.esc` is roughly 22.5k lines, so a
generator change rewrites it wholesale and the diff dominates
review. Mark the generated tree `linguist-generated` in
`.gitattributes` so those files collapse by default.
[#1353](https://github.com/escalier-lang/escalier/issues/1353).

**Gate.** Every output `.esc` file under
`internal/interop/data/{std,web}/` parses; generation is idempotent,
so a second run leaves the tree byte-identical; the partition
matches [FR1](requirements.md#fr1-no-ambient-set-shape-loaded-vs-named-bindings)
member-for-member; CI fails when the committed tree does not match
its inputs.

### 6.7 PR sequencing

§6 lands as five PRs after §5 is in. The split keeps each review
surface focused; bundling is possible but loses the isolation
between partition-routing churn and the generation-model change
that PR E makes.

A. **Partition table + routing + output layout** (6.1, 6.2, 6.3) ✅.
   Takes the §5 converter from "stdout, one file" to "full
   `lib.*.d.ts` set, partitioned tree under
   `internal/interop/data/{std,web}/`". Lands the hand-maintained
   Go partition map, registry routing (single `web:dom`, sibling
   `web:*` packages, well-known-symbol stay-put rule), the
   unmapped-symbol fail-safe, and the `node/` empty directory.
   `XxxConstructor` follows its `Xxx` partition entry via a
   suffix-strip fallback in [Route](../../internal/dts_to_esc/partition.go)
   so contributors only list the instance name. Cross-input
   declaration merging (TS-style interface/namespace merging) runs
   inside each bucket before trio fusion, so members spread across
   lib years collapse onto one synthesized class.

B. **`--check` mode + re-run semantics** (6.4) — superseded by
   PR E. Landed additive write mode and the declaration-level
   check behind `CheckPartition` / `RegeneratePartition`, wired as
   the `check` and `regenerate` subcommands. Its outstanding checks
   2 and 3 waited on SimpleSub M7.5 and are subsumed by E's byte
   diff;
   [#1345](https://github.com/escalier-lang/escalier/issues/1345)
   removed the machinery.

C. **TS-version-bump workflow** (6.6) — superseded by PR E. Landed
   the three pinned-lib subcommands `bootstrap`, `regenerate`, and
   `check` on `tools/dts_to_esc/`, and
   [tools/dts_to_esc/README.md](../../tools/dts_to_esc/README.md)
   documents the bump steps. `check` prints the unified diff a
   `regenerate` run would apply, rendered from that run's own
   insertion points and through `go-udiff`, so the report and the
   write cannot disagree about the change.
   PR E replaces all three subcommands with `generate`, so the
   bump steps C documented are the ones §6.6 now states and
   [#1345](https://github.com/escalier-lang/escalier/issues/1345)
   removed the code and the README section behind them. C's
   outstanding CI job is superseded too: the job runs `generate`
   and diffs, not `check`.

D. **Free the converter of `internal/type_system`.** ✅ The
   AST-producing conversion lives in `internal/dts_to_esc`:
   `dts_to_esc.go`, `partition.go`, `partition_writer.go`,
   `module.go`, `decl.go`, `helper.go`, `twin_rewrite.go`, and the
   mutability classifier `mutability.go`. `internal/interop` keeps
   the runtime override store — `store.go`, `class_shapes.go`,
   `extract.go`, `merge.go`, `consistency.go`, `loader.go` — along
   with the `data/` tree and the stdlib-directory lookup.

   The two halves meet at `dts_to_esc.OverrideLookup`, a one-method
   interface the classifier consults for a recorded receiver
   mutability. `*interop.OverrideStore` implements it by addressing
   the member with `pathForMember` and reading the receiver off the
   stored `*type_system.FuncType`. That keeps every `type_system`
   reference on the store's side of the boundary. The dependency
   runs `interop` → `dts_to_esc`, since the store also names the
   seven-tier `dts_to_esc.ResolutionTier` ladder that
   `Effective.Source` records.

   The converter's own imports are now `ast`, `parser`, `printer`,
   `dts_parser`, and `set`. `go list -deps ./tools/dts_to_esc`
   still reports `type_system` because `internal/ast` names
   `type_system.Type` and `type_system.BindingOwner`. Cutting that
   last edge means changing how every AST node stores its inferred
   type, which is M12's work rather than converter surgery.
   Removing the runtime override store itself is the third-party
   workstream's call.

E. **Generate the tree instead of merging into it** (6.4, 6.6) ✅.
   [#1341](https://github.com/escalier-lang/escalier/issues/1341).
   Makes a generated `.esc` a build output: one `generate`
   subcommand writes the tree from the three inputs of §6.4 and
   reads nothing back. Lands the overlay layer and `generate`
   ([#1342](https://github.com/escalier-lang/escalier/issues/1342)),
   the regenerate-and-diff CI job
   ([#1344](https://github.com/escalier-lang/escalier/issues/1344)),
   and the removal of the additive re-run machinery
   ([#1345](https://github.com/escalier-lang/escalier/issues/1345)).
   It supersedes PR B: the additive write mode and the `check` /
   `regenerate` split it added are what E retires, and the byte
   diff subsumes B's outstanding checks 2 and 3 without waiting on
   SimpleSub M7.5.

   The gate's last clause, CI failing when the committed tree
   does not match its inputs, needs a committed tree to read.
   The job #1344 landed runs against a tree it seeds, which
   gates generator idempotence over the pinned lib set.
   [#1393](https://github.com/escalier-lang/escalier/issues/1393)
   points it at `internal/interop/data` alongside §7.

6.5 (`throws`) is scope and policy rather than code — fold into
whichever PR is convenient. §6.8 sequences the fact application,
which lands after E.

### 6.8 Applying the derived facts

Four determinations reach a generated declaration from
[internal/ecma262/](../../internal/ecma262/). Escalier can spell all
four today — `throws T`, `mut T` on a parameter, `mut self`, and
`&'a mut T` with lifetime parameters all parse and print — so what
each needs is generator wiring, not language work.

| Determination | Derived by | Applied by |
| --- | --- | --- |
| Receiver mutability | ecma-262 §4.1, §4.3 | ecma-262 §7 ([#1200](https://github.com/escalier-lang/escalier/issues/1200)) |
| Parameter mutability | ecma-262 §8.1 ([#1201](https://github.com/escalier-lang/escalier/issues/1201)) | [#1352](https://github.com/escalier-lang/escalier/issues/1352) |
| Ownership and borrowing | ecma-262 §8.1 `escape`, §8.2 ([#1202](https://github.com/escalier-lang/escalier/issues/1202)) | [#1352](https://github.com/escalier-lang/escalier/issues/1352) |
| `throws` and `rejects` | ecma-262 §9.1–§9.3 | [#1352](https://github.com/escalier-lang/escalier/issues/1352) |

ECMA-262 §7 auto-applies receiver mutability alone and routes the
other three through curation. [#1352](https://github.com/escalier-lang/escalier/issues/1352)
changes that: each of the four is emitted from its fact, with
`curated.json` supplying the determinations the graph does not
settle. Curation stays the correction channel rather than the
delivery channel.

As of the pinned lib set the generated tree carries 394 `mut self`
receivers, all of them from the name-tier classifier in
[mutability.go](../../internal/dts_to_esc/mutability.go), and zero
`throws` clauses, zero parameter `mut`, and zero `&` borrows.

**Where the application runs.** Each determination is applied to the
converted declarations before `ApplyOverlay` folds the overlay in. The
digest §6.4 records for an overlay `replace` is taken over the printed
converted member, so a determination applied at that point lands inside
it. A `throws` set that widens or a parameter that becomes `mut` moves
the digest, and the `replace` standing in for that member fails until a
contributor re-records it.

Applying a determination after the overlay would annotate the overlay's
own member instead. The digest would have been taken from an
un-annotated form, so the fact would reach no check for exactly the
members an overlay forks. Receiver mutability already runs in the right
place: `ConvertBuckets` decides it and `ApplyOverlay` follows, so a
reclassified receiver already moves the digest of a member an overlay
replaces.

**Known fusion gaps.** A determination can only land on a fused
class, since an `interface` member has no receiver to annotate.
Two shapes do not fuse today, measured over the pinned lib set
with the Web Worker and Windows Script Host libs excluded.
`Array` was a third: its `ArrayConstructor` returns `T[]`, which
is `Array<T>` in shorthand rather than a type reference, and
`ctorReturnNames` now reads both forms
([#1350](https://github.com/escalier-lang/escalier/issues/1350)).

- **625 `interface Foo` + `declare var Foo: { prototype: Foo, new (…): Foo }`
  pairs**, of which 472 land in `web:dom`. `detectTrios` requires a
  named `FooConstructor` interface, and the DOM writes the
  constructor as an inline object literal on the `var`. Deferred:
  `web:*` fusion is folded into the web-specs workstream that
  replaces `lib.dom.d.ts` with WebIDL as the upstream source, so
  `web:*` is generated now as a check on the converter's ability to
  read `lib.dom.d.ts` and not yet as an annotated surface.
  [#1351](https://github.com/escalier-lang/escalier/issues/1351).
- **`Symbol` and `BigInt`**, whose constructor interfaces carry no
  construct signature at all, because the specification forbids
  `new` on them. [#1309](https://github.com/escalier-lang/escalier/issues/1309).

**Other conversion gaps.** A type predicate return, `arg is T`,
converts to `T` rather than to `boolean`, so `Array.isArray` emits
as `isArray(arg: any) -> mut Array<any>` and claims to return the
thing it tests. Escalier has no narrowing surface, so `boolean` is
the honest conversion. Four predicate returns exist across the
pinned lib set. [#1349](https://github.com/escalier-lang/escalier/issues/1349).

---

## §7. Stdlib bootstrap

**Goal.** Generate the `.esc` tree, review it, and commit it
together with the inputs that produce it. The tree is the source of
truth for every consumer that reads a `std:*` or `web:*` package;
the §6.4 inputs are the source of truth for the tree. Tracked as
[#1232](https://github.com/escalier-lang/escalier/issues/1232).

Work items 1 and 5 are done: the tree is committed at 49 packages
and 40,312 lines, byte-identical on a re-run. Item 2's mechanical
half is clean — every exported value-level declaration carries an
`@js` decorator and no unexported declaration leaks — and its
judgment half is open. Item 3 needs no overlay entry after all: the
converter emits `Awaited` faithfully from `lib.es5.d.ts`, so only
verification is left. Nothing type-checks the tree yet, per
[#1402](https://github.com/escalier-lang/escalier/issues/1402) and
[#1403](https://github.com/escalier-lang/escalier/issues/1403).

Depends on §6 PR E. Before that lands this phase reads as
"hand-edit the generated files", which it no longer is.

**Work items.**

1. Run `dts_to_esc generate node_modules/typescript/lib internal/interop/data`
   over the full pinned lib set, producing
   `internal/interop/data/{std,web}/`.
2. **Human review of every file.** Review reads the generated
   output, but a fix lands in the layer that owns what is wrong,
   never in the output:
   - A determination the facts got wrong — receiver mutability, a
     parameter's, a return's borrow, a `throws` set — costs an
     entry in `internal/ecma262/curated.json` stating the
     reviewer's reason and evidence.
   - A shape the converter cannot express, or expresses wrongly,
     costs an overlay `replace`.
   - A declaration no upstream source has costs an overlay `add`.
     `Symbol.customMatcher` in `std:symbol` and the
     [FR8](requirements.md) symbol re-export aliases in their
     owning packages land here, since neither is part of any
     `lib.*.d.ts`. Each is written as
     `@js("Symbol.<name>") export declare val <name>Key: unique symbol`
     per §3, which an importer reaches as a member of the package
     binding.
   - A bug in a rewrite pass or the printer is fixed in the
     converter and re-run. A systematic wrong answer across many
     declarations is this case, not a pile of overlay entries.
   - Verify every exported value declaration carries an `@js`
     decorator naming a real runtime target, and that
     package-private helpers are unexported and carry none. A miss
     is a generator bug, since the decorator is derived from the
     partition. Over the pinned lib set all 1096 exported value
     declarations carry one today.
3. **`Awaited<T>` source-level definition** in `std:async`, an
   overlay `add` written as the recursive conditional type matching
   TypeScript's. The solver already reduces recursive conditionals
   and has a productivity check that stops non-terminating ones
   ([productivity.go](../../internal/solver/productivity.go),
   [typeops.go](../../internal/solver/typeops.go)), so the remaining
   risk is the definition itself. Verification waits on SimpleSub
   M9. On a concrete blocker, fall back to a solver-resident
   intrinsic and document the specific failure.
4. **FR5 finalization — non-class package exports as namespace
   members**
   ([#1406](https://github.com/escalier-lang/escalier/issues/1406))**.**
   §2's single-class shortcut binds the class itself when
   activated; FR5 also calls for other package exports to stay
   reachable as namespace members on the same binding, with static
   methods winning a name collision. §2 left this a TODO in
   [bindStdlibLocal](../../internal/checker/infer_stdlib_import.go)
   because the §2-era stubs have a single export each. This phase
   produces the first package pairing a class with non-class
   exports, so implement the merge once in the solver's stdlib
   binding path when M7.5 ports it, with a unit test pinning the
   static-method-wins tiebreaker. Do not port the old checker's
   TODO forward.
5. Commit the generated tree together with **every input changed
   to produce it** in one change: the overlay, the
   `internal/ecma262/curated.json` entries step 2 added, the
   pinned TypeScript version, and any converter fix. A checkout of
   that commit has to regenerate the committed tree byte for byte,
   which it cannot do if an input stayed behind. After this point
   an ongoing edit goes to one of those inputs and never to a
   generated file. The §6.6 CI job is what keeps that true, and it
   is also what catches an input left uncommitted.
6. **§3.5 codegen fixtures deferred from §3**
   ([#1407](https://github.com/escalier-lang/escalier/issues/1407)),
   now that `std:number` and `std:iterator` exist as committed
   packages: hoisted global `parseInt`, the `Symbol.iterator`
   re-export, and package-private invisibility.

**Decision made.** The readonly-twin gap from §6 PR A: neither
route is taken, and the twin names leave the output entirely. The
twins existed to say whether a method mutates its receiver, and
the ECMA-262 derived facts answer that directly, with zero
disagreements between the two over the pinned lib set. Dropping
the name costs nothing, because Escalier already spells the
immutable view `Array<T>` and the mutable one `mut Array<T>`.
`ReadonlyArray<T> = Array<T>` was a second spelling for a type
that already had one, and TS `ReadonlyArray<T>` now converts
straight to `Array<T>` at every reference site.
[#1408](https://github.com/escalier-lang/escalier/issues/1408)
records the decision.

**Gate.** Humans review the committed files; every emitted file
parses and round-trips through the printer per §1; regenerating
leaves the tree byte-identical, which is what proves no generated
file was edited in place; `go test ./...` passes. This phase changes
no checker behavior in either checker — it lands the files and the
inputs that produce them and nothing more. The solver does not
type-check them until SimpleSub M7.5 ingests `std:*` / `web:*`, so
§7 can land well ahead of its consumer. Two items wait on later
milestones: `Awaited<T>` verification in step 3 needs M9, and step
6's package-private-invisibility fixture asserts a resolution rule,
so it holds on the solver only once §9.3 re-homes the loader rules.

---

## §8. Internal fixture migration

**Goal.** Give every fixture and test explicit `import "std:*"`
statements, so the corpus expresses where each stdlib name comes
from instead of relying on an ambient surface that the solver does
not have.

**Why it is required.** The solver reaches a stdlib type only
through an import or through its handle on the fixed well-known
protocol set, per "Target checker" above. A fixture that writes
`Math.max(a, b)` with no import is an unbound-name error there, no
matter what else has landed. This phase is what has to complete
before the SimpleSub M12 flip deletes `internal/checker`: until
then an unmigrated fixture still passes on the old checker and its
breakage stays invisible. So this phase is what lets SimpleSub
M8's second fixture harness run the `fixtures/` tree at all — it
is a precondition for the solver's real-package regression net,
not a step in a prelude cut-over.

**Prerequisites.** §7, because imports resolve against the
committed `.esc` files. §4, because a fixture that touches DOM
needs the single-`web:dom` package and cross-package type
references. SimpleSub M7.5, because the migrated fixtures do not
type-check on the solver until it ingests those files. §2's
resolver is in place transitively via §7, and the rewriting itself
can start as soon as §7 and §4 are in.

Update every fixture under [fixtures/](../../fixtures/) that
relies on previously-ambient symbols (`Math`, `JSON`, `console`,
`Promise`, `Error`, `Array.from`, `parseInt`, …) to use
`import "std:*"` statements. Do the same for the inline-source
tests under [internal/checker/tests/](../../internal/checker/tests/)
for as long as that tree exists. Tests under
[internal/solver/](../../internal/solver/) need no migration pass:
they are authored against intended semantics, so any that names a
stdlib type writes the import when it is written. The old checker resolves those names
ambiently for as long as it exists, and an explicit import
resolves through §2's resolver on the same tree, so each rewritten
file type-checks under both checkers and the migration can proceed
file by file.

The auto-import quick-fix from §10.3 is the migration aid when
it is available; otherwise migration is by hand. Ordering between
this phase and §10.3 is not strict — fixture migration can proceed
without the quick-fix, but having the quick-fix first lets the
fixture rewrite exercise the same tooling external users will
rely on.

**Gate.** `go test ./...` passes with every fixture using
explicit `import "std:*"` statements; no fixture relies on
ambient resolution — except for the third-party carve-out below.
Once M7.5 and M8 are both in, the same corpus runs green on the
solver harness, which is the real proof that nothing ambient is
left.

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
for the authoritative statement. These fixtures keep working on the
old checker until it is deleted; what they cannot do is run on the
solver, which resolves nothing ambiently. The mechanics required
here:

- **Skip helper.** Add a single `testutil.SkipUntilFR7(t, reason
  string)` helper (concrete package path TBD; likely
  [internal/testutil/](../../internal/testutil/) or a new
  `internal/testskip/`). It is the **only** sanctioned skip call
  site for this gap — every affected test calls it, no scattered
  `t.Skip(...)` strings. Re-enabling is one grep + one deletion.
- **Audit pass.** Before §9 lands, walk every fixture and test
  that loads a third-party `.d.ts` — the existing runtime interop
  pipeline's consumers, meaning `dts_to_esc.ConvertModule` call sites
  and fixtures with `.d.ts` payloads. Anything that resolves a
  stdlib name ambiently through the converted `.d.ts` gets
  `SkipUntilFR7`, since that is exactly what fails on the solver.
  Do not try to rewrite these to `import "std:*"`; the right
  migration path for them lives in third-party Phase 1.
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
affected tests — but it is strictly cheaper than teaching the
solver an ambient resolution path that exists solely for the
runtime interop pipeline. The skip approach also keeps the solver
harness readable: any remaining ambient-resolution failure in CI is
a genuine §8 miss, not third-party fallout.

---

## §9. Per-file shape loading in `internal/solver` (FR11)

**Goal.** Add FR11's lazy per-file shape loader to the solver, so
a file gets the method surface of the literals and language
features it uses — `"abc".toUpperCase()`, `xs.map(f)`, `await p` —
without naming the owning package in an import. Explicit
`import "std:*"` ingestion is SimpleSub M7.5's job; this phase
adds the trigger-driven load on top of it.

**No cut-over.** The solver never walked `lib.*.d.ts` and has no
ambient global surface, so there is no resolution path to swap and
nothing to delete here. FR11's "the prelude stops walking
`node_modules/typescript/lib/*.d.ts`" clause is satisfied by
construction; the legacy machinery it names is deleted with
`internal/checker/` at the SimpleSub M12 flip. What remains for
this phase is the trigger map, the loader-rule re-homing, and the
audit in §9.3.

**Prerequisites.** SimpleSub M7.5, which makes the solver ingest
`std:*` / `web:*` into `soltype`, and §8, which gives every
fixture explicit imports. §8 matters for observability rather than
correctness: a fixture still expecting a bare `Math` fails on the
solver with or without the trigger map, and that failure masks
trigger-map bugs.

Note: fixtures that load **third-party `.d.ts`** content are
intentionally not migrated in §8 — they are marked
`SkipUntilFR7` per §8.1 and stay skipped through this phase. Do
not un-skip or migrate them here; that work belongs to third-party
FR7. See §8.1 for the helper and CI guard.

### 9.1 Lazy shape-loader

Touch points:
[internal/solver/prelude.go](../../internal/solver/prelude.go) and
M7.5's stdlib ingestion path.

For each file F being checked, the solver inspects F's syntax and
shape-loads only the needed `std:*` packages. Shape-loaded
declarations are reachable to the inference walk but bind no names
into F's scope, so `"abc".toUpperCase()` resolves while a bare
`String` in F stays an unbound name. Trigger map per FR11:

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

**Open item: re-derive the `std:error` row.** That row triggers on
a `std:error` class *name* appearing in a `try` / `catch` /
`throw` / `throws` position, which presumes the name resolves
without an import. It does not on the solver: writing `TypeError`
requires importing `std:error`, which ingests the package anyway,
so the trigger is redundant for the written-name case. The
remaining question is whether an *unwritten* error type needs the
package — and the solver binds a caught value as `unknown` rather
than a union of the block's known throws
([#1181](https://github.com/escalier-lang/escalier/pull/1181)), so
a bare `try`/`catch` may need nothing from `std:error` at all.
Settle this when the phase lands: either drop the row or restate
its trigger in terms of a concrete type the walk needs and cannot
otherwise reach. The other rows are unaffected — they fire on
literal and language-feature syntax that names nothing.

**Overlap with M7.5's well-known-type handles.** M7.5 gives the
solver a direct handle on the fixed protocol set — `Promise` for
`await`, `Iterable` for `for (x in xs)`, `Generator` for `yield` —
so those desugaring rules fire in a file that imports nothing. The
trigger map covers the wider case: member access on a value whose
type the file never names, such as `"abc".toUpperCase()` or
`xs.map(f)`. The two overlap in an `async fn` body that also calls
`p.then(…)`, where the handle resolves the rule and the trigger map
loads `std:async`. Both are idempotent, so the overlap costs
nothing beyond one memoized parse.

### 9.2 Explicit import loading

Reuses §2's resolver path and M7.5's ingestion. The shape-load and
named-import paths share the same parsed declarations and the same
inferred `soltype` structures; they differ only in whether
identifiers land in F's scope. Multiple files in a compilation
share **one parsed copy** of each `std:*` package; the memoization
is keyed by package URI and shared between the two paths, so a file
that imports `std:array` and a file that only writes an array
literal reach the same ingested package. No bootstrap cycle: each
`std:*` package contains only declarations, so nothing in one needs
a prelude of its own.

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

### 9.3 Legacy-path audit and loader-rule re-homing

The legacy builtin machinery lives in `internal/checker/` and is
deleted with that tree at the SimpleSub M12 flip, not here. What
this phase owes is an audit that no entry below grew a solver-side
twin during the migration. Each one is either absent from
`internal/solver/` already or has a named source-level
replacement:

- `loadGlobalDefinitions` ([prelude.go](../../internal/checker/prelude.go))
  — replaced by import ingestion (M7.5) plus §9.1's trigger map
- `populateSelfParams` — replaced by the `self` receivers the
  converter emits into the `.esc` files (§6.1)
- `UpdateMethodMutability` — replaced by the converter's receiver
  classification plus the hand-edited mutability refinements
  committed in §7. It is the only caller that applies
  `nonMutatingOverrides`, so the table retires with it. Until then an
  entry cannot be dropped on the strength of a converter-side answer:
  a receiver `Classify` marks non-mutating does not reach the
  `.d.ts`-loaded lib types, so the entry is what carries the claim
- `mergeReadonlyVariant` — replaced by the converter's
  readonly-twin fusion (§6.1)
- `BuildBuiltinStore`
- `internal/interop/data/builtins/` (if present) — no override
  fragments for builtins remain
- the Escalier-specific `SymbolConstructor.customMatcher`
  injection at
  [prelude.go:804–836](../../internal/checker/prelude.go#L804-L836)
  — replaced by the hand-authored declaration in `std:symbol`
  (§7 step 2)

**Loader rules re-homed.** §3.4's rules are enforced today by
`validateJsDecorators` in
[js_decorator.go](../../internal/checker/js_decorator.go), a
`*Checker` method. Rules 1 through 3 read only the parsed
pseudo-package module and its decorators, so they move to the
pseudo-package load path alongside M7.5's ingestion, or to a
checker-agnostic loader package both callers share. Rule 4, the
`@js` argument check, is the one that needs a type-system:
`knownJSGlobals` in
[js_globals.go](../../internal/checker/js_globals.go) reads
`GlobalScope.Namespace.Values`, which exists only in the old
checker. Re-home it as a CI-only test that freshly parses the
pinned `lib.*.d.ts` through
[internal/dts_parser/](../../internal/dts_parser/) and validates
every `@js("...")` argument across the committed stdlib. That also
drops the compiler's startup dependency on a parsed TS lib.

Carry §3.4's allow-list across with the rule. `Symbol.customMatcher`
is Escalier's own and appears in no `lib.*.d.ts`, so §7 step 2
hand-authors it and the lib lookup will not find it. The allow-list
names it and any later Escalier-only target explicitly; every other
`@js` argument must still resolve to a real lib member, so the
check keeps its teeth.

**Rule 5: `@js` decl shape matches lib target.** Add to the same
CI-only test. Locate the lib member each `@js("...")` names and
assert that a `readonly` or getter-only member has an Escalier
`val` or `get` declaration and never `var`, a setter-only member
has `set`, and a method has `fn`. An allow-listed target has no
lib member to compare against, so rule 5 skips it and rule 4's
allow-list is the only thing vouching for it. This catches a stub
that silently makes a readonly thing writable:
`@js("Math.PI") export declare var PI: number` compiles and lowers
to a `Math.PI = …` that TypeErrors at runtime. Rules 4 and 5 share
the lib parse, so they land together.

**No `override declare` for builtins.** That syntax stays
reserved for the third-party workstream's override mechanism;
no builtin pseudo-package uses it.

**Gate.** A fixture with no `import` calls a string method, an
array method, and `await`s a promise, and type-checks on the
solver through the trigger map alone; a fixture that names `String`
or `Array` without importing it still errors as unbound, proving
shape-loading binds nothing. The audit list is discharged — every
entry is absent from `internal/solver/` or has a named replacement
— and the CI-only rules 4 + 5 test passes over the committed
stdlib.

---

## §10. Intrinsics; adaptive rendering; LSP support (FR13, FR15, FR16)

**Goal.** Ship the diagnostic + LSP tooling users need under the
new model, and confirm the intrinsic handlers stay solver-resident.

This phase runs against the solver's own surfaces: the `soltype`
printer for rendering, the solver's diagnostics for suggestions,
and the solver-backed LSP that SimpleSub M11 stands up. Its
rendering work is adjacent to SimpleSub M11.5, the
diagnostics-quality capstone — M11.5 owns provenance chains,
related spans, and cascade suppression across every diagnostic,
while §10.2 below owns one narrow question, which surface form a
stdlib type takes in a given file. Land them in either order and
keep the split at that boundary.

### 10.1 Intrinsic types (FR13)

Confirm that `Uppercase`, `Lowercase`, `Capitalize`,
`Uncapitalize`, `NoInfer` remain solver-resident handlers — no
source file under `internal/interop/data/`. The four string-case
utilities are pure `Type → Type` resolvers; `NoInfer` is an
inference-machinery hook. On the solver side the string four are
already representational, as `soltype.StringIntrinsicType` with a
`StringIntrinsicKind`, so what this phase confirms is that the
partition emits no `.esc` file for them and that no bootstrap
output tries to define one. Tracked in escalier-lang/escalier#631.

`Awaited<T>` source-level definition lives in `std:async` per §7
step 3 — the recursive conditional type matching TS's definition,
tracked in escalier-lang/escalier#630. It is verified once SimpleSub
M9 turns on type-level operators. Fall back to a solver-resident
intrinsic only on a documented blocker: recursive conditionals do
not reduce correctly, pathological performance, or a soundness
issue. **The fallback decision must be committed with a documented
description of the specific failure that motivated it.**
Concretely, a Go doc comment on the solver-resident `Awaited`
handler citing the failing test under `internal/solver/` or the
failing fixture under `fixtures/`. Not duplicated in this plan.

The bootstrap converter strips `intrinsic`-typed declarations
encountered in TS source (FR13) — no `.esc` output is produced
for them, which means no `export` and no `@js` either. The
parser does **not** learn the `intrinsic` keyword. Verify the
parser still rejects `intrinsic` after this workstream lands
(regression guard).

### 10.2 Adaptive diagnostic rendering (FR15)

Add a location-aware rendering mode beside the existing
`soltype` printers. Today [print.go](../../internal/soltype/print.go) exposes three:
`Print` renders the user-facing surface form with the namespace
stripped, `PrintQualified` renders a collision-free identity key
that is not a surface form, and `PrintElided` truncates deep
subtrees. The solver's `describe` renders raw mid-constrain types
alongside them. FR15 needs a fourth mode — call it
`PrintForLocation(t, scope)` — that picks the shortest unambiguous
form for `t` given the bindings in scope at the diagnostic's
source location:

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
scope — say the file has both `import "std:array"` and
`import "std:array?nested"` — the renderer picks the shortest;
ties break in the order 1 → 2 → 3 above. The rendering is
per-diagnostic, not per-compilation, so the same type can render
differently in two files.

Named imports from pseudo-packages are out of scope per Non-goals,
so the renderer has no "bare name" case to handle.

Touch points: the solver's error structs
([errors.go](../../internal/solver/errors.go)) carry typed
`soltype` references and build their text in `Message()` at report
time, so the change is to thread the file's scope into that step
rather than to rewrite each construction site. Only the sites that
render a *coalesced* surface form are in scope; `describe`'s raw
mid-constrain rendering (`t0`, `function`) names no stdlib type and
stays as it is. `Print` remains the default wherever there is no
scope to consult, such as snapshot tests and LSP hovers before M11
threads scope through, and `PrintQualified` remains the identity
key.

### 10.2a Diagnostic-assisted migration

When a stdlib name is referenced without an import, the solver's
**unbound-name diagnostic** includes a suggestion ("did you mean to
`import "std:async"`?") whenever the unbound name matches a known
pseudo-package export. This is the common case rather than a
migration artifact — the solver resolves nothing ambiently, so
every forgotten import lands here.
The suggestion list is derived mechanically from the LSP
name-index (§10.3); the diagnostic path reuses the same index.
This is the **fallback for command-line use** — users in a
supported editor get the FR16 quick-fix instead. Suggestion
text routes through the error-message taxonomy entries; spans
point at the bare reference, not the surrounding statement.

### 10.3 Auto-import quick-fix (FR16)

LSP first-class, so it lands on the solver-backed LSP from
SimpleSub M11. Both this quick-fix and §10.2a's suggestion hang off
an unbound-name diagnostic for a stdlib name, and only the solver
emits one — the old checker resolves `Math` ambiently and reports
nothing, so there is no diagnostic to attach a suggestion to and
no old-checker integration worth defining. The name index below is
the one piece that can land early: it walks `.esc` files and reads
no checker state, so building it ahead of M11 shortens the later
PR without shipping anything user-visible.

Quick-fix on an unbound-name diagnostic that:

1. Adds the appropriate namespace import statement
   (`import "std:async"`, `import "std:math"`, …).
2. **Single-class shortcut packages:** leaves the bare reference
   unchanged. `Array.isArray` and `Date.now` already match the
   imported binding name. The eligible packages are the ones §6.1
   enumerates, and only those.
3. **Other packages:** rewrites the bare reference to qualify
   through the resulting namespace. `sin(x)` becomes
   `math.sin(x)`; `Promise.all([...])` becomes
   `async.Promise.all([...])`, since `Promise` lives in the
   bundled `std:async`; `Error(...)` becomes `error.Error(...)`,
   since `std:error` bundles `Error` with `TypeError`,
   `RangeError`, and the rest and so has no shortcut either. A
   bundled package never leaves its names bare.

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

Touch points: [cmd/lsp-server/](../../cmd/lsp-server/) and
wherever the LSP's request handlers live, on the post-M11
solver-backed path.

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
already takes a filename; the resolver passes the resolved path
through unchanged, and the solver's diagnostics carry those spans
like any other.

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
- Shape loading (§9) — a fixture with no `import` calls a string
  method, an array method, and `await`s a promise, and type-checks
  on the solver through the trigger map alone; a fixture naming
  `String` or `Array` without importing it still errors as
  unbound. Fixtures migrated to `import "std:*"` in §8 keep
  type-checking on both checkers while both exist. No parity check
  against the old checker's ambient path: it is deleted with
  `internal/checker/` at the SimpleSub M12 flip, and SimpleSub M8's
  differential harness is where old-vs-new divergence is triaged.
- Adaptive diagnostic rendering (§10.2), auto-import quick-fix
  (§10.3), named-import rejection (§2 parser/resolver).
- Snapshot tests on converter output via `go-snaps`; CI
  regenerates the tree and fails on a dirty diff, which is what
  catches an upstream TS change nobody regenerated (§6.6).

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
- **Initial bootstrap quality** — mitigated by the human review
  pass at §7 and by the regenerate-and-diff job at §6.6. Note the
  sequencing
  risk this creates: §7's files are committed before SimpleSub
  M7.5 type-checks them, so review and the §1 round-trip gate are
  the only signal until then. Expect a correction pass when M7.5
  first ingests the tree.
- **Cross-package augmentation mechanism** — investigated by
  §4.1 spike; the conclusion was to deferred FR7 and ship
  closed registries (§4.2) for MVP. Risk re-emerges only when
  custom-element support is added (§4.5).
- **Polyfill story is separate.** This workstream assumes
  polyfill insertion at lowering is tractable (per FR12). No
  polyfill work happens here.

### Backwards-compatibility

**No released-user compatibility obligation — pre-1.0.** Escalier
has no released compiler yet, so there are no external users to
migrate and no deprecation cycle to manage.

There is still a behavior change, and it is worth naming: source
that reaches `Math`, `Array`, `console`, and the rest without an
import type-checks today on `internal/checker` and stops
type-checking the moment that tree is deleted. The solver resolves
no name ambiently. Nothing in the builtins phases themselves flips
that — the solver has no ambient surface to remove, and §8 adds
imports both checkers accept — but the SimpleSub M12 flip does.
**§8 must therefore complete before M12**, not merely before §9:
an unmigrated fixture survives §9 and breaks at the flip.

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
| FR1   | No ambient set; two-mode loading           | §6.1 (partition), §9 (lazy shape-load; the no-ambient half is the solver's starting posture, so §9.3 is an audit rather than a deletion), Drops subsection in §6.1 (`globalThis`/`eval`/`EvalError`) |
| FR2   | Pseudo-package layout                      | §2.2 (resolver mapping + underscore convention), §2.2a (stdlib data directory discovery), §6.1 (full enumeration), §6.3 (output layout + distribution) |
| FR3   | URI-scheme import grammar; runtime erasure | §2.1 (parser), §2.2 (resolver), §3 (decorator-based lowering + import erasure)                                |
| FR4   | Binding-shape flags                        | §2.3 (both shapes, mutual exclusion, extensibility, URI-keyed bookkeeping) |
| FR5   | Single-class shortcut                      | §2.4; eligibility list in §6.1                                                                                   |
| FR6   | Inter-package imports                      | §4.3 (cycles permitted within pseudo-package layer)                                                              |
| FR7   | DOM packaging; cross-package type references; open augmentation deferred | §4.2 (single `web:dom` package + standalone web siblings; closed registries; `createElementNS` stays one overloaded method on `Document`), §4.2b (qualified cross-package type references), §4.5 (deferred augmentation work scoped for the future custom-elements workstream), §4.6 (method-elem overload resolution on class/interface declarations — open prerequisite for §7 so converted DOM methods dispatch correctly). Spike (§4.1) showed achieving the old per-file-activation design needs two new checker subsystems; MVP sidesteps by collapsing the DOM tree into one package. |
| FR8   | Well-known symbol re-exports               | §7 step 2 (hand-authored re-export aliases with `@js("Symbol.<name>")`), §3 (decorator semantics carry the alias) |
| FR9   | Augmentation activation semantics          | N/A for MVP — single-`web:dom` partition (§4.2) requires no activation semantics. Original spec preserved in [requirements.md appendix](requirements.md#appendix-deferred-fr9-spec) for the deferred custom-elements work. |
| FR10  | Bootstrap converter                        | §5 (MVP, trio idiom, namespace flattening), §5.0 (JSDoc precursor), §6.1 (partition), §6.2 (routing), §6.4 (generation model), §6.5 (`throws`), §6.6 (TS-bump workflow), §6.8 (fact application) |
| FR11  | Prelude changes; lazy shape loading        | §9.1 (trigger map), §9.2 (shared parsed copies), §9.2a (cross-package verification), §9.3 (legacy-path audit + loader-rule re-homing) |
| FR12  | Always-current API; polyfills at lowering  | Acknowledged as out-of-scope dependency in cross-cutting; type checker sees modern surface unconditionally       |
| FR13  | Intrinsic types solver-resident            | §10.1 (handlers stay in `internal/solver`, `Awaited<T>` source-first with documented-fallback requirement, parser rejects `intrinsic`) |
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
