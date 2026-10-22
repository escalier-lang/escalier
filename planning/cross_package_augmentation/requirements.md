# Requirements: Cross-package augmentation

## 1. Problem statement

Many TypeScript packages expect their users, or other packages, to add members
to an interface the package declares. TypeScript calls this module
augmentation. A file writes `declare module "<specifier>" { ... }`, and each
interface inside the block merges into the same-named interface of the target
package.

Escalier has no equivalent. A program that uses one of these packages cannot
express the types the package's authors intend, so it either loses type
information or fails to check. Custom test matchers are the case that prompted
this document. [package_testing](../package_testing/requirements.md) section 8
depends on it. The same mechanism appears across the ecosystem, so this
document specifies augmentation as a general language feature rather than a
testing feature.

## 2. Background

### 2.1 Module augmentation in TypeScript

A `declare module "x" { ... }` block means one of two things in TypeScript,
depending on the file that holds it.

- **Ambient module declaration.** In a file with no top-level `import` or
  `export`, the block declares the whole of module `x`. This is how a hand-written
  `.d.ts` types an untyped package.
  [packages_vs_globals](../packages_vs_globals/requirements.md) rule 3.2
  already covers this case and treats each block as its own package.
- **Module augmentation.** In a file that is itself a module, the block patches
  the existing module `x`. Each interface or namespace in the block merges into
  the declaration of the same name in `x`. TypeScript does not let an
  augmentation add new top-level declarations to `x`.

`declare global { ... }` is the global counterpart of the second form. It
merges into global declarations.

TypeScript activates an augmentation for the whole program as soon as the file
holding it is part of the program. No file has to import the augmenting file
for its effect to apply.

### 2.2 Uses in the ecosystem

| Package | Interface augmented | Written by |
|---|---|---|
| Jest | `Matchers<R>` in `expect` | The project, for custom matchers |
| Vitest | `Assertion<T>` in `vitest` | The project, for custom matchers |
| `@testing-library/jest-dom` | Jest's and Vitest's matcher interfaces | A third-party `.d.ts` |
| Vue Router | `ComponentCustomProperties` in `vue` | A third-party `.d.ts` |
| Fastify | `FastifyInstance`, `FastifyRequest` | The project, for decorators |
| styled-components | `DefaultTheme` | The project, for its theme type |
| i18next | `CustomTypeOptions` | The project, for typed translation keys |
| Express | `Express.Request`, through `declare global` | A third-party `.d.ts` or the project |

Two kinds of author appear in the table. Some augmentations ship inside a
dependency's `.d.ts`, and the program only has to load them. Others are written
by the project itself, which in an Escalier program means Escalier source.
Both have to work.

### 2.3 Current state in Escalier

- The parser accepts `declare module "x" { ... }` and `declare global { ... }`.
  Today only interop override files use them, always with the `override`
  prefix, as `override declare module "x" { ... }`. The solver does not read
  either form from ordinary source.
- Interfaces merge within one namespace. Two `interface Foo` declarations in
  the same namespace combine, and an interface that shares a name with a type
  alias, class, or enum is a `ConflictingTypeDeclarationError`.
- Interfaces and namespaces are inexact, per
  [exact-types](../exact-types/requirements.md) sections 2.4 and 2.5, because
  their member set is not closed at one declaration site. Augmentation relies
  on this and needs no change to the exactness rules.
- The `.d.ts` converter rejects a `declare module` block nested inside a
  namespace with "module declarations are not supported".
- [interop_mutability/dts_to_esc_proposal.md](../interop_mutability/dts_to_esc_proposal.md)
  proposed augmenting DOM registry interfaces such as `HTMLElementTagNameMap`
  across `web:*` packages. It chose to activate an augmentation only in files
  that import the augmenting package.
- [builtins/implementation_plan.md](../builtins/implementation_plan.md) section
  4 found two gaps that stopped that proposal. First, there is no per-importer
  view of a merged interface. Second, `keyof HTMLElementTagNameMap` inside
  `createElement`'s signature is resolved once, where `createElement` is
  declared, so a later augmentation cannot reach it. The builtins work avoided
  both by putting every registry in one `web:dom` package.

