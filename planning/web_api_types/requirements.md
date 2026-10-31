# Requirements: Improving the web API type definitions

## 0. Scope

This is one of two streams. It covers what a declaration in
`internal/interop/data/web/` **says**: its members, their signatures, which
package holds it, and whether a global is reachable at all.

`planning/web_environments/` covers **where** a declaration exists, which
global scopes have it and what the compiler does about that. The two share one
input, the WebIDL generator in section 4, and are otherwise independent.

Both streams were one document until this split. Anything here about
environments is a dependency note, not a requirement; the other folder owns it.

## 1. Problem statement

The tree is converted from TypeScript's `lib.dom.d.ts` and
`lib.webworker.d.ts`, which are themselves generated from WebIDL by
TSJS-lib-generator. So the tree is a derivative of a derivative, and the
intermediate step loses information and invents some.

Two cases found so far, both measured:

**It invents constructors.** 628 of the 635 `declare var` blocks in
`lib.dom.d.ts` carry a construct signature, 452 of them a bare `new()`, on
interfaces the platform does not let you construct. `new ImageBitmap()`,
`new MessagePort()`, `new Navigator()` and `new Element()` all typecheck against
the tree and throw `TypeError: Illegal constructor` at runtime. #1645.

**It loses a set.** There is no `Transferable` union in the platform. WebIDL
marks `[Transferable]` on each interface and sixteen carry it; TypeScript's
convenience union names ten, omitting `MIDIAccess`, `MediaStreamTrack`,
`MediaStreamTrackHandle` and the three WebTransport streams. The tree inherits
the omission. `planning/web_environments/transferable.md` has the table.

A third is fixed and worth recording as the pattern: an interface with several
supertypes used to convert to a class with one, so `Element` kept `Node` and
lost `querySelector`. #1648 fixed it with `implements`.

Separately, and not a WebIDL question: the `*GlobalScope` classes describe what
each runtime puts in scope, and a program cannot reach into them, so a page
cannot call `alert` or set `onclick`.

## 2. What the tree does today

### 2.1 One conversion input

`internal/dts_to_esc` reads the pinned `lib.*.d.ts` set, the ECMA-262 derived
facts, and the overlay under `internal/interop/overlay/`. WebIDL is not an
input. The overlay is where a wrong or unrepresentable declaration gets
corrected by hand, which makes it the current answer to everything in section 1.

### 2.2 Globals live on classes nobody can name

`Window` and `WindowOrWorkerGlobalScope` are ordinary declarations in
`web/dom.esc`. Their members are the page's globals, and a program has no
syntax that reaches them, because Escalier gives a program package bindings
rather than the global object.

`web:fetch` shows the form a global should take, and `std:console` does the
same:

```
@js("fetch")
export declare fn fetch(input: RequestInfo | url.URL, init?: RequestInit) -> Promise<Response>
```

So the shape is established in two packages and applied to none of the scope
classes. The worker scope classes are not in the tree at all, which the other
stream records.

### 2.3 `web:core` binds unprefixed

`coreURI` at `internal/dts_to_esc/import_header.go:33` and
`internal/solver/imports.go:164` make `web:core` the one package whose exports
an importer binds without a qualifier. The comment beside it groups `web:core`
with `std:prelude` as the two packages that do this.

## 3. Requirements

### 3.1 Functional

**F1. WebIDL is an input to conversion.** A generated, checked-in table carries
the facts `[Exposed]`, `[Global=...]`, `[Transferable]` and each interface's
`constructor()` operations, and the converter reads it. Where WebIDL and the
`.d.ts` disagree on one of those, WebIDL wins.

**F2. A declaration carries a constructor only where the platform has one.** An
interface with no `constructor()` operation in WebIDL emits no constructor,
however the `.d.ts` declares it. #1645.

Removing the constructor must leave the class usable as the right operand of
`instanceof` and as a pattern-match discriminator. `x is ImageBitmap` stays
valid and correct, because `instanceof` reads the constructor's `prototype` and
never invokes `[[Construct]]`.

**F3. A derived set is derived.** `Transferable` is generated from
`[Transferable]` rather than copied from TypeScript's union. The same holds for
any other set WebIDL states and the `.d.ts` restates.

**F4. A global is reached through a package binding.** Every member of a
`*GlobalScope` class is a top-level declaration in the package owning its
family, and no program is handed a scope object to read members off.

**F5. Each member kind hoists to its matching top-level form.** A method becomes
a `declare fn`, a read-only property a `declare val`, a settable event-handler
property a `declare var`, and an overload set one `declare fn` per signature.
The hoisted declaration carries `@js` with the global's own name.

**F6. A member a family package already declares is not hoisted twice.** `fetch`
is a member of `WindowOrWorkerGlobalScope` and a top-level declaration of
`web:fetch`. The hoist drops the member rather than adding a second binding for
one global.

