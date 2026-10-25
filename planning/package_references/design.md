# Reference values a package exports

## 1. Overview

A package hands its importers values. Some are fresh, so the importer can own
them. Others are shared with the package, so the importer may only borrow them.
This document records how Escalier decides which is which, for three kinds of
package:

- An Escalier package, whose source states ownership.
- A TypeScript package read from its `.d.ts` files, which states none.
- The `std:*` and `web:*` pseudo-packages, which are generated from TypeScript's
  own `lib.*.d.ts` files.

A **reference value** here is a value with identity that more than one holder
can see: an array, a map, a set, an object or a class instance. A number or a
string is copied and raises none of these questions.

The rules rest on the ownership model in
[docs/09_ownership.md](../../docs/09_ownership.md). Two parts of it matter most:

- An owned value has one holder, who may make it mutable or move it.
- A borrow `&T` reads a value someone else owns. A `'static` borrow reads one
  that lives for the rest of the program, such as module-level state. A borrow
  never becomes mutable, and passing it where an owned value is expected is an
  error.

## 2. Escalier packages

An Escalier package's signatures carry ownership, because the checker infers it
from the source. An importer sees the same types the package's own code does.

### 2.1 Module-level values

A module-level `val` or `var` is shared by every function in the package and
every importer. It lives for the whole program.

- A function body may borrow it, but may not move it. A move reports
  `cannot move module-level value 'xs' inside a function. Borrow it with &xs`.
  This came in with #2211.
- Passing it to an owned parameter is a move, so it reports
  `cannot move module-level value 'xs' into an owned parameter. Make the
  parameter a borrow and pass &xs`. #2215 adds this rule.
- A closure that moves a capture takes it when the closure is created, and can
  then be called only once.

### 2.2 Functions that return reference values

The inferred return type says whether the caller owns the result:

```esc
export val xs: Array<number> = [1, 2]

export fn get() { return &xs }                            // fn () -> &Array<number>
export fn fresh() { return [1, 2] }                       // fn () -> [1, 2]
export fn copyOf() -> Array<number> { return [...xs] }    // fn () -> Array<number>
export fn leak() -> Array<number> { return xs }           // rejected
```

`get` returns a borrow of module-level state, so a caller can read the result
but not make it mutable. `fresh` and `copyOf` build a new value on each call, so
the caller owns it. `leak` is rejected by the rule in 2.1, since returning `xs`
as owned would move it.

Nothing about an Escalier package needs a heuristic. A package author who wants
callers to own a result returns a fresh value, and one who wants to share state
returns a borrow.

### 2.3 Scripts

A script's top-level bindings are locals of one body, not module-level values.
A closure in a script that passes a top-level value to an owned parameter takes
the value, and a later use of it is a use-after-move.
`TestScriptValuePassedToOwnedParam`, which #2215 adds to
`internal/solver/owned_param_move_test.go`, covers this.

## 3. TypeScript packages

A `.d.ts` file says nothing about ownership. TypeScript lets any holder of an
array mutate it unless its type says `readonly`, and that keyword only promises
that this one view won't be used to mutate. It does not say whether anyone else
holds the value. `internal/dts_to_esc` converts each `.d.ts` into Escalier
declarations, and the rules below pick an ownership for each position.

### 3.1 Consts

`export declare const items: readonly number[]` is a module-level value of the
package. Rule 2.1 applies unchanged: a function body in the importer may borrow
`d.items` but may not move it.

### 3.2 Return types

A function's return type decides whether the caller owns the result.

| `.d.ts` return | Converted return | Why |
|---|---|---|
| `number[]` | `Array<number>` | Nothing says the package keeps it, so the caller owns it. |
| `readonly number[]`, `ReadonlyArray<number>` | `&'static Array<number>` | The package may keep its own reference. |
| `ReadonlyMap<K, V>`, `ReadonlySet<T>` | `&'static Map<K, V>`, `&'static Set<T>` | As above. |
| `Items`, where `type Items = readonly number[]` | `&'static Items` | An alias of a readonly collection is one too. |

