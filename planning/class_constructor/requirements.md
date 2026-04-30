# Class Constructors: Explicit `constructor` Blocks

## Motivation

Escalier currently attaches the constructor signature directly to the class
identifier as a "primary constructor":

```ts
class Point(x: number, y: number) {
    x,
    y,
    // ...
}
```

This works for simple value-style classes, but has limitations:

- **Single signature only.** A class can only be constructed one way. Anything
  else has to go through `static` factory methods, which then need to call the
  primary constructor anyway.
- **Initialization is implicit.** Fields are initialized either by matching a
  same-named primary constructor parameter or by a default-value expression.
  There is no place to express conditional initialization, derived initial
  values that depend on more than one parameter, or validation that runs
  before fields are set.
- **Constructor parameters live in a parallel scope** (a closure caught by
  every instance method) that does not appear in the class body. This is hard
  to teach and creates an asymmetry with everything else in the language.
- **The class header carries information that is not the class.** Type
  parameters, mutability modifiers, primary-constructor parameters, and the
  `private` qualifier all stack up before the `{`.

The proposal: drop the primary constructor and require explicit `constructor`
blocks inside the class body, with rules that guarantee every non-optional
field is initialized before `self` becomes observable.

A class is constructed by calling it like a function:
`val p = Point(1, 2)`. Inside the class body, the same call can also be
written as `Self(...)` — `Self` and the class name are interchangeable
at the call site (the Future Work §"Constructor Delegation" extension
gives `Self(...)` an additional meaning when used as the first
statement of a constructor body, but at any other call site the two
forms are equivalent). Every constructor call goes through overload
resolution against the class's constructor set.

## Core Concept

A class declaration consists of a name (and optional type parameters) followed
by a body. The body declares fields, methods, and **one or more `constructor`
blocks**. Constructors look like methods, but:

- They are named with the keyword `constructor`.
- They have no return type — they always return an instance of `Self`. The
  returned instance is immutable by default; the caller opts in to a
  mutable instance with `mut` at the binding site (`val mut p = Point(1, 2)`).
- They take `mut self` as their **first parameter**, just like methods take
  `self`. Writing `mut self` explicitly makes the source of `self` visible
  in the source — symmetric with method signatures. The `mut self`
  parameter is **not** part of the constructor's callable arity: at call
  sites it is implicit (`Point(1, 2)`, not `Point(self, 1, 2)`), and the
  type checker enforces that it must appear, must be the first parameter,
  must be `mut`, and must have no type annotation (its type is always
  `Self`). Omitting it, declaring it as `self` instead of `mut self`, or
  putting it anywhere other than first is a parse error.
- They run a **definite-assignment** check: every non-optional field must be
  initialized before any read of `self` or any call through `self`.
- They may `throw`. A constructor that throws never produces an instance;
  this is useful to express factory-only patterns (e.g. mirroring
  `document.createElement(tag)`).

```ts
class Point {
    x: number,
    y: number,
    color: Color,

    constructor(mut self, x: number, y: number, color: Color = [255, 0, 0]) {
        self.x = x
        self.y = y
        self.color = color
    },

    add(self, other: Self) -> Self {
        return Self(self.x + other.x, self.y + other.y)
    },
}

val p = Point(1, 2)
```

## Field Declarations

Field declarations live in the class body. Only two forms are supported:

| Form         | Must constructor assign? |
|--------------|--------------------------|
| `x: number`  | yes                      |
| `x?: number` | no (implicit `undefined`)|

