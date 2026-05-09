# Override Merge Semantics

This document specifies how an `override declare ...` block in an
`overrides/*.esc` file combines with the declarations parsed from a
TypeScript `.d.ts`. It refines the high-level rules in
[requirements.md](requirements.md) (section "Override file format")
with concrete answers to the edge cases that the resolver and the
type checker have to handle.

## Inputs

- The **original** type, built from the upstream `.d.ts` (or, for
  globals, from the TS standard-library `.d.ts` set bundled with
  TypeScript).
- An **override** type, parsed from `override declare ...` blocks
  found in any `overrides/*.esc` file.

The merge produces the **effective** type that the rest of the
compiler (checker, codegen, interop) operates on. The original type
is never mutated; the merged type is a fresh value.

## Top-level matching

An override block targets a specific scope:

| Form                                      | Targets                                    |
| ----------------------------------------- | ------------------------------------------ |
| `override declare module "name" { ... }`  | All exports of the named module            |
| `override declare global { ... }`         | The global / ambient scope (`Date`, etc.)  |
| `override declare class Foo { ... }`      | Sugar for `override declare global { class Foo { ... } }` |
| `override declare fn name(...) { ... }`   | A specific global function                 |
| `override declare interface Foo { ... }`  | Sugar; same as `class Foo` rule above      |

A single override file may contain any number of these blocks; a
single library's overrides may be split across files (per
requirements.md "Override file format").

## Class / interface body merge

Inside an `override declare class Foo { ... }` (or interface), the
override body is matched member-by-member against the original
declaration:

### Member-presence rules

| Original | Override | Effective |
| -------- | -------- | --------- |
| Present  | Present  | Override replaces original (subject to overload rules below) |
| Present  | Absent   | Original passes through unchanged |
| Absent   | Present  | **Error** by default. See "Targeting nonexistent members" |

**Targeting nonexistent members.** If an override declares a member
that does not exist on the original, the compiler emits a hard error.
Rationale: silently adding members is too easy a way to mask typos.
A future opt-in pragma (`@allow_new`) on the override class can
relax this to a warning when authors genuinely intend to extend.

### Overload collapsing

The original `.d.ts` may declare a method with multiple overloads
(common in lib.es5.d.ts). A **single** override entry for that method
name replaces **all** original overloads. The override becomes the
sole authoritative signature regardless of how many overloads the
original had.

Example (sketch):

```ts
// original.d.ts
interface Foo {
  bar(x: string): string;
  bar(x: number): number;
  bar(x: any): any;
}
```

```esc
// overrides/foo.esc
override declare interface Foo {
  bar(self, x: number | string) -> number | string
}
```

Effective `Foo.bar` after merge: just the override's signature.

### Override-defined overloads

If the override declares the same method **multiple times**, the
override entries form an overload set in the effective type — exactly
as if the user had written overloads in normal Escalier source:

```esc
override declare interface Foo {
  bar(self, x: string) -> string
  bar(self, x: number) -> number
}
```

Effective `Foo.bar` after merge: an overload set of the two override
signatures (the original's three overloads are discarded as a unit).

### Static vs instance

Static and instance members live in independent namespaces for the
purposes of merging. An override of `static foo` does not affect the
instance `foo` and vice versa.

### Getters / setters

`get foo` and `set foo` are independent overridable units:

- Overriding only `get foo` leaves the original `set foo` (if any)
  intact.
- Overriding only `set foo` leaves the original `get foo` (if any)
  intact.
- An override that declares a regular method named `foo` on a class
  whose original had a `get`/`set` pair is an error (the kinds
  conflict).

## Generics

The override must declare type parameters that match the original's
arity and bounds. The names may differ; positional matching is what
counts.

```ts
// original.d.ts
interface Box<T> { unwrap(): T; }
```

```esc
// overrides/box.esc — OK, U is positionally T
override declare interface Box<U> {
  unwrap(self) -> U
}
```

Mismatched arity is a hard error. Mismatched bounds (e.g. original
has `T extends string`, override has `T extends number`) is also a
hard error — overriding the bound itself isn't supported in this
phase.

## Module-level (`override declare module "x"`)

The body of `override declare module "x" { ... }` may contain any of:

- `override declare class C { ... }` — patches an exported class
- `override declare interface I { ... }` — patches an exported interface
- `override declare fn f(...)` — patches an exported function
- `override declare type T = ...` — patches an exported type alias
- `override declare val v: T` — patches an exported binding
- A blanket pragma at the top of the block (see below)

### Blanket pragma

For FP / immutability libraries (per requirements principle #5), a
blanket pragma at the top of an override module marks every exported
symbol non-mutating without listing them:

```esc
// overrides/ramda.esc
override declare module "ramda" {
  @all_pure
  // No member declarations needed; every export is pure.
}
```

`@all_pure` semantics: every exported function and method is
non-mutating in receiver and arguments. Does **not** apply
transitively to types returned by those functions (open question
from earlier review; settled here as: only the immediate call
site).

A module override may combine `@all_pure` with explicit member
declarations; the explicit declarations take precedence over the
blanket:

```esc
override declare module "fp-ts" {
  @all_pure

  // Exception: this one really does mutate.
  override declare fn unsafePush(arr: mut Array<number>, x: number) -> void
}
```

## Conflict resolution between override files

Multiple files may declare overrides for the same module:

- **Same member declared twice across files:** error. Authors should
  consolidate to a single file or split by class/function name.
- **Different members of the same class:** the override class body is
  the union of all member declarations across files. Each individual
  member must still appear in only one file.

Shipped overrides and user overrides cannot collide because they
load at different precedence tiers (resolution-order tiers 4 and 3
respectively); the user's wins. The diagnostic for the silent override
suggests the user verify the shipped override is wrong rather than
out-of-date.

## Order independence

The merge result must not depend on the order in which override files
are discovered. The implementation should:

1. Parse all override files into a flat list of override entries.
2. Group by `(module, qualified-name)`.
3. Detect duplicate-member conflicts (above) before merging.
4. Apply each merged override against the corresponding original.

## Diagnostics

Every merge action produces a structured event so diagnostics can
explain the effective type:

- "method `Foo.bar` overridden by `overrides/foo.esc:12` (collapsing
  3 original overloads)"
- "module `ramda` marked `@all_pure` by `overrides/fp.esc:1`"
- error: "override targets nonexistent member `Foo.baz`"

## Open follow-ups

These are deferred from Phase 0 but tracked here to keep the spec
honest:

- **Argument-mutation refinements** (per-parameter mutability) need a
  schema extension. Reserved for Phase 8.
- **`@allow_new` pragma** for extending classes legitimately. Not in
  the initial milestone.
- **`@all_pure` transitivity** through returned functions / curried
  forms. Out of scope; revisit if FP libraries hit false positives.
- **TS module augmentation in `.d.ts`**: the override file is the
  preferred mechanism, but TS authors may also declare `declare module
  "x" { ... }` ambient augmentations in their own source. The compiler
  reads those as part of the original type, before override merge.
