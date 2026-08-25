# 03 Imports

Escalier has **no ambient globals**. Every name a program uses is either declared
in that program's own module or imported. `Array`, `Promise`, `Math`, `console`,
and `document` are all behind an explicit import, unlike TypeScript, where the
whole `lib.es*` surface and, in a browser program, the whole `lib.dom` surface
are visible everywhere without asking.

Two things follow. `globalThis` does not exist — it was the union of every
ambient name, and there is nothing left to take its union over. Neither does
`eval`.

## The two kinds of import

```escalier
import "std:math"                       // pseudo-package: the JS and Web platform
import * as lodash from "lodash"        // npm package
```

Pseudo-packages hold the platform surface Escalier owns as first-class `.esc`
source. npm packages come from `node_modules` and are typed by their `.d.ts`
files.

## Pseudo-packages

The URI scheme names the platform layer:

| Scheme | Surface |
|---|---|
| `std:` | The JavaScript language surface, from ECMA-262 |
| `web:` | The Web platform surface, from the browser specs |
| `node:` | Reserved; content lands when Node support does |

```escalier
import "std:math"
import "std:json"
import "web:dom"
import "web:fetch"
```

The import binds the package under a local identifier equal to the lowercased
last URI segment:

```escalier
import "std:math"
val area = math.PI * r * r

import "web:webgl"
fn draw(ctx: webgl.WebGLRenderingContext) { ... }
```

Package names use underscores, matching the file naming, so `import
"std:typed_arrays"` binds as `typed_arrays`.

### Named imports are not accepted

All pseudo-package imports go through the bare form above. Writing
`import { sin } from "std:math"` is a compile error that points at the
alternative. Qualified-only access is the Go convention: reading `math.sin(x)`
makes the origin of `sin` visible at every call site, and gives editor tooling an
unambiguous target.

### The single-class shortcut

When a package's lowercased name matches a class declared in it, the binding
**is** that class, named with its original capitalization.

```escalier
import "std:array"
import "std:date"

val nums = [1, 2, 3]
Array.isArray(nums)           // class statics
val xs: Array<number> = []    // type position
val d: Date = Date()          // construct — no `new` keyword
```

The shortcut is structural: it fires when the package declares a top-level class
whose name matches the URI segment, ignoring case and the underscores that
separate words in a package name. That is what pairs `std:weak_ref` with
`WeakRef`. `std:array`, `std:string`,
`std:number`, `std:boolean`, `std:bigint`, `std:regexp`, `std:symbol`,
`std:object`, `std:function`, `std:date`, `std:map`, `std:set`, and
`std:weak_ref` all qualify. `std:math` declares no `Math` class, so its binding
stays the lowercase namespace `math`.

`Promise` is not on the list. It lives in `std:async` alongside the async
iteration protocol and `AggregateError`, so the access is `async.Promise.all(…)`.

Other exports of a shortcut package are reachable as namespace members on the
same binding. Where a name collides, class statics win.

### Binding-shape flags

A URI may carry `?flag` modifiers separated by `&`. The binding shape today is
`?local`, which is the default and the behavior described above. The flag slot is
reserved for future additions such as `?type-only`, and an unrecognized flag is
an error.

### Imports are runtime-erased

The names behind these imports already exist at runtime. `Math.sin` is an
ECMAScript builtin, present in every conforming engine; `console.log` is a host
API, present wherever the target JavaScript host provides one. A pseudo-package
import adds type information to the compile-time scope and codegen deletes the
import line, so the package is a type-checking grouping mechanism with zero
runtime cost.

Binding names are not always the runtime names, so each exported declaration in a
pseudo-package carries a `@js("...")` decorator naming the JavaScript expression
it lowers to. `math.sin(x)` lowers to `Math.sin(x)`, and `parseInt` from
`std:number` lowers to bare `parseInt(...)` rather than `Number.parseInt(...)`.
Construction is not carried by the decorator: codegen inserts `new` at the call
site from the callee's type, so `Date()` lowers to `new Date()`.

### The checker still knows what the language guarantees

Requiring an import for every name does not mean writing `import "std:string"`
before calling `.toUpperCase()`. Each `std:*` package loads in one of two modes.

- **Shape-loaded.** The checker loads a package's contents to satisfy needs that
  arise from the language itself: method dispatch on a string or number literal,
  the result type of `await`, the iterable protocol behind `for x in xs`, the
  array shape behind an array literal, the regex shape behind a regex literal.
  No identifier enters scope. This is the checker knowing what the language
  guarantees about its own values.
- **Named.** Naming a class, type, or value — `Array`, `Promise`, `Error`,
  `parseInt`, `Partial`, `Symbol` — requires the explicit import. The bindings
  exposed are exactly the package's top-level declarations.