## 3. Goals

1. Escalier source can augment an interface declared by another package, using
   the `declare module` syntax a TypeScript user already knows.
2. Augmentations inside a dependency's `.d.ts` take effect the way they do in
   TypeScript, so packages such as `@testing-library/jest-dom` work unchanged.
3. Every reference to an augmented interface sees the augmented members,
   including references inside the target package's own signatures. This
   covers `keyof I` and `I[K]`.
4. The set of augmentations a file sees is determined by its package and the
   package's own source and imports. It never depends on an unrelated sibling
   package in the same build.
5. An Escalier package that augments another package carries the augmentation
   in its published `.d.ts`, so TypeScript consumers see it too.

## 4. Non-goals

- Adding new top-level declarations to another package. TypeScript forbids this
  in an augmentation, and Escalier follows it.
- Augmenting a class, enum, type alias, or function. Section 6.3 lists what may
  be augmented.
- Augmenting the `std:` and `web:` pseudo-packages from user code. Those are
  Escalier-owned surfaces, refined through the override system instead.
- Changing generated JavaScript. Augmentation affects types only.

## 5. Syntax

An augmentation is a top-level `declare module` block without `override`:

```escalier
import "pg"

declare module "fastify" {
    interface FastifyInstance {
        db: pg.Pool,
    }
}
```

- The specifier is an import specifier, resolved the same way an `import`
  statement resolves it. It may name an npm package or another Escalier package
  in the same monorepo.
- The block holds only `interface` and `namespace` declarations. A namespace in
  the block may hold further interfaces and namespaces.
- Inside the block, a name resolves first against the target package's own
  declarations and then against the enclosing file's scope. `FastifyInstance`
  could refer to other Fastify types without qualifying them, and `pg.Pool`
  resolves through the file's own import.
- The block does not import the target package into the file. A file that also
  calls Fastify's API writes `import "fastify"` as usual.

`override declare module` keeps its meaning as an interop override. The two
forms differ in purpose. An override refines how Escalier reads an existing
declaration, such as its receiver mutability. An augmentation adds members the
package's authors left open for others to add.

## 6. Merge rules

### 6.1 Members

An augmenting interface merges into its target the way two interfaces in one
namespace merge today.

- A property the target lacks is added.
- A property the target already has must have an identical type. A different
  type is an error.
- A method signature joins the method's overload set. Overloads from an
  augmentation come before the target's own, as in TypeScript.
- An augmentation's `extends` clause adds supertypes to the target.

### 6.2 Type parameters

An augmenting interface declares the same type parameters as its target, with
the same names, in the same order, and with the same bounds and defaults. Jest's
`Matchers<R>` is augmented as `interface Matchers<R> { ... }`. A mismatch is an
error.

### 6.3 Targets

An augmentation may target these declarations in the target package:

- an interface;
- a namespace, whose interfaces and nested namespaces are augmented by the
  same rules.

Naming a declaration the target does not have is an error, so a misspelled
interface name is reported rather than silently declaring a new interface.
Naming a class, enum, type alias, or function is an error.

## 7. Augmentations in `.d.ts` files

The `.d.ts` loader classifies a `declare module` block by the file that holds
it, following TypeScript:

- In a file with no top-level `import` or `export`, the block is an ambient
  module declaration under packages_vs_globals rule 3.2.
- In a file that is a module, the block is an augmentation of the named
  package. The loader converts it into the same form as an Escalier
  augmentation, so sections 6 and 8 apply to both.

A `declare global` block in a `.d.ts` merges into the global declarations the
packages_vs_globals R6 rules describe. Escalier source has no ambient globals
and cannot write `declare global`.

The members of a converted augmentation go through the same conversion as any
other `.d.ts` declaration. That includes the receiver-mutability classification
from [interop_mutability](../interop_mutability/requirements.md).

## 8. Activation

Activation decides which files see an augmentation. This is the main departure
from TypeScript, which activates every augmentation for the whole program.

### 8.1 Views

A view is the set of files that check against one merged form of every
augmented interface. Each package has two views.

- The **lib view** holds the package's `lib/` and `bin/` files.
- The **test view** holds the lib view's files plus the package's `.test.esc`
  and `.setup.esc` files.

