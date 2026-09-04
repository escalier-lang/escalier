# Overlay

Hand-written `.esc` fragments that `dts_to_esc generate` folds into the
generated tree under [../data/](../data/). They are one of the three
inputs a generated file's every fact comes from, alongside the pinned
`lib.*.d.ts` set and the ECMA-262 derived facts in
[../../ecma262/](../../ecma262/). See §6.4 of
[planning/builtins/implementation_plan.md](../../../planning/builtins/implementation_plan.md).

Files here are inputs. Files under `../data/` are build outputs and open
with a `Code generated` banner saying so. Correcting the generated tree
means editing an input and re-running, never editing the output.

## The operation is in the filename

An overlay file is ordinary `.esc`, and every declaration in it takes
that file's operation:

```
overlay/drop.esc               drops whole symbols that belong to no package
overlay/std/symbol.add.esc     adds to the package written to data/std/symbol.esc
overlay/std/array.replace.esc  replaces members of that package's declarations
overlay/std/date.drop.esc      drops declarations or members of that package
```

The name before the operation is the package's file basename, so
`std/symbol.add.esc` applies to `std:symbol` and `web/dom.replace.esc`
applies to `web:dom`.

A per-declaration marker would be the alternative, and the parser has
none to offer. Decorators are its only annotation and
[internal/parser/decl.go](../../parser/decl.go) rejects them on
`interface`, `type`, `enum`, and `namespace`, which is most of the
generated tree. The class-member parser reads no decorators at all.

## `add`

A declaration or member no upstream source has. A declaration the
package does not already have is appended whole; a declaration it has is
extended member by member.

```
// overlay/std/symbol.add.esc
export declare interface SymbolConstructor {
    readonly customMatcher: unique symbol,
}
```

The member needs no `@js` decorator of its own, since
`export declare var Symbol: SymbolConstructor` already carries
`@js("Symbol")` and members lower through it. A top-level addition does
carry one:

```
@js("Symbol.iterator")
export declare val iteratorKey: unique symbol
```

Adding a member the converted declaration already has fails the run.
Correcting one is `replace`.

One file may add several signatures under one name, which is how it
contributes an overload set the converted declaration has no signature
of.

A name holds one member, with one exception: a `get x()` and a
`set x()` are two halves of one accessor and share a name. So an overlay
may add the setter beside a converted getter, and adding any other form
of a name the converted declaration holds fails.

## `replace`

Takes the same file shape as `add` and differs only in what happens on a
key collision. Each overlay member substitutes the converted member
sharing its name, at that member's position, so a second run leaves the
tree byte-identical.

```
// overlay/std/array.replace.esc
export declare interface Array<T> {
    sort(compareFn?: fn (a: T, b: T) -> number) -> Self,
}
```

A member is addressed by its name, which side of the class it lives on,
and its kind. Two rules follow.

A name addresses a whole overload set, so an overlay that replaces
`Array.find` restates both of its signatures. Restating fewer than the
converted declaration holds fails the run and names the member, since a
name is what addresses the set and there is no way to point at one
signature in it. Only signatures overload, so writing one name as two
fields or two accessors fails as well.

The kind is part of the key, so a `readonly x: T` and a `get x()` are
two members rather than one. An overlay that writes a name under a kind
the converted declaration does not hold it under fails, rather than
retyping the member under cover of replacing it. Changing a member's
kind is a `drop` and an `add`, and supplying the missing half of an
accessor is an `add` on its own.

## Digest sidecars

A `replace` forks its target. The overlay wins by construction, so
TypeScript can retype the member it stands in for and the generated tree
would not move. The sidecar beside each `replace` file is what turns
that silence into a failed run:

```
overlay/std/array.replace.esc
overlay/std/array.replace.digests.json
```

The sidecar records the printed Escalier form of every converted
declaration and member the file stands in for, one entry each. A run
recomputes those digests and fails when one no longer matches, naming
the member and the file.

Recording is a separate run, so writing a new `replace` is two steps:

1. Write the overlay file.
2. Run `dts_to_esc generate --update-digests <lib-dir> <esc-dir>`, which
   rewrites the sidecars from what the overlay currently replaces.