The borrow applies at any depth of the return type. `Promise<readonly number[]>`
becomes `Promise<&'static Array<number>>`, and `{ items: readonly number[] }`
becomes `{items: &'static Array<number>}`. It applies to methods, statics,
overload arms and function-typed consts.

The borrow does not apply inside a parameter list. In
`onItems(cb: () => readonly number[]): void`, the array is the return type of
`cb`, which the caller writes and hands to the package, so it stays owned.

A reference to an alias resolves the way TypeScript looks a name up. It tries
the namespace the reference is written in, then each enclosing namespace. So an
owned `Items` in one namespace is not mistaken for a readonly `Items` in
another, and a qualified reference such as `ro.Items` resolves.

#2213 added the borrow for readonly collections, and #2220 extended it to
aliases.

### 3.3 Parameters

A call into a package moves none of its arguments, because a `.d.ts` parameter
has no ownership meaning. Passing a module-level value to a package function is
therefore allowed. A borrow an argument carries is still checked for escaping
the caller's frame. #2215 adds this rule.

That makes every package parameter act like an immutable borrow, which is
unsound for a function that keeps or mutates its argument:

```ts
export declare function register(handlers: Handler[]): void;  // keeps handlers
export declare function sortInPlace(xs: number[]): void;      // mutates xs
```

#2222 tracks a policy that decides between an owned parameter, an immutable
borrow and a mutable borrow.

### 3.4 Known gaps

- A readonly collection in an interface field stays owned, because the field is
  not in a return position. #2218 lists this as a case to decide on.
- A mutable `T[]` that the package also keeps reads as owned. TypeScript gives
  no way to tell this apart from a fresh array, so the rule trusts the missing
  `readonly`.
- `val mut m = d.make()` on a borrowed result has no effect, since a borrow's
  mutability belongs to its type. The first error appears at the first write.
  #2223 tracks a diagnostic at the binding itself.

## 4. Fresh versus shared readonly returns

Every readonly return is a `'static` borrow today. That is sound but too strict
for a function that builds a new collection on each call:

```ts
export declare function range(n: number): readonly number[];
export declare function getItems(): readonly number[];
```

`range` most likely returns a fresh array that the caller could own. `getItems`
most likely returns one the package still holds. #2219 tracks the heuristics
that tell the two apart. A return they judge fresh becomes owned, and every
other readonly return stays a `'static` borrow.

### 4.1 Default

A return that no signal decides stays a `'static` borrow. Treating a shared
collection as owned lets the caller mutate state the package still reads, which
is unsound. Treating a fresh one as a borrow only rejects a program that would
have been safe.

### 4.2 Signals

The signals below are listed from strongest to weakest. The first one that
gives an answer decides.

1. **An explicit annotation.** A per-package override file, or a JSDoc tag in
   the `.d.ts`, states the answer. This is the only signal that can overrule the
   default for a getter or a property.
2. **The declaration form.** A getter or a readonly property returns shared
   state. A method or a function may return either, so it falls through to the
   next signals.
3. **The parameters.** A function with no parameters that returns a readonly
   collection more likely returns state it holds. One that computes from its
   arguments more likely builds a new value. This signal alone never judges a
   return fresh, since `getItemsFor(id)` reads shared state too.
4. **The name.** A name that starts with `create`, `make`, `to`, `from`, `range`,
   `clone` or `copy` suggests a fresh value. A name that starts with `get` or
   names a field suggests a shared one.

This document proposes how the signals combine, and #2219 settles it. A return
is judged fresh only when signal 1 says so, or when signals 3 and 4 both point
to a fresh value and signal 2 does not rule it out. Every other combination
keeps the borrow.

### 4.3 Overrides

Heuristics will guess wrong for some packages, so the override mechanism has to
reach return ownership. #2222 needs the same mechanism for parameters, and the
receiver-mutability policy in #577 and #500 already reads per-package
overrides. One override file per package should answer all three questions.

### 4.4 Done when

#2219 is done when the heuristics in 4.2 are implemented in
`internal/dts_to_esc/ref_rewrite.go` and this document matches them. A return
judged fresh must be bindable with `val m: mut Array<number> = …`, and one
judged shared must still be rejected. Tests should cover both in
`TestNodeModulesImportRejectsUpgradingAReadonlyViewCall` and
`TestNodeModulesImportReadsAReadonlyCollection`.

