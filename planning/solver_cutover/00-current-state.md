# 00 — Current state

Measured at `6c620c2`. Every number below is reproducible; the method is in
§"How these numbers were taken".

## What has landed

The solver is further along than a reader of [01-milestones.md](../simple_sub/01-milestones.md)
would guess, because most milestone sections carry no status line.

| Milestone | State | Evidence |
| --- | --- | --- |
| M1–M2.5 | landed | status lines in `01-milestones.md` |
| M3 | landed | `poly.go`, `overload.go`, `probe.go`, `simplify.go` |
| M4, M4.5 | landed | status line; `script.go` is the M4.5 entry point |
| M5 | landed | `infer_class.go`, `classes.go`, `inherit.go`, `super.go` |
| M6 | PR1–PR6, PR2.5, PR2.7, PR8 landed; PR7 open | `m6-implementation-plan.md` status block |
| M6.5 | landed | `lifetime_bounds.go`, `lifetime_coalesce.go` |
| M7 | landed | `aliases.go` |
| M7.5 | landed as machinery; the data is the gap | `stdlib_import.go`, `stdlib_closure.go`, `package_load.go` |
| M9 | landed except PR9f | `m9-implementation-plan.md` dependency graph; `typeops.go`, `generator.go` |
| M8, M10, M11, M11.5, M12 | not started | no `soltype` or `solver` reference in `internal/compiler/`, `internal/codegen/`, or `cmd/` |

Size, non-test Go:

| Package | Lines |
| --- | --- |
| `internal/solver` | 45,369 |
| `internal/checker` | 28,374 |
| `internal/type_system` | 7,946 |

Tests: 139 files and 61,220 lines under `internal/solver`, against 43 files and
34,649 lines under `internal/checker/tests`.

## Library ingestion, measured per package

The committed tree under `internal/interop/data/` holds 48 pseudo-packages plus
`std:prelude`. Loading one package loads its import closure, so the count a
package reports includes everything it reaches. The prelude loads on every run,
so its own 2 diagnostics are the floor.

| Package | Diagnostics |
| --- | --- |
| `std:prelude` alone | 2 |
| `std:async`, `std:boolean`, `std:console`, `std:disposable`, `std:error`, `std:iterator`, `std:json`, `std:math`, `std:regexp`, `std:url`, `std:weak_ref` | 2 |
| `std:map`, `std:set` | 4 |
| `std:decorators`, `std:function`, `std:object`, `std:reflect` | 6 |
| `std:proxy` | 7 |
| `std:date`, `std:intl`, `std:number` | 17 |
| `std:string` | 18 |
| `std:bigint` | 24 |
| `std:typed_arrays` | 215 |
| `web:core` and the ten standalone `web:*` siblings | 224–279 |
| `web:dom` and the eight packages that reach it | 2,707 |
| every package at once | 2,741 |

Eleven `std:*` packages are already clean, in the sense that they add nothing to
the prelude's floor.

## What the residual diagnostics actually are

Across the whole tree, 2,397 of 2,741 are `cannot find type` and they concentrate
on five names:

| Name | Occurrences |
| --- | --- |
| `EventListenerOptions` | 457 |
| `AddEventListenerOptions` | 457 |
| `EventListenerOrEventListenerObject` | 456 |
| `Event` | 334 |
| `Window` | 125 |

All five are DOM types, so the bulk of the grind sits behind `web:dom`. The
`std:*` half reduces to four root causes:

1. **`Unsupported: BigintTypeAnn`, 137 occurrences.** `internal/solver/type_ann.go`
   has no arm for `*ast.BigintTypeAnn`. `internal/checker/infer_type_ann.go:99`
   has one. Nearly all of these land in `std:typed_arrays` and `std:bigint`.
2. **Unqualified sibling references in `std:typed_arrays`, 82 occurrences.**
   `Int32Array`, `BigInt64Array`, `Uint8Array` and the rest fail to resolve bare
   while `typed_arrays.Uint8Array` resolves, so the generated file names a
   same-package sibling in a position the qualification pass does not rewrite.
3. **`std:prelude`'s two constraint failures.** These reach every run.
   - `cannot constrain if keyof T : K { never } else { keyof T } <: keyof T`
     comes from `export declare type Omit<T, K: keyof any> = Pick<T, Exclude<keyof T, K>>`
     at `internal/interop/data/std/prelude.esc:925`. `Exclude` leaves a residual
     conditional, and the residual is then checked against `Pick`'s `K: keyof T`
     bound instead of being deferred.
   - `cannot constrain tuple[T] <: number` comes from
     `static race<T: Array<unknown> | []>(values: T) -> Promise<Awaited<T[number]>>`
     at `internal/interop/data/std/prelude.esc:599`, repeated for `any` at 645.
     A numeric indexed access against a type parameter whose bound includes the
     empty tuple constrains the tuple itself against `number`.