**F7. `addEventListener` and `removeEventListener` live in `web:core`.** They are
among the most used globals on the platform, and `web:core` already holds the
event model they belong to. What that pulls along is the event maps, which are in
`web:dom` today, so either they move too or `web:core` imports the package
holding them.

**F8. `web:core` is ambient rather than imported.** Its declarations are injected
into scope the way `std:prelude`'s are, so a program writes `setTimeout(...)` and
`Event` without naming a package. `import "web:core"` stays legal as a redundant
no-op, which is how the generated tree and existing code migrate without a flag
day.

This removes a special case rather than adding one. `coreURI` exists in two
places to make `web:core` bind unprefixed, and the comment beside it already
calls `web:core` and `std:prelude` the two packages that do. The exception goes.

It also decides F4's ergonomics. F4 moves a global out of a scope class and into
a package, which on its own means a file that sets a timer has to import
`web:core` first, worse than the global it replaces.

**The boundary needs stating, because the obvious justification proves too
much.** `Document` and `Element` are as ambient at runtime as `Event` is, so
"ambient on the platform" would pull `web:dom` in too. What earns `web:core` the
prelude is that it is small and portable: 24 declarations against
`std:prelude`'s 44. A package that is large stays an import however ambient the
runtime makes it.

### 3.2 Non-functional

**N1. The generated table is pinned and hermetic.** `@webref/idl` is a
devDependency, the table it produces is checked in, and a test asserts the table
against the pinned version. Conversion stays offline.

**N2. A hoisted global compiles to a bare reference.** Codegen emits
`importScripts(...)`, never a member access on a scope object, since no such
object is in scope.

**N3. The scope classes survive as types.** Event-handler signatures annotate
`this` with the scope class and event maps are keyed by it, so hoisting the
members does not make the classes removable.

**N4. The ambient name set is pinned.** F8 makes `web:core`'s membership decide
what every file has in scope, where today it decides which package holds a
declaration. A test asserts the set of ambient names, so adding one is a
deliberate edit rather than a side effect of moving a declaration.

**N5. Nothing regresses load cost.** Measured against
`BenchmarkStdlibClosureLoad`. #1861 and #1862 are open against that benchmark
and both want resolving before any figure from it is trusted.

## 4. The shared input

Both streams need one generator, and it belongs to this one because the facts it
reads are facts about declarations:

1. Add `@webref/idl` as a devDependency. It is reachable from the build
   environment.
2. Read `[Exposed]` per interface and `[Global=...]` per global scope, which is
   what `planning/web_environments/` consumes.
3. Read `[Transferable]` and each interface's `constructor()` operations, which
   is what F2 and F3 consume.
4. Emit a Go table and check it in.

The environment stream cannot start before this lands, because an `@env("window")`
is a claim about the other eight environments and only `[Exposed]` knows it.

## 5. Open questions

1. **How far does the augmentation go?** F1 names four facts because four have
   been measured. WebIDL carries more that the `.d.ts` flattens: nullability
   markers, integer width and range through `unsigned long`, `[Clamp]` and
   `[EnforceRange]`, `sequence<T>` against array, `USVString` and `ByteString`
   against `string`, `optional` with a default, and `[SameObject]` and
   `[NewObject]`. Each is a separate piece of work with its own payoff, and none
   is measured yet.
2. **Does the overlay shrink?** The overlay exists partly to correct what the
   `.d.ts` gets wrong. Every fact moved to WebIDL is a reason for an overlay
   entry to go, and nobody has checked which.
3. **Does `Window` get hoisted whole?** F4 says every member, which for `Window`
   and its mixins is around 176 declarations. `planning/web_api_types/hoisted/`
   has the analysis. Whether they all belong in `web:dom` or the browsing-context
   API earns a `web:window` is open.
4. **Does `self` survive?** `WorkerGlobalScope.self` is the global object, and
   exposing it returns the program the handle F4 withholds.
5. **What is the `this` of a hoisted listener?** `addEventListener` annotates its
   callback with `this: DedicatedWorkerGlobalScope`, which stays true after the
   hoist even though the receiver is no longer nameable.

## 6. Non-goals

- Replacing the `.d.ts` set as the conversion input. WebIDL supplements it with
  facts it does not carry or gets wrong. Generating the tree from WebIDL alone
  is a larger question and this does not answer it.
- Runtime feature detection in user code, such as narrowing on
  `typeof OffscreenCanvas !== "undefined"`.
- `typeof globalThis`. F4 withholds the global object, so the intersections
  naming it lose their second half and the annotation goes. Nothing replaces it.

## 7. Related issues

| issue | relation |
| --- | --- |
| #1645 | the tree declares constructors that throw, which is F2 |
| #1648 | an interface's supertypes beyond the first were dropped. Fixed with `implements`, and the pattern this stream generalizes |
| #1646 | a type guard on an alias to a class matches every value |
| #1643 | comment attachment dominates cold load |
| #1861 | warm whole-tree inference regressed 1.5x |
| #1862 | the closure benchmark silently measures fewer packages than it claims |
| #1586 | the tier is per package where availability is per declaration |