## 5. The standard library

The `std:*` and `web:*` pseudo-packages are generated from TypeScript's
`lib.*.d.ts` files and committed under `internal/interop/data`. They go through
a different path from npm packages, and the rules in section 3 do not all apply.

### 5.1 How the generator treats readonly types

The generator merges each readonly twin into its mutable class. The members of
`ReadonlyArray<T>` join `class Array<T>` as `&self` methods, and a reference to
`ReadonlyArray<T>` becomes `Array<T>`. No return becomes a `'static` borrow:

```ts
// lib.es5.d.ts
interface TemplateStringsArray extends ReadonlyArray<string> { ... }
// lib.es2020.intl.d.ts
function getCanonicalLocales(locale?: string | readonly string[]): string[];
// lib.dom.d.ts
interface Navigator { readonly languages: readonly string[]; }
```

```esc
// std:string
export declare interface TemplateStringsArray extends Array<string> { ... }
// std:intl
export declare fn getCanonicalLocales(locale?: string | Array<string>) -> Array<string>
// web:dom
readonly languages: Array<string>
```

A call into the prelude or a pseudo-package moves none of its arguments, the
same as a call into an npm package. #2215 applies that rule to every package the
run loads.

### 5.2 Why the standard library differs

Most standard-library functions that return a collection build a new one, such
as `Array.prototype.map`, `Object.keys` and `Intl.getCanonicalLocales`. A
`'static` borrow on those would reject ordinary code like
`val mut keys: mut Array<string> = Object.keys(o)`. So the generator keeps them
owned.

The shared cases are mostly properties and live collections on the DOM:

- `navigator.languages` is a readonly property the browser holds.
- `node.childNodes` is a `NodeList` that changes as the document changes.
- `document.styleSheets` is a `StyleSheetList` the document holds.

These are properties rather than function returns, and their types are
interfaces rather than readonly collections. Section 3.2's return rule would not
reach them even if the generator applied it.

### 5.3 Proposed decisions for the standard library

These are proposals. They take effect when the work that changes the generator
adopts them.

1. **The generator does not borrow readonly returns.** The npm rule in 3.2 stays
   specific to `ConvertModuleWithOverrides`, the path npm packages take. Turning
   it on for the generator would make returns like `Object.keys` borrows, which
   section 5.2 rejects.
2. **Shared standard-library values are marked by override.** A property or a
   function that returns state the runtime holds gets its borrow from an
   overlay, the override mechanism in `internal/dts_to_esc/overlay.go` that the
   generator already applies. This covers `navigator.languages`
   and the live DOM collections, which no type-based rule can find.
3. **The heuristics in section 4 stay off for the generator.** The standard
   library is small enough to review by hand, and a heuristic that misjudges a
   standard-library function affects every Escalier program.
4. **Regeneration changes committed files.** Any change to these rules for the
   standard library regenerates `internal/interop/data`, and the fixtures that
   print standard-library types change with it. Run both
   `UPDATE_SNAPS=true go test ./...` and `UPDATE_FIXTURES=true go test ./cmd/...`
   after regenerating, so snapshots and fixtures stay in sync.

### 5.4 Open questions

- Whether a readonly property of a standard-library interface should read as a
  `'static` borrow, or as an owned value the holder cannot make mutable. The
  first matches the npm rule for returns. The second is what the generator
  produces today.
- Whether `std:*` and `web:*` parameters follow the policy #2222 settles for npm
  packages, or keep taking their arguments without moving them.

## 6. Related issues and pull requests

- #2210: a readonly collection from an npm package could be made mutable. Fixed
  by #2213.
- #2211: rejects moving a module-level value inside a function.
- #2212: passing a module-level value to an owned parameter. #2215 fixes it.
- #2218: readonly collections returned through type aliases. Fixed by #2220.
- #2219: telling a fresh readonly return from a shared one.
- #2222: the ownership and mutability of `.d.ts` parameters.
- #2223: a diagnostic for `val mut` on a borrow.
- #577 and #500: receiver mutability for `.d.ts` methods.
