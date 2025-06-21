# 00 Types

Escalier supports the same types supported by TypeScript.  This results in very
good interop.

There are some additional types that Escalier provides.  These types will be
converted to the closest TypeScript equivalent when the compiler generates .d.ts
files.  TSDoc comments are included in the output so that an Escalier program
importing the .d.ts files will be able to use the original original types.

## `any`

There are a few different situations where `any` can be considered "safe":
- appearing in type constraints
- appearing in conditionals
- annotation function params in function types and in .d.ts files, function declarations

We can use `never` and `unknown`, but the user has to remember which one to use
in order to get the right behaviour, e.g.

```ts
type IsFunc<T extends (...args: never[]) => unknown> = T extends (...args: never[]) => unknown ? true : false
```

It's easier to allow the use of `any` in this type instead.  It will have the
exact same semantics:

```ts
type IsFunc<T extends (...args: any[]) => any> = T extends (...args: any[]) => any ? true : false
```

All other uses of `any` will be converted to `unknown`, e.g. `JSON`'s `parse`
method will be update like so upon import.

```ts
// lib.es5.d.ts
parse(text: string, reviver?: (this: any, key: string, value: any) => any): any;

// imported
parse(text: string, reviver?: (this: any, key: string, value: any) => any): unknown;
```

NOTE: `any` can be used in type annotations for imported types.

## Exact types

TODO

## Classes

Classes are nominally typed.  Even though the class name isn't part of a class'
structure, it does affect the result of `instanceof` of checks, e.g.

```js
// NOTE: This code block is written in JavaScript
class Foo {}
class Bar {}

const foo = new Foo();

foo instanceof Foo; // true
foo instanceof Bar; // false
```

Because Escalier compiles to JavaScript so its type semantics should reflect the
semantics of its target.  This differs from TypeScript which treats classes as
strictly structural.

## Enums

Escalier enums are implemented as classes with extractors.  This is significantly
different from TypeScript's enums.  When importing enums from .d.ts they will be
modelled in the following way:

```ts
// TypeScript representation
enum MyEnum {
    Foo,
    Bar,
    Baz
}
enum StringEnum {
    MouseUp = "mouseup",
    MouseDown = "mousedown",
    MouseClick = "mouseclick",
}

// Escalier representation
type MyEnum = 0 | 1 | 2
val MyEnum = {
    Foo: 0,
    Bar: 0,
    Baz: 0,
}

type StringEnum = "mouseup" | "mousedown" | "mouseclick"
val StringEnum = {
    MouseUp: "mouseup",
    MouseDown: "mousedown",
    MouseClick: "mouseclick",
}
```

See [Enums](05_enums.md) for information on Escalier enums.

## `Compose` and `Pipe` utility types

These types can be used to compose a sequence of functions.  For interop we can
replace the `Compose<F1, F2, ..., Fn>` type with it's result by expanding it 
before exporting it.

## Regex checked string type

TODO: link to the RFC for this.
