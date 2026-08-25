# 10 Code Organization

## Packages

A package is the smallest unit of interop with TypeScript. A package is either an
Escalier package or a TypeScript package, never both, and both use a standard
`package.json` to say where the dist files and type definitions live.

An Escalier package has a fixed layout. A TypeScript package has whatever layout
its authors chose — Escalier reads it through its `package.json` and its `.d.ts`
files, so nothing below applies to it.

```text
package.json
lib/
  index.esc
  foo/
    foo.esc
    bar/
      bar.esc
bin/
  main.esc
build/            # generated
```

- **`lib/`** holds the package's library source. Its `.esc` files are source
  files rather than modules of their own: **one module spans all of `lib/`**,
  subdirectories included. A subdirectory introduces a namespace within that
  module rather than a module of its own. Statements may not appear outside a
  function or method in these files, with `.test.esc` files the exception.
- **`bin/`** holds executable scripts. These run top to bottom, so statements at
  top level are allowed. A `bin/` script reaches the package's own `lib/`
  exports by name, with no import — the lib namespace sits between the prelude
  and the script's own scope. Each script is checked on its own, so one `bin/`
  file never sees another's declarations.
- **`build/`** holds the generated `.js`, `.d.ts`, and source maps.

## Namespaces

The directory structure under `lib/` defines a nested namespace hierarchy. The
path of a file, minus the `lib/` prefix and the filename, is the namespace its
declarations land in.

| File | Namespace |
|---|---|
| `lib/index.esc` | root |
| `lib/foo/foo.esc` | `foo` |
| `lib/foo/bar/bar.esc` | `foo.bar` |

Files sharing a namespace merge their declarations into it, so splitting a
namespace across several files needs no plumbing.

A child namespace sees every symbol in its parent namespaces without qualifying.
A parent must qualify to reach a child's symbols. This matches how namespaces
work in TypeScript.

The layout above corresponds to:

```ts
namespace Foo {
    namespace Bar {
        function bar() {
            return "bar"
        }
    }
    export function foo() {
        return "foo"
    }
}
export function foo_bar() {
    return Foo.foo() + Foo.Bar.bar()
}
```

## Declarations, imports, and dependency order

Declarations are visible across the whole module regardless of source order.
Before checking, every declaration is collected and a dependency graph orders
them by strongly connected component, so a function may call one defined further
down, and two types may reference each other across files.

Imports do **not** work this way: they are file-scoped, and each file states its
own. See [Imports](03_imports.md).

## Exporting

A declaration marked `export` is exported from the bundle built for the package.
A namespace that exports nothing is left out of the exports entirely.

```esc
export fn greet(name: string) -> string {
    return `hello, ${name}`
}
```

## Bundling

Files outside `lib/` and `bin/` are neither bundled nor published. Files in
`bin/` are published but stay out of the dist bundle.

There are two bundling modes:

- **`prod`** bundles everything into a single file.
- **`dev`** produces several bundles derived from the dependency graph, which is
  what hot-reloading tools such as Vite need.

A third mode, **`test`**, is planned. It exists to make symbols in other packages
mockable. It emits a bundle whose symbols are all read through a module-level
object rather than bound directly, so a test can replace one entry on that object
and every call site picks up the replacement. Jest and similar libraries already
work this way against CommonJS; the `test` bundle gives them the same handle over
an Escalier build.

## Monorepos

A monorepo may hold many packages, and packages may not nest. A package imports
another package in the same monorepo through the repository's own scope, and may
import only that package's exported symbols even though both live in one
repository.

The scope is the repository's own name, not a literal `@repo`. In a monorepo
called `acme`, a package named `vec` is `@acme/vec`:

```escalier
import "@acme/vec"

val v = vec.zero()
```

The binding is the specifier's last segment, so `@acme/vec` binds as `vec`.
