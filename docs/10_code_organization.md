# 10 Code Organization

## Packages

A package is the smallest unit of interop with TypeScript. A package is either an
Escalier package or a TypeScript package, never both, and both use a standard
`package.json` to say where the dist files and type definitions live.

```
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
  files rather than modules of their own — a module spans every file that shares
  a namespace. Statements may not appear outside a function or method in them,
  with `.test.esc` files the exception.
- **`bin/`** holds executable scripts. These run top to bottom, so statements at
  top level are allowed.
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

## Monorepos

A monorepo may hold many packages, and packages may not nest. A package imports
another package in the same monorepo through the `@repo/` scope, and may import
only that package's exported symbols even though both live in one repository.

```escalier
import * as vec from "@repo/vec"
```
