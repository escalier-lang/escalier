# Requirements: Testing Escalier packages

## 1. Problem statement

An Escalier package has no way to run unit tests today. Two parts of the current
design get in the way.

- A `lib/` file may not hold top-level statements. Jest, Vitest, and similar
  runners register tests through top-level calls such as `describe(...)` and
  `test(...)`, so a test file has to run top to bottom the way a `bin/` script
  does.
- The `prod` and `dev` builds bind every top-level symbol directly. A function
  calls a sibling through a module-local name, so replacing that name from
  outside the module has no effect. Tests that mock or spy on a function, or
  that set a private value, need a handle the module's own code also reads
  through.

This document specifies test files and a `test` build that addresses both.
[10 Code Organization](../../docs/10_code_organization.md) already reserves the
`test` bundling mode and the `.test.esc` suffix. This document fills in their
behavior.

## 2. Goals

1. A test file is an entry point. Its top-level statements run in source order,
   so test-runner registration calls work.
2. A test can read and replace any top-level symbol of the package under test,
   including symbols the package does not export.
3. A replacement is visible to every other function in the package. Spying on
   `fetchUser` changes what `greet` calls when `greet` calls `fetchUser`.
4. A test file names the package it tests through an explicit import, the same
   way it names Vitest or Jest. The handle that import produces is the object a
   spy is installed on.
5. The test build reuses the package's existing type check. It differs from the
   `prod` build only in codegen.

## 3. Non-goals

- Having the test runner compile `.esc` files itself. The first version requires
  the user to run the Escalier build before running the tests. Section 9 covers
  the follow-up.
- Shipping a test runner or an assertion library. Tests use Jest, Vitest, or any
  runner that executes ES modules.
- Mocking symbols of other packages in the first version. A test mocks another
  package through its runner's module-mocking facility, such as `vi.mock`, as it
  would in TypeScript. Section 10 describes how a published test build could
  lift this.

## 4. Test files

A test file is any file under `lib/` whose name ends in `.test.esc`. It sits
beside the code it tests:

```text
lib/
  index.esc
  index.test.esc
  geo/
    shapes.esc
    shapes.test.esc
```

A test file is checked as a script, with the same rules as a `bin/` script.

- Top-level statements are allowed and run in source order.
- Each test file is checked on its own and never sees another test file's
  declarations.
- A test file is not part of the `lib/` module. Its declarations do not join a
  namespace, and the `prod` and `dev` builds leave it out.

A test file differs from a `bin/` script in one way. A `bin/` script sees the
package's exports by name with no import. A test file sees none of the package's
symbols until it imports the package's test build, as section 5 describes. The
location of a test file under `lib/` does not put the namespace of its directory
in scope either. `lib/geo/shapes.test.esc` reaches `Point` as `my_lib.geo.Point`.

The test framework is imported explicitly too. Escalier has no ambient globals,
so a test file imports whatever framework it uses. A Vitest test writes
`import "vitest"`, and a Jest test writes `import "@jest/globals"` in place of
the globals Jest would otherwise inject.

The explicit import exists because the test reads symbols through the same
object it mocks on. Binding `greet` by name into the test's scope would hand the
test a second path to the function, one that a spy on the test-build object does
not affect.

## 5. Importing the package under test

A test file imports its own package's test build with the package's own name and
the `?test` flag:

```escalier
import "my_lib?test"
```

The binding follows the existing import rule from
[packages_vs_globals](../packages_vs_globals/requirements.md) R10.1. It strips
the scope prefix and replaces each character that is not valid in an identifier
with `_`. So `my_lib?test` binds as `my_lib`, `@acme/vec?test` binds as `vec`,
and `basics-comments?test` binds as `basics_comments`. The `?test` flag uses the
binding-shape slot that [03 Imports](../../docs/03_imports.md) reserves for
future flags.

The flag is valid only under these conditions. Each violation is a compile
error.

- The importing file is a `.test.esc` file.
- The specifier names the importing file's own package.

### 5.1 Type of the binding

The binding's type is an object type with one member for every top-level
declaration in the package's `lib/` module, whether or not it is exported.

- A function, class, enum, or `val`/`var` binding becomes a value member with
  the type the lib check inferred for it.
- A type alias or interface becomes a type member, so a test can annotate with
  `my_lib.User` even when `User` is private.
- A namespace becomes a nested object of the same shape. `lib/geo/shapes.esc`
  contributes `my_lib.geo.Point`.

Every value member is writable, and the binding is a `mut` reference to the
object. This lets a test assign a member directly, and it lets the test pass the
object to a TypeScript API that writes to it. Vitest's `vi.spyOn` and Jest's
`jest.spyOn` both replace a property on the object they receive.

```escalier
my_lib.retryLimit = 0              // set a private value
vitest.vi.spyOn(my_lib, "fetchUser")  // spy on a private function
```

