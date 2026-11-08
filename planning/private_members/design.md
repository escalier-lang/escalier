# Private class members

Tracks #2508.

## Goal

A `private` member of an Escalier class is private in two places:

- The checker rejects every access from outside the class that declares it.
- The emitted JavaScript stores it in a JS private name, `#name`. Code outside
  the class cannot read it, list it or write it at runtime either.

TypeScript's `private` is a check-time rule only. At runtime the member is an
ordinary string-keyed property, so `Object.keys`, spread, `JSON.stringify` and a
cast all reach it. Escalier's `private` holds at runtime, so the type the checker
gives an instance describes what the instance actually exposes. In particular,
a private member is never one of the keys `keyof C` lists, and `Object.keys`
never returns it for an instance of `C`.

## Syntax

The parser already accepts `private` on these class members, and the AST records
it:

- fields;
- methods;
- getters and setters;
- constructors.

Each member kind also takes `static`. Nothing after the parser reads the flag
yet, except for `private constructor` on a `declare` class, which #1645 gave a
meaning.

```ts
class Account {
    private balance: number,
    private static count: number = 0,

    constructor(&mut self, opening: number) {
        self.balance = opening
        Account.count = Account.count + 1
    },

    deposit(&mut self, amount: number) {
        self.balance = self.balance + amount
    },

    private audit(&self) -> boolean {
        return self.balance >= 0
    },

    transferTo(&mut self, other: &mut Account, amount: number) {
        self.balance = self.balance - amount
        other.balance = other.balance + amount
    },
}
```

`private` is a class-member modifier only. An object type, an object literal and
an interface reject it.

## The access rule

A private member `m` declared by class `C` can be read, written or called only
from code lexically inside the body of `C`. The body includes:

- `C`'s constructors, methods, getters and setters, both instance and static;
- field initializers;
- functions and closures written inside any of those.

Inside the body, `m` is reachable through any expression whose type is `C` or a
subclass of `C`, not only through `self`. `transferTo` above writes
`other.balance`. This matches JS, where `other.#balance` works for any object
`C`'s constructor initialized.

A subclass body is outside. Escalier has no `protected`, and a subclass reaching
its base's private members would make `private` mean two things. This also
matches JS, where a subclass cannot name its base's `#x`.

A private static member is reached through the class value, `Account.count`, by
the same rule.

### Diagnostics

An access from outside reports that the member is private, not that it is
missing. A reader who sees "has no member `balance`" while the class plainly
declares one wastes time looking for a typo.

```
PrivateMemberAccessError: `balance` is private to class Account and can only be accessed inside its body
```

The checker reports this at every access site, including:

- a read or a write;
- a method call;
- a destructuring pattern that names the member;
- an index with a literal key, such as `acct["balance"]`.

## Types

### The instance type keeps private members

Each member elem in `soltype` gains a `Private` flag: `PropertyElem`,
`MethodElem`, `GetterElem` and `SetterElem`. The class body the registry holds
keeps the private elems, so the checker can type `self.balance` inside the class.
Member lookup takes the site's enclosing class. It returns a private elem only
when that class declared it, and reports `PrivateMemberAccessError` otherwise.

### Private members are not part of the structural surface

Everywhere the checker treats an instance as a set of keys, it leaves private
members out:

- `keyof C`. A non-final class's instance type is inexact, as
  `planning/exact-types/requirements.md` §2.6 sets out, so `keyof C` is an inexact
  union of the declared keys. A private member is not one of the declared keys. A
  `final` class's `keyof` is the exact union of its public keys.
- A mapped type over `C`, and the utility types built on one, such as `Pick`,
  `Omit`, `Partial` and `Readonly`.
- Object spread `{...acct}`. JS spread copies own enumerable string and symbol
  keys, and a private name is neither.
- Assignability of a class instance to an object type. A private member never
  satisfies a member an object type requires. `Account` is not assignable to
  `{balance: number, ...}`, because outside code reading `.balance` on the result
  would get `undefined` at runtime.
- Indexing with a key of type `string`. The result type does not include a
  private member's type.

### Instances stay nominal

Reading `obj.#x` throws a `TypeError` when `obj` was not initialized by the
class that declares `#x`. This is JS's brand check. The checker therefore must
never let a value that is not a real instance reach a type that names the class.
Class instance types are already nominal: `val p: Point = {x: 1}` fails with
`cannot constrain object <: class Point`. This design depends on that staying
true. A future feature that makes an object type assignable to a class type must
exclude classes with private members.

## Code generation

### Fields, methods, getters and setters

A private member `m` is emitted as `#m`, and every access to it inside the class
body is emitted as `.#m`.

A JS private field must be declared in the class body before any code assigns
it. Codegen assigns fields in the constructor. For a private field it also emits
a declaration at the top of the class body:

```js
export class Account {
  #balance;
  static #count = 0;
  constructor(temp1) {
    const opening = temp1;
    this.#balance = opening;
    Account.#count = Account.#count + 1;
  }
  deposit(temp2) {
    const amount = temp2;
    this.#balance = this.#balance + amount;
  }
  #audit() {
    return this.#balance >= 0;
  }
  transferTo(temp3, temp4) {
    const other = temp3;
    const amount = temp4;
    this.#balance = this.#balance - amount;
    other.#balance = other.#balance + amount;
  }
}
```

### Private statics are reached through the class name