Shape-loading is per-file and additive, and it never satisfies an explicit
reference. `for x in xs` works without an import; `Array.from(xs)` needs
`import "std:array"`.

### The Web surface

The entire DOM tree lives in one package, `web:dom` — `Document`, `Element`,
`Node`, `Window`, every HTML, SVG, and MathML element class, the tag-name and
event-map registries, CSSOM, events, observers, animations, and custom elements.
One import gets the whole DOM surface, and the registries are closed inside the
package, so `createElement("canvas")` narrows to `HTMLCanvasElement` and
`createElement("does-not-exist")` is an error.

Web families with no DOM coupling get their own packages: `web:fetch`,
`web:streams`, `web:crypto`, `web:workers`, `web:webgl`, `web:web_audio`,
`web:web_rtc`, `web:web_codecs`, `web:indexeddb`, `web:service_worker`,
`web:websocket`, `web:storage`, `web:url`, `web:encoding`, `web:file`,
`web:performance`, `web:webauthn`, `web:payments`. A typical browser program
imports `web:dom` plus one or two siblings.

A sibling that needs a `web:dom` type refers to it through a qualified name, so
`web:fetch`'s `Response.body` is a `web.streams.ReadableStream | null` and has to
be narrowed before a stream method can be called on it.
Pseudo-packages import each other exactly like ordinary code, and import cycles
between them are permitted.

## npm packages

Third-party packages are imported by name, as a namespace or by member.

```escalier
import * as lodash from "lodash"
import * as fp from "lodash/fp"

val result = lodash.map([1, 2, 3], fn(x) { x * 2 })
val piped = fp.pipe(fn1, fn2, fn3)
```

```escalier
import { map, filter } from "lodash"
import { useState, useEffect } from "react"

val doubled = map([1, 2, 3], fn(x) { x * 2 })
```

Members may be renamed to avoid conflicts:

```escalier
import { map as lodashMap } from "lodash"
import { map as ramdaMap } from "ramda"
```

Subpath exports are separate namespaces with their own contents, as `lodash` and
`lodash/fp` are above.

Types imported from a `.d.ts` file are **inexact** in every category, since
TypeScript cannot state that a shape is closed. See
[Exact Types](07_exact_types.md).

## Import scope and declaration scope

Imports are **file-scoped**, as in Go. Each file states what it depends on.

```escalier
// lib/utils.esc
import * as lodash from "lodash"

fn helper() -> number {
    return lodash.sum([1, 2, 3])
}
```

```escalier
// lib/main.esc — no import statement for lodash
fn main() -> number {
    return lodash.sum([1, 2, 3])   // ERROR: 'lodash' is not defined
}
```

Type and value **declarations**, by contrast, are shared across the files of a
module:

```escalier
// lib/types.esc
type UserId = string
type User = {id: UserId, name: string}
```

```escalier
// lib/users.esc — no import needed; same module
fn createUser(id: UserId, name: string) -> User {
    return {id, name}
}
```

File-scoped imports keep each file's external dependencies visible, let a file
move without breaking its imports, and give tooling an exact answer for what is
in scope where. See [Code Organization](10_code_organization.md) for how files
map onto modules and namespaces.

## Cyclic dependencies

Types may reference each other across files within a module:

```escalier
// lib/node.esc
type Node<T> = {value: T, children: Tree<T>}
```

```escalier
// lib/tree.esc
type Tree<T> = {root: Node<T>, size: number}
```

All declarations in a module are collected before checking, and a dependency
graph orders them by strongly connected component, so mutual references resolve.

Cycles between pseudo-packages are permitted. Circular dependencies between npm
packages are not.

## Shadowing

A local declaration shadows an imported one. Since there are no ambient globals,
there is nothing to shadow implicitly, and no `globalThis` escape hatch for
reaching a shadowed name. Where you need both, alias one at the import:

```escalier
import "std:array"
import { Array as VecArray } from "@repo/vec"
```

## Status

The parser, the resolver, the binding shape, the single-class shortcut, `@js`
lowering, and the `web:dom` partition are implemented.

Three pieces are still in progress. The committed `.esc` source for the `std:*`
and `web:*` packages is generated from the pinned TypeScript `.d.ts` set by a
bootstrap converter and then hand-edited, and that generation is not finished.
The per-file shape loader that makes the shape-loaded mode work is not built, so
until it is, the compiler resolves the previously-ambient names through the older
prelude. Two editor features are planned on top: **auto-import**, a quick fix that
adds the namespace import for a name the file references but has not imported, and
**adaptive diagnostic rendering**, which prints a type name in the shortest form
that is unambiguous given the bindings in the file the diagnostic came from.