Commit both files. The same two steps accept a member that moved
upstream, after checking the overlay still says what it should about the
new upstream form.

A digest covers the converted form, not the overlay's own text, so
editing the overlay alone needs no re-record. The converted form is
what the generator makes of the `.d.ts` declaration, so the digest
moves when the upstream type moves and when a derived fact the
generator applies to that member moves. Doc comments are left out
of the form, so the prose churn of a version bump moves no digest. Such
an edit still reaches the output, since the converted member's comment
carries onto the overlay member replacing it wherever that member wrote
none of its own. Any change to the printer's output does invalidate
every entry at once. The comparison that replaces this reads both sides
through the solver's `constrain` and waits on SimpleSub M7.5.

Write the overlay in the shape the generated file has. The generator
converts the `.d.ts` first and matches the overlay against the result,
so a declaration TypeScript spells as the `interface Foo` +
`interface FooConstructor` + `declare var Foo` trio is addressed as the
single `class Foo` the generated file holds.

A member operation contributes members alone, so the overlay writes the
name and the type parameters and nothing else. The type parameters have
to agree, since the members are read under them: writing
`class Array<U>` where the generated file holds `class Array<T>` fails
the run rather than emitting members that refer to a name nothing binds.
Everything else written around the members goes unread, so an `extends`
clause, an `implements` clause, a lifetime parameter, a `final`
modifier, or a decorator on the overlay declaration fails rather than
being dropped in silence. This holds for `add` as well as `replace`.

A whole-declaration replacement is the other case, and it does read all
of that. The overlay stands in for the declaration entire, so what it
writes around its members is what the generated file gets.

The converted member's doc comment carries onto the overlay member
replacing it, unless the overlay wrote one of its own. Upstream
documentation therefore reaches the generated tree whether or not an
overlay stands in for the member it describes.

A declaration the converter gets structurally wrong is replaced whole
rather than member by member. That happens when the overlay declaration
and the converted one disagree on declaration kind, or when the kind
holds no members at all — a `val`, `fn`, `type`, or `enum`.

## `drop`

Names what the generator must not emit. A drop file's declarations are
read for their **names alone**. Every type annotation, signature, and
body is ignored, so a drop is written in the shortest form that parses
and a run rejects an entry carrying more:

```
// overlay/drop.esc — whole symbols, package-less
export declare val eval
export declare val escape

// overlay/std/date.drop.esc — members of a package's declarations
export declare interface Date {
    getYear: unknown,
}
```

`export declare val <name>` names a whole declaration and
`<name>: unknown` names a member. A member drop removes every member
under that name, overload set included, matching the rule `replace`
follows.

Both keywords are containers for the names inside them rather than
claims about what is being dropped. `export declare type <name>` does
not parse without `= …`, so a dropped type alias is named with the `val`
form. And a declaration the converter emits as a class, `Array` and
`Symbol` among them, has its members dropped with the `interface` form
like any other.

`drop.esc` sits at the overlay root because a whole-symbol drop resolves
during routing, before a package is assigned. `eval` and `escape` belong
to none.

An entry has to name something a `lib.*.d.ts` file declares, which is
what makes a stale drop visible after a TypeScript bump. `globalThis` is
the one §6.1 drop with no entry for that reason. TypeScript synthesizes
it rather than declaring it, so there is nothing for the generator to
skip.

## What a run rejects

- An overlay file that does not parse. It is a committed input, and the
  generator reads no other `.esc`.
- A `replace` or a `drop` naming a declaration or member the upstream
  source no longer has. That is the TypeScript-side-removal signal,
  keyed on the overlay rather than on the tree the run overwrites.
- A `replace` whose converted counterpart has moved since its digest was
  recorded, or that has no recorded digest at all.
- A sidecar entry the overlay file beside it no longer replaces, and a
  sidecar with no `replace` file beside it.
- An `add` naming a declaration or member the upstream source already
  has.
- A drop entry carrying a type annotation, a signature, or an
  initializer, so that "the rest is ignored" does not become a trap for
  whoever writes a real signature and expects it to matter.
- A member operation writing anything around its members that a merge
  does not read, so the same trap does not open there.