A static private member exists only on the class that declares it. In
`static inc() { this.#count++ }`, the call `Sub.inc()` throws, because `this` is
`Sub` and `Sub` has no `#count`. Codegen therefore emits every access to a
private static as `ClassName.#name`, never `this.#name`.

### Keys that have no private-name form

A JS private name must be an identifier. The checker rejects `private` on:

- a computed key, such as `private [Symbol.iterator]`;
- a string-literal key that is not an identifier, such as `private "content-type"`.

```
PrivateMemberKeyError: a private member's name must be an identifier
```

### A private and a public member cannot share a name

`#x` and `x` are distinct at runtime, so JS allows both on one class. Escalier
rejects it. A class where `self.x` and a private `x` name different members would
read badly, and the access rule could only resolve it by where the read happens.
The checker has no duplicate-member check for class bodies yet, only
`DuplicateObjectMemberError` for object type annotations. This work adds one:

```
DuplicateClassMemberError: class Account declares `balance` twice
```

A getter and a setter for the same name are one member and do not trigger it.

### Proxies

A method that reads `this.#x` throws when it is called through a Proxy, because
the Proxy is not the instance. Instances of a class with private members do not
work with libraries that wrap objects in a Proxy, such as Vue's `reactive`,
MobX observables and Immer drafts. This is accepted. A class meant for those
libraries should not declare private members. `docs/` states this next to the
rule.

## Private constructors

JS has no private-name form for a constructor. The checker enforces the rule from
`planning/class_constructor/requirements.md`:

- A class whose every constructor is `private` can be constructed only inside its
  own body, typically by a static factory method.
- Extending it from outside is `PrivateBaseConstructorError`.

At runtime, codegen guards the constructor with a private static flag. Only a
construction site inside the class body sets it:

```js
class DBConnection {
  static #constructing = false;
  constructor(temp1) {
    if (!DBConnection.#constructing) {
      throw new TypeError("DBConnection's constructor is private");
    }
    DBConnection.#constructing = false;
    const conn = temp1;
    this.conn = conn;
  }
  static async create(temp2) {
    const host = temp2;
    const conn = await mysql.createConnection({host});
    const temp3 = conn;
    DBConnection.#constructing = true;
    return new DBConnection(temp3);
  }
}
```

The construction site evaluates the arguments into temporaries before it sets the
flag. Otherwise an argument expression that calls out could construct the class
while the flag is set. The constructor clears the flag before it runs any other
code. A JS subclass of `DBConnection` fails at `super()` for the same reason, so
the runtime matches `PrivateBaseConstructorError`.

A class with at least one public constructor gets no guard. A private overload
next to a public one is then reachable from JS, which the checker cannot see
anyway.

## `.d.ts` output

Codegen emits a class as two declarations: a structural type for the instance
and a value for the constructor.

```ts
declare type Account = {deposit(amount: number): void, ...};
declare const Account: {new (opening: number): Account};
```

A TypeScript caller can pass any object with that shape where an `Account` is
expected, and the first private read on it throws. For a class with at least one
private member, codegen emits a `declare class` instead. It carries a `#private;`
marker, which is how TypeScript records that a class has private names:

```ts
declare class Account {
    #private;
    constructor(opening: number);
    deposit(amount: number): void;
    transferTo(other: Account, amount: number): void;
}
```

TypeScript treats a class with a `#private` marker nominally, so a lookalike
object no longer type-checks against `Account`. Private members themselves are
not listed, because TypeScript callers cannot name them.

A class without private members keeps today's two-declaration form.

## `.d.ts` input

`dts_to_esc` reads TypeScript `declare class`es. TypeScript's `private` is a
check-time rule, so a `private x` there is an ordinary string-keyed property at
runtime.

- **`private` members are kept as private.** The checker rejects access to them
  from Escalier code, and they stay out of `keyof` and assignability, as
  TypeScript's do. `Object.keys` on such an instance does return them. That
  divergence comes from the TypeScript source, and the converter records no
  `#name` for these members, so codegen never emits `.#x` for them.
- **The `#private;` marker** names no member. The converter drops it and marks
  the converted class as having private names, so the class stays nominal.
- **`private constructor()`** keeps the #1645 handling.
- **`protected` members** are an open question below.

## Open questions

1. Should TypeScript `protected` members in `.d.ts` input map to `private`? That
   makes them unreachable from an Escalier subclass of the TS class, which the TS
   author meant to allow. The alternative is to treat them as public.
2. Does the instance type need a separate "has private names" flag, or does the
   presence of any private elem stand in for it? A `.d.ts` class with only a
   `#private;` marker has no private elem to point at.

## Done when

- Each of the four member kinds, in both instance and static form, has a test
  asserting that access from outside the class fails with the full
  `PrivateMemberAccessError` message and access from inside is accepted.
- A subclass body accessing its base's private member fails.
- `keyof`, a mapped type, spread and assignability to an object type each have a
  test showing a private member is excluded. The `keyof` test uses a `final`
  class, so its exact union shows the private key is missing.
- A fixture's emitted JS uses `#name` for every private member and
  `ClassName.#name` for every private static.
- A non-`declare` class with only private constructors can be built from a static
  factory, fails to build from outside in Escalier, and throws when built from JS.
- The emitted `.d.ts` for a class with a private member is a `declare class` with
  a `#private;` marker.
- `docs/` describes the access rule, the runtime representation and the Proxy
  limitation.