4. **A long tail under 70 occurrences total** — `owned-mutable field annotation
   is not allowed`, `Unsupported: typeof of a name that is not a readable value`,
   two overload-distinguishability reports, and a handful of inherited-member
   redeclarations.

None of the four is a pseudo-package problem. Causes 1, 3, and 4 are confirmed
solver gaps. Cause 2 is unresolved between the generator and the solver, and
stays that way until a minimal reproduction says whether the bare name is
emitted wrong by `internal/dts_to_esc/ref_rewrite.go` or looked up wrong by
`internal/solver/stdlib_group_load.go`.

## What the fixture tree actually needs

Two fixtures carry an `import` statement, `fixtures/stdlib_import_local` for
`std:math` and `fixtures/stdlib_import_class_via_namespace` for `std:date`. Both
are marked `DISABLED`. No fixture imports from `node_modules`, and none uses
JSX.

No fixture writes an `import` for a `web:*` package either, but that is not the
same as needing none, because the old checker supplies the DOM ambiently. The
paragraph after the table says which fixture that catches.

Fixtures naming a stdlib type without importing it, counted by name:

| Name | Fixtures | Reachable today |
| --- | --- | --- |
| `Array` | 9 | yes, `std:prelude` exports it |
| `Symbol` | 7 | yes |
| `Promise` | 1 | yes |
| `console` | 8 | no |
| `String` | 4 | no |
| `Date`, `Number` | 3 each | no |
| `Object` | 2 | no |
| `Math`, `JSON`, `Map`, `Set`, `RegExp` | 0 | — |

So 18 of 73 fixtures need an added `import "std:…"` line, and most of the rest
need nothing. `std:prelude` is ambient in the solver, which is what already
covers `Array`, `Promise`, and `Symbol`.

**One fixture needs a `web:*` package.** `fixtures/async_await` calls `fetch` at
four places and declares it nowhere. The old checker supplies it ambiently,
because `internal/checker/prelude.go:529` appends `lib.dom.d.ts` to the global
load. On the solver it comes only from `web:fetch`, which sits at 257
diagnostics because it reaches `web:core`. So the `std:` / `web:` split does not
fall exactly on the fixture tree, and P0 has to carry `web:core` and `web:fetch`
with it.

That ambient load is the wider compatibility story, and the next section
measures it. `document` and the element types stay lost after the flip, because
P1.5's ambient scope reaches only what P0 has cleared. See
[02-parked-work.md](02-parked-work.md)§"The three real regressions at the flip".

The old checker cannot resolve these imports against the committed tree, which
is why both stdlib fixtures are disabled. Adding imports to a fixture therefore
breaks the old-checker harness on that fixture. The consequence for sequencing
is in [01-cutover-plan.md](01-cutover-plan.md) P5: whatever fixture edits remain
after P1.5 ride with the flip rather than preceding it.

## Expression forms the solver rejects

Six expression forms report `UnsupportedNodeError`. Measured through
`InferModuleAgainstStdlib` against the committed tree:

