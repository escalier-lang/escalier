# 10 Code Organization

## Packages

This is the smallest unit of interop with TypeScript.  A package
must either be an Escalier package or a TypeScript package.  Both
will use a standard package.json for specifying where dist files
and type definitions live.

Within a package all symbols are visible.  There is no need to
import something from one file to another.  The directory structure
defines a series of nested namespaces.  The only imports necessary
are for external packages or other packages within a monorepo.

Child namespaces have access to all symbols in parent namespaces 
automatically.  Parents must qualify symbols in child namespaces.  
This maches how namespaces work in TypeScript.

Example file structure:
- package.json
- src
  - foo
    - bar
      - bar.esc  
    - foo.esc
  - foo_bar.esc

This would be equivalent to the following TypeScript file:
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

Any symbol marked with the `export` modifier will be exported from
the bundle that is created when building the package.  If a
namespace doesn't export anything then it won't be include in the
exports.

Any .esc file located inside src/ will be considered a module which
prohibits the use of statements outside of functions/methods.  The exception to this is .test.esc files.

## Bundling

Files outside of the src/ are not included in the dist file bundle
or the published package.  Files inside the bin/ will be included in
the published package but not in dist file bundle.

There are two bundling modes:
- `prod`: bundles everything into a single bundle
- `dev`: produces a number of bundles based on the dependency graph

`dev` mode is to support hot-loading using tools such as `vitejs`.

## Monorepo

Monorepos can have multiple packages.  Packages cannot be nested.
Packages can import other packages within the monorepo using the
`@repo/` scope.  A package can only import exported symbols even if
both packages are in the same monorepo.