An assignment is checked against the member's declared type, so `my_lib.retryLimit
= "zero"` is a type error when `retryLimit` is a `number`. A `val` member is
writable through this binding even though code inside `lib/` cannot reassign
it.

## 6. The `test` build

The `test` build compiles the `lib/` module once more with a different codegen
strategy. It runs no extra type check.

### 6.1 Module object

The build declares one object, called the module object here, and makes it the
build's default export. Codegen names it with a generated identifier that cannot
collide with a source name. The examples below write it as `$m`.

Every top-level declaration is attached to the module object, exported or not.
Namespaces become nested objects on it, matching the shape in section 5.1.

### 6.2 Reference rewriting

Every reference to a top-level symbol inside the lib module reads through the
module object instead of the symbol's local binding. This covers:

- calls and reads, such as `fetchUser(id)` becoming `$m.fetchUser(id)`;
- writes to a top-level `var`, such as `hits = hits + 1` becoming
  `$m.hits = $m.hits + 1`;
- class construction and `instanceof` tests, including those `match` lowers to;
- the `extends` clause of a class;
- references from inside a namespace to a parent namespace's symbol;
- a recursive call inside the function it names.

The declaration itself keeps its local name. Only the uses change. This is the
same pattern the `prod` build already uses to populate a namespace object, as in
`geo.Point = geo__Point`. In the `prod` build, internal references bypass that
object and use `geo__Point` directly. In the `test` build they go through it.

### 6.3 Why a default-exported object

Named exports cannot provide goal 3. An ES module's named exports are live
read-only bindings. A test can import them through a namespace import, but it
cannot reassign them, and a spy installed on a namespace object is rejected or
ignored. Even where a runner patches a namespace object, the module's own
functions call their siblings through local bindings and never see the patch.

A plain object avoids both problems. Its properties are writable, and the
module's code reads every symbol through it, so a replacement takes effect at
every call site.

### 6.4 Example

Given this lib source:

```escalier
// lib/index.esc
var retryLimit = 3

fn fetchUser(id: string) -> User {
    // ...
}

export fn greet(id: string) -> string {
    val user = fetchUser(id)
    return `hello, ${user.name}`
}
```

The `test` build emits:

```js
const $m = {};
let retryLimit = 3;
$m.retryLimit = retryLimit;
function fetchUser(temp1) {
  const id = temp1;
  // ...
}
$m.fetchUser = fetchUser;
function greet(temp2) {
  const id = temp2;
  const user = $m.fetchUser(id);
  return `hello, ${user.name}`;
}
$m.greet = greet;
export default $m;
```

A test against it:

```escalier
// lib/index.test.esc
import "vitest"
import "my_lib?test"

vitest.test("greet reads the user's name", fn () {
    vitest.vi.spyOn(my_lib, "fetchUser").mockReturnValue({id: "1", name: "Ada"})
    vitest.expect(my_lib.greet("1")).toBe("hello, Ada")
})
```

The test file compiles to:

```js
import * as vitest from "vitest";
import my_lib from "./index.js";
vitest.test("greet reads the user's name", () => {
  vitest.vi.spyOn(my_lib, "fetchUser").mockReturnValue({ id: "1", name: "Ada" });
  vitest.expect(my_lib.greet("1")).toBe("hello, Ada");
});
```

### 6.5 Initialization order

A top-level `val` or `var` initializer runs once, when the test build is first
imported. That happens before any test body runs. Mocking a function therefore
does not change a value an initializer already computed from it. A test that
needs a different value sets the member directly, and the rewriting in section
6.2 makes every later read see it.

### 6.6 Constraints on codegen

The `test` build must not bypass the module object. Any codegen step that
inlines a top-level constant, or that calls a top-level function through its
local name, is disabled in this mode.

## 7. Build layout and commands

`escalier build --test` produces the test build and compiles the test files. The
`prod` and `dev` builds ignore `.test.esc` files.

The output mirrors `lib/` under `build/test/`:

```text
build/
  lib/              # prod build, unchanged
  test/
    lib/
      index.js      # test build of the lib module, default export only
      index.d.ts    # type of the default export
      index.test.js
      geo/
        shapes.test.js
```

A compiled test file imports the test build by a relative path to
`build/test/lib/index.js`. This matches how a `bin/` script imports
`../lib/index.js` today.

The workflow for the first version is:

1. Run `escalier build --test`.
2. Point the runner at `build/test/**/*.test.js` and run it.

Source maps map the compiled test files and the test build back to the `.esc`
sources, so runner failures report Escalier line numbers.

`index.d.ts` declares the default export with the binding type from section
5.1. Escalier tests in the same package read that type from the lib check and
do not need the file. It exists so a consumer outside the package can type the
test build, as section 10 describes.

## 8. Open questions

1. **Typing of runner APIs.** `vi.spyOn(obj, "fetchUser")` constrains its key
   argument with mapped and conditional types from Vitest's `.d.ts`. Checking
   that call depends on how much of those types the third-party loader handles.
   See [third_party](../third_party/requirements.md). This needs investigation,
   and it does not need the test harness. A fixture whose `lib/` imports
   `vitest` and calls `vi.spyOn` on a plain object exercises the same check
   through `go test ./cmd/...`, without running any tests.

## 9. Follow-up: runner integration

A later version lets the runner compile `.esc` files on demand, so the separate
build step goes away. For Vitest this is a Vite plugin that does two things:

- It compiles each `.test.esc` file the runner collects.
- It resolves the `?test` import to an in-memory test build of the package.

Jest would get the equivalent transformer. Sections 4 through 6 stay the same.
Only the step that produces the files changes.

## 10. Follow-up: publishing the test build

A published Escalier package could ship its `build/test/lib/` output. Another
Escalier package's tests could then import it and mock the published package's
symbols, private ones included, the same way they mock their own.

This needs two changes to the first version.

- The `?test` flag accepts a dependency's name, not only the importing file's
  own package. The binding's type comes from the published `index.d.ts`.
- The package's own code must read the dependency through the dependency's test
  build when it runs under test, so that a mock reaches it. One way is to
  compile the package's own test build against the dependency's test build.