| Form | Example | Report | Issue |
| --- | --- | --- | --- |
| binary operators | `1 + 2`, `1 < 2`, `true && false` | `Unsupported: BinaryExpr` | [#1652](https://github.com/escalier-lang/escalier/issues/1652) |
| unary operators | `-5`, `!true` | `Unsupported: UnaryExpr` | [#1653](https://github.com/escalier-lang/escalier/issues/1653) |
| template literals | `` `hi` ``, tagged | `Unsupported: TemplateLitExpr`, `TaggedTemplateLitExpr` | [#1654](https://github.com/escalier-lang/escalier/issues/1654) |
| typecast | `x : number` | `Unsupported: TypeCastExpr` | [#1655](https://github.com/escalier-lang/escalier/issues/1655) |
| `do` expressions | `do { 5 }` | `Unsupported: DoExpr` | [#1656](https://github.com/escalier-lang/escalier/issues/1656) |
| regex literals | `/ab+c/` | `Unsupported: RegexLit` | [#1657](https://github.com/escalier-lang/escalier/issues/1657) |

All six parse cleanly, so these are inference gaps rather than syntax the
language lacks. JSX is a seventh, covered separately under §"Gaps between the
solver's API and the compiler's needs".

Binary operators are the one that matters for sequencing. They appear in 59 of
the 73 directories under `fixtures/` — `return a + b` in `func_decl`, `r + g + b`
in `enum` — so until [#1652](https://github.com/escalier-lang/escalier/issues/1652)
lands, a solver run over the fixture tree reports almost all of it as failing and
every other gap is hidden behind that.

The operator schemes themselves are already seeded. `addOperatorBindings` in
`prelude.go:77` binds `+ - * /` over `number`, `< > <= >=` to `boolean`,
`== !=` over `unknown`, `&& ||` over `boolean`, `!`, and `++` over `string`.
Nothing looks them up. `infer.go:785` says so directly:

```go
// PR8 handles the ASSIGNMENT op only (`a = expr`); every other binary
// operator (+, ==, &&, ++, …) needs the operator-scheme walk over the prelude
// bindings, a separate unlanded PR, so it stays UnsupportedNodeError.
```

**How six forms went unowned.** `m2-implementation-plan.md:199` lists
`BinaryExpr` as in scope for M2 and calls the port near-mechanical at line 784.
`m3-implementation-plan.md:1160` then defers it, and no later milestone picked it
up. The other five appear in no milestone plan at all, because the M-series
tracks the type-system surface and treated the plain expression walk as M2 table
stakes. P1.7's gate is a test over every `isExpr` implementor, so the next form
cannot go unowned the same way.

**What the same sweep cleared.** Partial and unparseable source degrades to
diagnostics rather than panicking — `val x = foo.`, `val x = foo(`, an unclosed
brace, a missing right-hand side. That matters most for the LSP, which
re-checks on every keystroke. The solver also already has the constructor
initialization checks (`FieldNotInitializedError`, `ReadBeforeInitError`,
`MethodCallBeforeInitError`), match exhaustiveness in `ucs_coverage.go`, and
decorator parsing. `ArraySpreadExpr` is handled by the solver and not by the old
checker.

## How a builtin is reached, on each checker

The old checker has no namespaces for builtins. `loadGlobalDefinitions`
(`internal/checker/prelude.go:488`) merges every `lib.es*.d.ts` plus
`lib.dom.d.ts` into one flat global scope, so `Math`, `console`, `JSON`,
`Element` are all bare names with no import.

The solver has exactly two unprefixed mechanisms, and neither is a general
global surface:

| Mechanism | Names | Needs an import |
| --- | --- | --- |
| `std:prelude`, via `bindPreludeExports` in `prelude.go` | 44 | no, it is ambient |
| `web:core`, via `bindCoreExports` in `imports.go:105` | 24 | yes, `import "web:core"` |

Everything else is namespace-qualified. The tree exports 278 top-level names
under `std/` and 1,823 under `web/`, so roughly 2,030 names are reachable only
as `<package>.<name>`.

**The spellings change, not just the import list.** The partition dissolved the
TypeScript wrapper objects: `std:math` exports bare `fn clz32`, not a `Math`
object, so the package namespace became the prefix and lost its capital. Checked
against the committed tree:

| Written today | On the solver |
| --- | --- |
| `Math.PI` | `Unknown identifier: Math`; write `import "std:math"` then `math.PI` |
| `JSON.parse` | `Unknown identifier: JSON`; write `import "std:json"` then `json.parse` |
| `console.log` | `Unknown identifier: console`; `import "std:console"` then `console.log` gives `Namespace std:console has no member: log`, because the package namespace and the exported var share the name. `console.console.log` resolves. Tracked at [#1651](https://github.com/escalier-lang/escalier/issues/1651) |
| `Array<number>` | resolves, `std:prelude` exports it |

So the fixture migration is a rewrite of call sites rather than added import
lines, and the flip would otherwise carry a user-visible language change.
P1.5 is the phase that avoids that.

**The old spelling is recorded in the tree already.** Every value-carrying
export has an `@js` decorator naming its runtime path, and no type-only export
has one, because a type has no runtime path:

| Export kind | Count | Carries `@js` |
| --- | --- | --- |
| `class` | 691 | yes |
| `fn` | 210 | yes |
| `var` | 186 | yes |
| `val` | 9 | yes |
| `interface` | 701 | no |
| `type` | 335 | no |

`@js("Math.clz32")`, `@js("console")`, `@js("parseInt")`, `@js("NaN")`. That
makes the global spelling of every builtin derivable rather than hand-listed,
which is what P1.5 builds on.

## Integration surface

### Compiler

`internal/compiler/compiler.go` reaches the checker through **five**
`checker.NewChecker` sites across six exported entry points: `CheckLib`,
`CheckPackage`, `CheckBinScript`, `Compile`, `CompilePackage`, and
`CompileScript`. The `00-overview.md` boundary analysis says three; it has grown
since.

`type_system.Namespace` also threads through the public signatures of
`CheckBinScript`, `CompileScript`, and `collectUsedLibSymbols`. That is the
`lib/` to `bin/` seam: a script is checked against the namespace the library
module produced.

### Codegen

51 references to `type_system` or `InferredType()`, in four files:

| File | References | What they do |
| --- | --- | --- |
| `dts.go` | 17 | `.d.ts` emission; `buildTypeAnn(type_sys.Type)` walks a whole type |
| `builder.go` | 16 | JS emission |
| `self_type_utils.go` | 15 | rewrites `Self` to `this` for `.d.ts` |
| `js_lowering.go` | 3 | JS emission |

The JS half is smaller than the count suggests. Across `builder.go` and
`js_lowering.go` there are five `InferredType()` read sites, asking five
questions: is the callee's type a nominal object, does it carry a constructor
element, is this expression a function, does an optional-chaining target's union
include `null` or `undefined`, and is a member expression's object a namespace.
`js_lowering.go` also reads the AST's `BindingOwner` field, which M12 re-homes
anyway. Everything else in JS emission is driven by the AST and the dep graph,
both checker-agnostic.

`.d.ts` emission is the real cost. `BuildDefinitions(depGraph, libNS)` consumes
a `type_system.Namespace` and renders types through
`buildTypeAnn(type_sys.Type)`, roughly half of `dts.go`'s 1,350 lines.

### LSP

`cmd/lsp-server` holds 86 non-test references to `checker` or `type_system`, 76
of them in `completion.go`. `completion_test.go` holds 51 more.

## Gaps between the solver's API and the compiler's needs

1. **No lib-scope argument on `InferScript`.** `InferScript(script, source)`
   parents the script scope to the prelude. The compiler needs a script checked
   against the library module's scope, which is what `CheckBinScript` and
   `CompileScript` do with `libNS`.
2. **`ModuleResult` does not carry the dep graph.** Codegen takes one.
   `inferDepGraph` builds it internally from `dep_graph.BuildDepGraph(module)`,
   which is deterministic and checker-agnostic, so the caller can rebuild it. A
   field is cheaper than a second build.
3. **No third-party `.d.ts` ingestion.** `bindImport` sends a non-scheme URI to
   `loadPackage`, which asks the run's `ModuleSource`. No `ModuleSource` in the
   tree routes `internal/resolver` to `dts_parser` to `dts_to_esc.ConvertModule`.
   Nothing in `fixtures/` needs this. The coverage lives in
   `internal/checker/tests/import_load_test.go`, `package_registry_test.go`, and
   `jsx_test.go`, all of which P7 deletes.
4. **No JSX inference.** `internal/solver` contains no reference to `JSX` at
   all. The old checker has `infer_jsx.go` at 677 lines and `react_types.go` at
   194, with 3,126 lines of tests. `internal/codegen/jsx.go` emits it, so this
   is a shipped language feature with no solver implementation.
   `react_types.go` reaches `@types/react` through `internal/resolver`, so JSX
   sits downstream of gap 3. No fixture uses JSX, so the P2 harness will not
   surface this.
5. **No solver path at any compiler entry point.** Nothing under
   `internal/compiler/`, `internal/codegen/`, or `cmd/` names `solver` or
   `soltype`.

## How these numbers were taken

The per-package table came from a throwaway test in `internal/solver` that
globs `../interop/data/{std,web}/*.esc`, builds a one-line module importing each
URI in turn, runs `InferModuleAgainstStdlib(module, "../interop/data")`, and
counts the messages `errorMessagesOf` returns. A package group reports as one
message holding a nested count, so the counter adds the nested lines. P1 turns
this into the committed ledger test, so the table above becomes a checked-in
baseline rather than a one-off.

Coupling counts came from `grep -c 'type_system\.\|InferredType()'` per file and
`grep -rn 'checker.NewChecker'`. Fixture name counts came from
`grep -rlw '<name>' fixtures --include='*.esc'`.