### 8.2 Which augmentations a view activates

A view activates:

1. every augmentation written in a file of that view;
2. every augmentation shipped by a package that some file of the view imports.

Activation is transitive through a dependency's own imports. When a view imports
`vue-router`, and `vue-router`'s `.d.ts` augments `vue`, the augmentation is
active in the view.

This gives the testing design what it needs. A custom matcher declared in a
setup file is active in the test view, so every test file sees it, but the lib
view does not. A setup file that imports `@testing-library/jest-dom` activates
that package's augmentations for every test file. `lib/` code never sees test
matchers, and a published package never carries them.

### 8.3 Why views instead of per-file activation

The DOM registry proposal activated an augmentation only in files that import
the augmenting package. That rule fails two common cases.

- A custom matcher is declared once and used by every test file, and no test
  file imports the file that declares it.
- `@testing-library/jest-dom` is imported once, in a setup file, by convention.

Per-file activation would also need a separate merged form of each interface
for every distinct import set. A view needs one per view.

Views keep goal 4. What a file sees depends only on its own package. A sibling
package in the same monorepo never changes it.

## 9. Resolving augmented interfaces

An augmented interface's members are those of the merged form in the viewing
file's view. Every reference resolves against that merged form, including:

- a reference inside the target package's own signatures, such as the `R`
  returned from each member of `Matchers<R>`;
- `keyof I` and `I[K]`, such as `keyof HTMLElementTagNameMap` in
  `createElement`'s type parameter bound.

The target package's declarations therefore cannot fix the members of an
augmentable interface when the target is loaded. The checker builds each
view's merged forms before it checks any file in the view, and it resolves
references to an augmentable interface through them. This closes the gap the
builtins work found, where `keyof HTMLElementTagNameMap` was resolved where
`createElement` is declared.

A dependency is still loaded and converted once per build. Only the merged
interfaces differ between views.

## 10. Emitting `.d.ts`

An augmentation in a lib-view file is part of the package's public types. The
package's `build/lib/index.d.ts` emits it as a TypeScript module augmentation,
so a TypeScript consumer of the package sees the same members:

```ts
import type { Pool } from "pg";

declare module "fastify" {
  interface FastifyInstance {
    db: Pool;
  }
}
```

An augmentation in a test-view file is not emitted to `build/lib/`. When the
test build's `index.d.ts` from package_testing section 7 is published, it
carries the test view's augmentations.

## 11. Diagnostics

Each message below is the full text the checker reports.

| Condition | Message |
|---|---|
| The specifier does not resolve | `cannot augment "fastify": package not found` |
| The target has no such declaration | `cannot augment FastifyInstanc: "fastify" does not declare it` |
| The target is not an interface or namespace | `cannot augment Router: only interfaces and namespaces can be augmented` |
| Type parameters differ | `augmentation of Matchers must declare the type parameters <R>` |
| A property's type differs | `augmentation of FastifyInstance redeclares db as string, but it is pg.Pool` |
| The block holds another declaration kind | `a declare module block may contain only interfaces and namespaces` |
| `declare global` in Escalier source | `declare global is not supported: Escalier has no ambient globals` |
| The target is a `std:` or `web:` package | `cannot augment "web:dom": use an override file to refine a platform package` |

## 12. Open questions

1. **Bare imports for activation.** A setup file activates
   `@testing-library/jest-dom` by importing it, and the import binds
   `jest_dom` even though nothing reads the binding. Decide whether that unused
   binding is reported, or whether a side-effect-only import form is needed.
2. **Interfaces merged with classes.** TypeScript merges an interface into a
   class of the same name, and some packages are augmented that way. Decide
   whether a later version allows a class as a target.
3. **Cost of views.** The checker builds merged forms per view. Measure how
   many interfaces a large dependency graph makes augmentable before deciding
   whether to build them eagerly or on first use.
4. **DOM registries.** With section 9 in place, the `web:dom` registries could
   become augmentable again. Decide whether custom elements, the deferred user
   of that work, should register through user-code augmentation of
   `HTMLElementTagNameMap`, which section 4 excludes for `web:` packages.