Notes:
- The bare-identifier shorthand (`x,` to mean "init from a same-named
  parameter") is removed. Constructors must assign explicitly via
  `self.x = x`.
- Optional fields (`x?: T`) start as `undefined` and require no
  initialization. They may be assigned any number of times (including
  on only some control-flow branches); the definite-assignment rule
  does not apply to them.
- Defaults for non-optional fields are expressed as constructor parameter
  defaults (e.g. `constructor(mut self, x: number = 0) { self.x = x }`) rather than
  on the field declaration. This keeps a single place that initializes a
  field and avoids questions about ordering between field-level defaults
  and constructor body statements.

### Body-Element Ordering

Fields, methods, and constructors may appear in any order inside the
class body. The synthesizer (see §"Synthesized Constructor") uses the
declaration order of fields to determine parameter order, but
user-written constructors have no positional constraint relative to the
fields they assign. Convention is fields-first, then constructors, then
methods, but the parser and type checker do not require it.

### `constructor` as an Identifier

`constructor` is a **soft keyword**: it is recognized as the keyword
only at the start of a class element. Everywhere else it remains a
plain identifier. Specifically:

- `obj.constructor` (property access on an arbitrary value) is unchanged
  and reads the property called `constructor`.
- `constructor` is a legal parameter name inside a constructor's
  parameter list (e.g. `constructor(mut self, constructor: string) { ... }`),
  though discouraged for readability.
- `constructor` is a legal local variable name inside any function or
  method body.
- A field named `constructor` is **not** allowed: it would collide with
  the JS prototype property of the same name. Reported as
  `ReservedFieldNameError` at the field declaration.

## Definite-Assignment Rule

Inside a `constructor` body, the checker tracks the set of fields that have
been definitely initialized along every reachable control-flow path. The
following operations are gated by this set:

- `self.foo` (read) — allowed only after `foo` is in the initialized set.
- `self.method(...)` (call) — allowed only after **every non-optional
  field** is in the initialized set.
- `self` passed as an argument or returned — same rule as method call:
  every non-optional field must be initialized first.
- `self.foo = expr` (write) — always allowed; adds `foo` to the initialized
  set for the rest of the path.
- Aliasing `self` (`val r = self`, passing `self` as an argument, returning
  it, or capturing it in a closure) is forbidden until every non-optional
  field has been initialized. Otherwise the alias would bypass the rules
  above.

A constructor is well-formed if, at every exit point, every non-optional
field is in the initialized set.

```ts
class User {
    name: string,
    age: number,
    isActive: boolean,

    constructor(mut self, name: string, age: number, isActive: boolean = true) {
        self.name = name             // OK
        self.age = age               // OK
        self.isActive = isActive     // OK
    },

    constructor(mut self, name: string) {
        self.name = name
        // ERROR: self.age and self.isActive must be initialized before
        // constructor returns
    },

    constructor(mut self, raw: {name: string, age: number}) {
        log(self.name)               // ERROR: self.name is not initialized
        self.name = raw.name
        self.age = raw.age
        self.isActive = true
        log(self.name)               // OK
    },

    greet(self) {
        return `Hi, ${self.name}`
    },
}
```

If a constructor needs derived values that require running code before any
field is assigned (e.g. validation), that code can run as a sequence of
`val` bindings before the assignments — they do not touch `self`.

```ts
class Email {
    local: string,
    domain: string,

    constructor(mut self, raw: string) {
        val parts = raw.split("@")     // OK — does not touch self
        if parts.length != 2 {
            throw Error(`bad email: ${raw}`)
        }
        self.local = parts[0]
        self.domain = parts[1]
    },
}
```

### Branching

The check is flow-sensitive. A field is initialized at a join point only if
it was initialized on every incoming branch.

`if`/`else` and `match` branches are supported. **Loops** (`for`,
`while`, `loop`) are not permitted inside a constructor body in the
initial implementation — the "is the body provably entered at least
once?" question is non-trivial and not worth solving until a real
motivating case appears. Encountering a loop inside a constructor
reports `LoopInConstructorNotSupportedError`. We can relax this later
(e.g. allow loops that provably execute at least once, or require all
required fields to already be initialized before the loop).

```ts
class Range {
    lo: number,
    hi: number,

    constructor(mut self, a: number, b: number) {
        if a < b {
            self.lo = a
            self.hi = b
        } else {
            self.lo = b
            self.hi = a
        }
        // both fields initialized on both branches → OK
    },

    constructor(mut self, a: number, b: number, swap: boolean) {
        if swap {
            self.lo = b
            self.hi = a
        }
        // ERROR: self.lo and self.hi are not initialized on the else branch
    },
}
```

## Synthesized Constructor

If a class declares no `constructor` block, the compiler synthesizes one
whose parameters correspond, in order, to the non-optional instance fields:

```ts
class Point {
    x: number,
    y: number,
}
```

is equivalent to:

```ts
class Point {
    x: number,
    y: number,

    constructor(mut self, x: number, y: number) {
        self.x = x
        self.y = y
    },
}
```

Rules for the synthesis:

- The parameter order is the **declaration order** of the instance fields
  in the class body.
- Only non-optional fields become constructor parameters. Optional fields
  (`x?: T`) are skipped — they start as `undefined`.
- Each parameter has the same name and type as its corresponding field.
- Static fields are not included.
- Computed-key fields cannot be synthesized as parameters; if a class with
  no explicit constructor has any non-optional field whose key is a
  computed expression, synthesis fails and the user must write a
  constructor explicitly.

If the class declares **any** `constructor` block, no synthesis happens —
the user is responsible for ensuring every constructor satisfies the
definite-assignment rule.

```ts
// no synthesis: the user-written constructor is the only one
class User {
    name: string,
    age: number,
    isActive: boolean,

    constructor(mut self, name: string) {
        self.name = name
        self.age = 0
        self.isActive = true
    },
}
```

## Multiple Constructors

A class may declare any number of `constructor` blocks. Each one is a separate
overload at the type level:

```ts
class Vec3 {
    x: number,
    y: number,
    z: number,

    constructor(mut self, x: number, y: number, z: number) {
        self.x = x; self.y = y; self.z = z
    },

    constructor(mut self, xyz: number) {
        self.x = xyz; self.y = xyz; self.z = xyz
    },

    constructor(mut self, p: {x: number, y: number, z: number}) {
        self.x = p.x; self.y = p.y; self.z = p.z
    },
}

val a = Vec3(1, 2, 3)
val b = Vec3(0)
val c = Vec3({x: 1, y: 2, z: 3})
```

### Overload Resolution

Constructor overloads behave exactly like function overloads. There is no
well-defined declaration order across modules, so resolution is *not*
order-sensitive: at a call site the checker collects every constructor
whose parameter list the arguments are assignable to, and:

- If exactly one matches, it is selected.
- If none match, the call is rejected with `NoMatchingConstructorError`.
- If more than one matches, the call is rejected with
  `AmbiguousConstructorError`.

To keep call-site resolution unambiguous, two constructors in the same class
whose parameter lists are mutually assignable (i.e. either could match the
same argument list) are rejected at class-definition time as
`AmbiguousConstructorOverloadsError`. This mirrors the rule for function
overloads.

### Rest Parameters

A constructor may declare a rest parameter as its last formal:
`constructor(mut self, first: T, ...rest: U[])`. Overload resolution treats a rest
constructor as matching any argument count `≥ N - 1` where `N` is the
declared parameter count (excluding the leading `mut self`). For runtime dispatch (see §"Codegen —
Constructor Merging"):

- Rest constructors live in their own dispatch bucket and are tried
  **after** all fixed-arity constructors at the same minimum arity.
- A rest constructor and a fixed-arity constructor with the same minimum
  arity must be runtime-distinguishable on a non-rest parameter, by the
  same rules as same-arity dispatch. Otherwise the class is rejected
  with `ConstructorsNotRuntimeDistinguishableError`.
- Two rest constructors are mutually assignable on overlapping argument
  counts and are rejected at class-definition time as
  `AmbiguousConstructorOverloadsError` unless their leading non-rest
  parameters differ in a runtime-distinguishable way.

### Codegen — Constructor Merging

JavaScript only allows one `constructor`. Escalier merges all declared
constructors into a single JS constructor that dispatches at runtime. The
default dispatch strategy is:

1. **By arity.** Constructors with distinct parameter counts are dispatched
   by `arguments.length`.
2. **By a runtime guard for same-arity overloads.** When two constructors
   have the same arity, the codegen emits a discriminator chosen from the
   first parameter position where the static types disagree on a runtime-
   checkable property:
   - primitive `typeof` (`"number"`, `"string"`, `"boolean"`)
   - `Array.isArray`
   - presence of a discriminating property on an object type
   - `instanceof` for nominal class types

   If no such discriminator exists for a same-arity pair, the class is
   rejected with a "constructors are not runtime-distinguishable" error.

The merged JS form looks like this (for the `Vec3` example above):

Each per-arity bucket emits one positive guard per constructor: every
arm independently checks every condition that picks it. The codegen does
**not** rely on fall-through ordering — this keeps the emitted code
order-independent and makes the generated guards match the dispatch
plan in [implementation_plan.md](./implementation_plan.md) §5.5
(Runtime-Distinguishability Analysis) and §5.7 (Codegen — Merged
Dispatch) directly.

```js
class Vec3 {
    constructor(...args) {
        if (args.length === 3) {
            this.x = args[0]; this.y = args[1]; this.z = args[2];
            return;
        }
        if (args.length === 1 && typeof args[0] === "number") {
            this.x = args[0]; this.y = args[0]; this.z = args[0];
            return;
        }
        if (args.length === 1 && typeof args[0] === "object" && args[0] !== null) {
            this.x = args[0].x; this.y = args[0].y; this.z = args[0].z;
            return;
        }
        throw new TypeError("no matching Vec3 constructor");
    }
}
```

(Open question: if all constructors have distinct arities, the codegen can
emit a `switch (arguments.length)` instead of stacked `if`s. Worth
benchmarking before committing.)

## Generic Classes

Type parameters move to the class header only; constructor parameters move
into the body:

```ts
class Box<T> {
    value: T,

    constructor(mut self, value: T) {
        self.value = value
    },

    get(self) -> T {
        return self.value
    },
}

class Pair<T: number, U: string> {
    first: T,
    second: U,

    constructor(mut self, first: T, second: U) {
        self.first = first
        self.second = second
    },
}
```

A constructor may also introduce its own type parameters when they only
appear in its signature:

```ts
class Wrapper<T> {
    value: T,

    constructor(mut self, value: T) { self.value = value },

    constructor<U>(mut self, other: Wrapper<U>, convert: (U) -> T) {
        self.value = convert(other.value)
    },
}
```

## Throwing Constructors

A constructor may `throw` (the keyword `constructor` allows an optional
`throws` clause; without one the constructor is inferred to throw
whatever its body throws, like any function). Semantics:

- A constructor that throws never produces an instance. Control
  propagates to the caller exactly as if a free function with the same
  signature had thrown.
- The partially-initialized `self` is **not** observable to the caller.
  No reference to `self` may have escaped the constructor body before
  the throw (definite-assignment forbids aliasing `self` until every
  required field is initialized — see §"Definite-Assignment Rule"), so
  there is no way for the caller to acquire a partially-initialized
  instance.
- Throwing constructors are the recommended way to express
  factory-only patterns whose validation cannot be expressed in the
  type system (e.g. mirroring `document.createElement(tag)`).

```ts
class Email {
    local: string,
    domain: string,

    constructor(mut self, raw: string) throws ParseError {
        val parts = raw.split("@")
        if parts.length != 2 {
            throw ParseError(`bad email: ${raw}`)
        }
        self.local = parts[0]
        self.domain = parts[1]
    },
}
```

`try`/`catch`/`finally` is **not** permitted inside a constructor body in
the initial implementation. Definite-assignment across exception edges
(a write in `try` may not be visible in `catch`) is non-trivial and not
worth solving until a real motivating case appears. Encountering `try`
inside a constructor reports `TryInConstructorNotSupportedError`.
Free-function `try`/`catch` around constructor calls is unaffected.

## Async Constructors

Constructors are synchronous. JavaScript does not support async constructors,
and supporting them in Escalier would force every `Foo(...)` call to return a
`Promise<Foo>`, breaking the symmetry between class instantiation and value
construction. Async creation continues to be expressed with a `static async`
factory that calls a (possibly private) sync constructor. This matches the
existing `DBConnection.create` pattern.

## Removal of Primary Constructor

The primary-constructor syntax is removed entirely. Concretely:

- `class Foo(p1, p2) { ... }` is no longer valid.
- The shorthand field form `x,` (meaning "init from same-named primary
  constructor parameter") is removed; use `self.x = x` inside a constructor.
- The closure-captured-parameter behavior — where primary-constructor
  parameters were directly visible inside method bodies — is removed.
  Fields take their place; constructors assign them explicitly.

This is a breaking change. A migration tool should rewrite each existing
class:

```ts
// before
class User(name: string, age: number) {
    name,
    age,
    isActive: true,
    greet(self) { return `Hi, ${self.name}` },
}

// after
class User {
    name: string,
    age: number,
    isActive: boolean,

    constructor(mut self, name: string, age: number, isActive: boolean = true) {
        self.name = name
        self.age = age
        self.isActive = isActive
    },

    greet(self) { return `Hi, ${self.name}` },
}
```

The migration is mechanical when the existing class only uses the primary
constructor for field-init shorthand. Classes that relied on the
closure-captured-parameter behavior need a small refactor to add
corresponding (possibly `private`) fields.

## Lifetimes

Each constructor is lifetime-checked independently, the way an
overloaded free function would be. Concretely:

- Each constructor allocates its own fresh `LifetimeVar`s for its
  reference-typed parameters and stamps them onto its own `FuncType`.
- A field's lifetime is **not** a property of the field declaration; it
  is determined per-constructor by which parameter (or longer-lived
  region) the field is initialized from.
- A reference-typed parameter that is stored into a field must have a
  lifetime at least as long as the resulting instance, by the same
  rule that applies to any reference stored in a struct.
- Two constructors that store references into the same field do not
  share lifetime variables — each ctor's `FuncType` carries its own.
  At a call site, the selected overload determines the instance's
  effective lifetime.
- A reference parameter that is used inside the constructor body but
  not stored in a field has no constraint beyond the body's scope (same
  as any function parameter).

This mirrors how function overloads are lifetime-checked today; nothing
new is introduced at the type-system level.

## Default Mutability

A constructor call always returns an immutable instance. The caller opts
in to a mutable instance with `mut` at the binding site:

```ts
val p     = Point(1, 2)        // immutable Point
val mut q = Point(1, 2)        // mutable Point
```

This rule is uniform across all classes and does not depend on whether the
class has `mut self` methods. Inside a constructor body, `self` is always
treated as `mut Self` regardless; this is required to assign fields during
initialization, and is invisible to callers (the returned instance is
still immutable unless bound with `mut`).

### Removal of the `data` Modifier

Escalier currently supports `data class Foo { ... }` to force an immutable
default for a class that has `mut self` methods. With the new uniform rule
("constructor calls always return immutable; the caller opts in with
`mut`"), the `data` modifier becomes redundant — every class already
behaves the way `data class` did. The modifier is removed alongside the
primary-constructor syntax:

- `data class Foo { ... }` is no longer valid.
- A class with `mut self` methods is constructed immutable like any
  other; callers that want to invoke those methods write
  `val mut p = Point(1, 2)`.

Dropping `data` simplifies the class header and the contextual-keyword
logic in the parser.

## Future Work

### Inheritance and `super`

Subclassing (`class Dog extends Animal { ... }`) and the `super(...)`
call form are deferred. When this lands, the design should be:

When a class extends a base class, every constructor of the subclass must
call `super(...)` exactly once before any read of `self` or any
`self.foo = …` write that targets a base-class field. The `super(...)` call
selects an overload of the base class's constructor using the usual
overload-resolution rules.

```ts
class Animal {
    name: string,

    constructor(mut self, name: string) {
        self.name = name
    },
}

class Dog extends Animal {
    breed: string,

    constructor(mut self, name: string, breed: string) {
        super(name)            // initializes self.name
        self.breed = breed
    },
}
```

Definite-assignment integrates with `super(...)`:

- Before the `super(...)` call, only writes to subclass-declared fields are
  permitted; reads of `self`, calls through `self`, and aliasing `self` are
  forbidden (the base-class portion is not yet initialized).
- After `super(...)` returns, base-class fields are treated as initialized
  for the rest of the constructor body.
- Every reachable exit path of a subclass constructor must have called
  `super(...)` exactly once.
- A `throw` before `super(...)` propagates as usual; no instance escapes.
- A `throw` after `super(...)` returns also propagates; the
  partially-constructed instance is unreferenced and will be collected.
  Escalier does not run base-class destructors on construction failure.

If the base class's constructors are all `private` (see below), the
subclass cannot reach `super(...)` from outside the base's module, and
the `extends` declaration is rejected at class-definition time as
`PrivateBaseConstructorError`.

A subclass that does not declare any constructor synthesizes one
according to the following rules:

1. If the base class has a single constructor and the subclass has no
   non-optional fields of its own, the synthesized constructor takes the
   base constructor's parameters and forwards them via `super(...)`.
2. If the base class has a single constructor and the subclass has
   non-optional fields, the synthesized constructor takes the base
   constructor's parameters **followed by** parameters for the
   subclass's non-optional fields (in declaration order); it calls
   `super(...)` with the leading slice and assigns each remaining
   parameter to its same-named subclass field.
3. If the base class has multiple constructors, no constructor is
   synthesized — the subclass must write its constructors explicitly
   (`SubclassNeedsExplicitConstructorError`). Picking one base overload
   for synthesis would be arbitrary.
4. If every base-class constructor is `private`, the subclass cannot
   extend it externally; that case is rejected at class-definition time.

A subclass with computed-key non-optional fields cannot be synthesized
for the same reason as in §"Synthesized Constructor"
(`ComputedKeyFieldRequiresConstructorError`).

In a subclass constructor, a throw before `super(...)` returns
propagates as usual; a throw after `super(...)` returns also propagates
and the base-class instance is discarded along with the rest. Whatever
finalization the base class needs must be expressed through ordinary
`try`/`finally` or disposal patterns — Escalier does not run base-class
destructors implicitly on construction failure.

### Private Constructors

Replace the existing `class Foo private(...)` syntax with a `private`
modifier on the constructor block:

```ts
class DBConnection {
    conn: SQLConnection,

    private constructor(mut self, conn: SQLConnection) {
        self.conn = conn
    },

    static async create(host: string) -> Promise<DBConnection, Error> {
        val conn = await mysql.createConnection({host})
        return DBConnection(conn)
    },

    [Symbol.asyncDispose](self) -> Promise<void, Error> {
        return self.conn.end()
    },
}
```

A class with at least one non-private constructor is publicly constructible.
A class whose every constructor is `private` can only be constructed from
inside the class body (typically by static factory methods).

### Constructor Delegation

A constructor could delegate to another by calling `Self(...)` — but only as
the first statement of its body, and only if no `self` access has occurred.
Delegation transfers the obligation to initialize fields:

```ts
class Point {
    x: number,
    y: number,

    constructor(mut self, x: number, y: number) {
        self.x = x
        self.y = y
    },

    constructor(mut self, p: {x: number, y: number}) {
        Self(p.x, p.y)               // delegates; self is now fully initialized
        log(`built point at ${self.x},${self.y}`)
    },
}
```

Open question: do we want delegation at all, or do we prefer extracting the
shared logic into a private helper that returns the field values? Delegation
is convenient but adds a non-trivial codegen case. Deferred until the base
proposal is implemented and the ergonomic gap (if any) is clear.

## Open Questions

**Same-arity dispatch.** Same-arity overloads are allowed when the
parameter types differ in a runtime-checkable way (codegen inserts the
discriminator described above). Open question: what should the error
look like when types are not runtime-distinguishable, and is there a
case for letting users supply an explicit discriminator annotation?
