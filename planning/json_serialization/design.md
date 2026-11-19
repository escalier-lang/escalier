# JSON serialization: design proposal

Status: proposal. Nothing here is implemented.

## Goal

Let a program turn Escalier data into JSON and back without writing the
conversion by hand. The data in scope:

- primitives and literal types
- arrays and tuples
- object types
- classes
- enums (variant types)

A type is in scope only if none of its fields hold a function. Methods on a
class are allowed, because they live on the prototype and are not data. A
function-typed field is not.

The output must lower to plain JavaScript that `JSON.stringify` and
`JSON.parse` interoperate with, so a TypeScript client reading the same JSON
needs nothing from Escalier.

## How other languages do it

The designs split along two questions. Where does the per-type conversion code
come from, and how does a generic function get hold of the conversion for its
type parameter?

| Language | Opt-in | Code comes from | Sum-type encoding (default) | Generics |
|---|---|---|---|---|
| Rust (serde) | `#[derive(Serialize, Deserialize)]` | Compile-time macro | External tag: `{"RGB": {"r": 1, ...}}`. `#[serde(tag = "type")]` switches to an internal tag. | Trait bounds: `T: Deserialize` |
| Swift (Codable) | `: Codable` conformance | Compiler-synthesized `init(from:)` and `encode(to:)` | External tag keyed by case name, payload keyed by argument label | Protocol constraint: `T: Codable` |
| Kotlin (kotlinx.serialization) | `@Serializable` | Compiler plugin | Sealed class: internal tag `{"type": "RGB", ...}` | `Box.serializer(Int.serializer())`, or `serializer<T>()` with reified types |
| C# (System.Text.Json) | None for classes; `[JsonPolymorphic]` and `[JsonDerivedType]` for hierarchies | Runtime reflection, or a source generator | Internal tag `$type` | Runtime type information |
| Haskell (aeson) | `deriving (Generic)` plus empty instances | Generic deriving | `{"tag": "RGB", "contents": ...}` | Type-class constraint |
| OCaml (ppx_deriving_yojson) | `[@@deriving yojson]` | Preprocessor | `["RGB", 1, 2, 3]` | Explicit function arguments: `box_of_yojson int_of_yojson` |
| Elm | None | Hand-written decoder combinators | Whatever the author writes | Decoders passed as values |
| TypeScript | None built in | Runtime schema libraries (zod, io-ts), runtime reflection (class-transformer with `reflect-metadata`), or compile-time transforms from types (typia) | Discriminated union with a literal `kind` or `type` field, by convention | Schema values passed explicitly |

Four things recur across the languages that work well:

1. The compiler, not runtime reflection, writes the conversion code. Rust,
   Swift, Kotlin, Haskell and OCaml all generate it ahead of time. C# added
   source generators because reflection was slow and broke under trimming.
2. A hand-written conversion slots in wherever a generated one would. Swift
   lets a type implement `Codable` itself, and serde lets a type implement
   `Serialize` itself. Generated code calls either kind the same way.
3. Field-level attributes cover the common mismatches between the in-memory
   type and the wire format: rename a key, skip a field, supply a default.
4. Generics need the conversion for `T` from somewhere. Languages with type
   classes get it implicitly. Kotlin and OCaml, which lack them, pass it as an
   argument.

Escalier has no type classes and erases types at runtime, so point 4 shapes
the design more than anything else. It puts Escalier closer to Kotlin and
OCaml than to Rust or Swift.

## Proposal

### Opt-in with `@json`

`@json` on a class or enum asks the compiler to generate the two conversion
methods described below.

```esc
@json
class Point {
    x: number,
    y: number,
}

@json
enum Shape {
    Circle(center: Point, radius: number),
    Rect(topLeft: Point, size: [number, number]),
    Empty,
}
```

Escalier decorators are compile-time annotations that the compiler reads,
which is how `@js` works today. `@json` follows the same model. It lowers to
generated methods, not to a call to a runtime decorator function.

The name is `@json` rather than serde's format-neutral `@derive(Serialize)`.
JSON is the only format in scope, and the generated methods use the names
`JSON.stringify` already looks for. A second format can get its own decorator
later without disturbing this one.

Object types, arrays, tuples and primitives need no decorator. They have no
declaration to attach one to, and their conversion follows from the type
alone (see [Structural types](#structural-types)).

### The conversion protocol

A class or enum is serializable when it has these two members:

```esc
toJSON(&self) -> JsonValue
static fromJSON(value: JsonValue) -> Self throws DecodeError
```

`@json` generates both. A class may instead write them by hand, and then it
needs no decorator. Generated code calls a hand-written pair the same way it
calls a generated one. That is the escape hatch for anything the derivation
can't express, such as a class with invariants to check or a wire format that
doesn't follow the field layout.

`toJSON` is the method `JSON.stringify` already calls on every object it
visits. Generating it means encoding needs no Escalier runtime support at all.
`JSON.stringify(shape)` produces the right output in plain JavaScript.

`JsonValue` is an ordinary alias in a new `std:json` module:

```esc
type JsonValue =
    | null | boolean | number | string
    | Array<JsonValue>
    | {[key: string]: JsonValue}
```

`DecodeError` is a class in the same module. It records the path to the value
that failed, the expected type, and a rendering of what was found:

```esc
class DecodeError {
    path: Array<string | number>,   // ["shapes", 2, "center", "x"]
    expected: string,               // "number"
    found: string,                  // "\"12\""
}
```

Decoding reports failure through `throws` because Escalier checks a `throws`
clause. A caller that forgets to handle a decode failure gets a diagnostic,
which is what a `Result` would give, without a second error convention.

### The `Serializable` predicate

The checker decides whether a type `T` is serializable:

- `null`, `boolean`, `number`, `string`, and their literal types are.
- `undefined` is, but only as an optional field or `T | undefined` field, where
  it means "key absent".
- `Array<T>` and tuples are when their elements are.
- An object type is when every field is. A function-typed field fails it.
- A class or enum is when it has the `toJSON`/`fromJSON` pair, generated or
  written by hand.
- A union is when its members are, and when a decoder can tell them apart from
  the JSON alone (see [Unions](#unions)).
- Everything else is not: `bigint`, `symbol`, functions, `Map`, `Set`, `Date`,
  and classes without the pair.

`Date` is excluded on purpose. It has a native `toJSON` that yields an ISO
string, but nothing reads one back, and a decode that returns a `string` where
the type says `Date` is the bug this proposal exists to prevent. A program that
needs dates wraps one in a class with a hand-written pair.

`@json` on a class or enum whose fields fail the predicate is an error at the
decorator, naming the field:

```esc
@json
class Job {
    id: string,
    run: fn () -> void,
}
// error: `Job` cannot derive `@json`: field `run` has type `fn () -> void`,
//        which is not serializable
```

`Serializable` is also available as a constraint, `T: Serializable`, which
the checker resolves with the rules above rather than structurally. It is a
checker-resident predicate in the same way `NoInfer` is a checker-resident
type.

### Encoding

`json.stringify` in `std:json` is `JSON.stringify` with a `Serializable`
constraint on its argument:

```esc
import "std:json"

val text = json.stringify(Shape.Circle(Point(0, 0), 5))
// {"type":"Circle","center":{"x":0,"y":0},"radius":5}
```

```esc
declare fn stringify<T: Serializable>(value: &T, indent?: number) -> string
```

Encoding needs no type information at runtime. Plain objects, arrays and
primitives are already JSON. Classes and enum variants carry their own
`toJSON`, and `JSON.stringify` calls it recursively. So a generic function can
encode any `T: Serializable` with nothing passed in.

The argument is an immutable borrow because encoding only reads it.

### Decoding

Decoding needs the type, because `JSON.parse` has no idea whether
`{"x":0,"y":0}` should become a `Point`. `std:json` exports a compiler
intrinsic:

```esc
declare fn parse<T: Serializable>(text: string) -> T throws DecodeError | SyntaxError
declare fn decode<T: Serializable>(value: JsonValue) -> T throws DecodeError
```

At each call the compiler reads the instantiated `T` and emits a decoder for
it in place. `T` must be given explicitly or inferred from an annotation:

```esc
val shapes = json.parse<Array<Shape>>(text)
val p: Point = json.parse(text)
```

The decoder composes the pieces `T` is built from. Classes and enums supply
their `fromJSON`. Structural types get inline checks. The call above lowers to
roughly:

```js
const shapes = __json.decodeArray(JSON.parse(text), (v, path) => Shape.fromJSON(v, path));
```

A `T` that is still a type parameter at the call can't be decoded, because
nothing at runtime says what it stands for:

```esc
fn load<T: Serializable>(text: string) -> T throws DecodeError {
    return json.parse<T>(text)
    // error: cannot build a decoder for type parameter `T`;
    //        take a `Decoder<T>` parameter and call it instead
}
```

The fix is to pass the decoder as a value, which is what Kotlin and OCaml do:

```esc
type Decoder<T> = {
    fromJSON(value: JsonValue) -> T throws DecodeError,
}

fn load<T>(text: string, decoder: Decoder<T>) -> T throws DecodeError | SyntaxError {
    return decoder.fromJSON(json.parse<JsonValue>(text))
}

val p = load(text, Point)                        // a class satisfies `Decoder<Point>`
val xs = load(text, json.decoder<Array<Point>>()) // intrinsic for a structural type
```

A class value satisfies `Decoder<C>` structurally through its static
`fromJSON`, so the common case passes the class by name. `json.decoder<T>()` is
the intrinsic that turns any concrete `T` into a `Decoder<T>` value. Implicit
decoder passing, in the style of Scala's `given`, is left for later. It can be
added on top of this without changing the generated code.

### Classes

The generated `toJSON` writes every instance field into an object, in
declaration order. Static members, getters and methods are not fields and are
left out. Inherited fields come first.

The generated `fromJSON` reads every field, decodes it with the decoder for its
declared type, and assigns it to a fresh instance made with
`Object.create(C.prototype)`. It does not call a constructor. Swift's
synthesized `init(from:)` and serde's derived `Deserialize` both work this way.
A class can have several constructors with arbitrary parameters, so there is no
general way to map JSON keys onto one of them. A class whose constructor
enforces an invariant should write its own `fromJSON` and call the
constructor from it.

A decoded value is fresh, so the caller owns it, and it may bind it `mut`.

```esc
@json
class Point {
    x: number,
    y: number,
    norm(&self) -> number { return Math.hypot(self.x, self.y) },
}
```

lowers to:

```js
export class Point {
  constructor(x, y) { this.x = x; this.y = y; }
  norm() { return Math.hypot(this.x, this.y); }
  toJSON() {
    return { x: this.x, y: this.y };
  }
  static fromJSON(v, path = []) {
    const o = __json.expectObject(v, path, "Point");
    const self = Object.create(Point.prototype);
    self.x = __json.decodeNumber(o.x, [...path, "x"]);
    self.y = __json.decodeNumber(o.y, [...path, "y"]);
    return self;
  }
}
```

`__json` is a set of small helpers in `@escalier/runtime`, the package that
already holds `InvokeCustomMatcherOrThrow`. Each helper throws a `DecodeError`
carrying the path.

`fromJSON` takes an optional second `path` argument so that a nested decoder
reports a full path. A hand-written `fromJSON` may ignore it, and errors from
inside it then report the path up to that class.

Inheritance. Decoding produces exactly the class named in the type. A JSON
value encoded from a subclass of `Base` decodes as a `Base` when the target
type is `Base`, and the subclass's extra fields are dropped. Polymorphic
decoding of a class hierarchy needs a closed set of subclasses to pick from,
which is what `sealed` classes
([#842](https://github.com/escalier-lang/escalier/issues/842)) will provide.
Once they land, `@json` on a sealed base should encode the hierarchy the same
way it encodes an enum.

### Enums

An enum variant is already a JavaScript class (`Color__RGB`), so each variant
gets a generated `toJSON` and the enum namespace gets one `fromJSON` that
dispatches on a tag.

The encoding is an internal tag, Kotlin's default and serde's
`#[serde(tag = "type")]`. The variant name goes in a `"type"` key next to the
payload fields:

```esc
@json
enum Shape {
    Circle(center: Point, radius: number),
    Empty,
}
```

```json
{"type": "Circle", "center": {"x": 0, "y": 0}, "radius": 5}
{"type": "Empty"}
```

The internal tag is the right default for Escalier for three reasons:

- TypeScript code models a sum type as a discriminated union with a literal
  tag field. A TypeScript client can declare
  `type Shape = {type: "Circle", center: Point, radius: number} | {type: "Empty"}`
  and read the JSON with no adapter.
- Every variant parameter in Escalier has a name, so the payload always maps
  onto object keys. Swift needs `_0`, `_1` keys for unlabelled associated values,
  and serde rejects an internal tag on tuple variants. Escalier has neither
  problem.
- A payload-less variant still encodes as an object, so every variant has the
  same JSON kind and the decoder dispatches with one property read.

A variant parameter named `type` collides with the tag. That is an error at the
decorator, and the fix is to choose another tag key:

```esc
@json({tag: "kind"})
enum Token {
    Ident(name: string),
    Keyword(type: string),
}
```

`@json({tag: ...})` is the only enum option in the first version. An
external-tag or untagged option can be added later if interop demands it.

The lowering:

```js
class Shape__Circle {
  // constructor and customMatcher unchanged
  toJSON() {
    return { type: "Circle", center: this.center, radius: this.radius };
  }
}
class Shape__Empty {
  toJSON() { return { type: "Empty" }; }
}
Shape.fromJSON = function (v, path = []) {
  const o = __json.expectObject(v, path, "Shape");
  switch (o.type) {
    case "Circle": {
      const self = Object.create(Shape.Circle.prototype);
      self.center = Point.fromJSON(o.center, [...path, "center"]);
      self.radius = __json.decodeNumber(o.radius, [...path, "radius"]);
      return self;
    }
    case "Empty":
      return Object.create(Shape.Empty.prototype);
    default:
      throw __json.badTag(o.type, ["Circle", "Empty"], path, "Shape");
  }
};
```

`toJSON` returns `this.center` as is rather than calling `this.center.toJSON()`.
`JSON.stringify` makes that call itself when it visits the nested value.

### Generic classes and enums

A generic `fromJSON` takes one decoder per type parameter after the value,
in the order the parameters are declared:

```esc
@json
enum MyOption<T> {
    Some(value: T),
    None,
}
// generated:
// static fromJSON<T>(value: JsonValue, t: Decoder<T>) -> MyOption<T> throws DecodeError
```

`json.parse<MyOption<Point>>(text)` lowers to
`MyOption.fromJSON(JSON.parse(text), Point)`. This is Kotlin's
`Box.serializer(Int.serializer())` with the compiler filling in the arguments.

Encoding a generic value needs nothing extra, for the reason given under
[Encoding](#encoding). The type parameters of a `@json` class or enum get an
implicit `Serializable` bound, so `MyOption<fn () -> void>` is an error where
it is written.

A generic class with a hand-written pair must give `fromJSON` the same
signature, with one `Decoder` per type parameter, so that generated code can
call it.

### Structural types

Object types, arrays, tuples and primitives have no declaration to hang
generated code on, so the decoder for them is emitted inline at each
`json.parse`, `json.decode` or `json.decoder` call. Repeated shapes can be
hoisted into a module-level function as an optimization, without changing
behaviour.

Exactness. Object types are exact by default, and a decoded value must not
carry keys its type lacks. The decoder builds a fresh object from the declared
keys only, so extra keys in the JSON are dropped rather than rejected. This
matches serde's and pydantic's default and lets a newer producer add fields
without breaking an older consumer. An inexact type, written with the `open`
marker, keeps the extra keys, with their values typed `JsonValue`.

Optional fields. For a field `name?: T`, a missing key decodes to an absent
field. An explicit `null` decodes only if `T` admits `null`, because JSON gives
a missing key and a `null` value different meanings.

Tuples decode from arrays of exactly the tuple's length. A longer array is an
error rather than a truncation, because position carries meaning in a tuple
where a key name carries it in an object.

Numbers. `JSON.stringify` writes `NaN` and the infinities as `null`, which then
fails to decode as `number`. Encoding leaves this as is, matching JavaScript.
The decoder reports it as a `DecodeError` at the field, which points at the
cause.

### Unions

A union of structural types is decodable when the decoder can choose a member
from the JSON alone. The rule:

- Members of different JSON kinds are distinguishable: `string | number | null`.
- Literal types of one kind are distinguishable by value: `"a" | "b" | 1`.
- Object types are distinguishable when they share a key whose type is a
  distinct literal in each member. This is the TypeScript discriminated union,
  and the decoder switches on that key, the same way it does for an enum.
- Classes and enums each count as one object member. Two of them in one union,
  or one alongside a structural object type, are ambiguous.

An ambiguous union is an error at the `json.parse` call, naming the two members
that overlap. Trying each member in turn, serde's `#[serde(untagged)]`, is left
out. It makes the chosen member depend on member order and turns a missing
field into a silently different variant.

### Field options

Each of serde, Swift and Kotlin lets a field rename its key, drop out of the
encoding, or take a default when the key is missing. These need decorators on
class fields and variant parameters, which the parser does not accept today.
Decorators are currently decl-level only.

The proposed set:

```esc
@json
class User {
    @json.rename("user_id")
    id: string,
    name: string,
    @json.default([])
    tags: Array<string>,
    @json.skip
    cachedHash: number = 0,
}
```

- `@json.rename(key)` changes the key used in both directions.
- `@json.default(expr)` makes a missing key decode to `expr`. The field still
  encodes.
- `@json.skip` leaves the field out of both directions. The field must have an
  initializer, which `fromJSON` uses.

These are an extension and can land after the core derivation. The core
covers the case where the JSON matches the type, and a hand-written pair covers
the rest until then.

## Implementation outline

1. Add `std:json` with `JsonValue`, `DecodeError`, `Decoder<T>`, and the
   `stringify`, `parse`, `decode` and `decoder` declarations.
2. Add the `__json` decode helpers to `@escalier/runtime`.
3. Allow decorators on `enum` declarations in the parser. They are rejected
   today in [internal/parser/decl.go](../../internal/parser/decl.go). Validate
   `@json` and its argument in the checker.
4. Add the `Serializable` predicate to the solver, and the diagnostic for a
   `@json` type whose fields fail it.
5. Synthesize `toJSON` and `fromJSON` member types on `@json` classes and enum
   namespaces, so that `Point.fromJSON` and `Point: Decoder<Point>` type-check.
6. Emit the generated methods in codegen, and the decoders at the intrinsic call
   sites.
7. Add the union disambiguation rule and its diagnostic.
8. Later: member-level decorators and the field options, then `@json` on
   `sealed` class hierarchies once #842 lands.

Each step lands with fixtures under `fixtures/` that run the generated
JavaScript through a round trip.

## Open questions

- Should a class be able to ask for decoding through a constructor instead of
  `Object.create`? Kotlin requires every serialized property to be a primary
  constructor parameter, which keeps invariants but forbids classes whose
  constructor doesn't line up with the fields. A
  `@json({construct: true})` option could opt in, given a constructor whose
  parameters all name fields.
- Should dropping unknown keys be configurable per type, as Kotlin's
  `ignoreUnknownKeys` is per format? A `@json({strict: true})` option would
  cover security-sensitive inputs.
- Should `bigint` and the planned integer types encode as strings? JSON numbers
  lose precision past 2^53, so a number encoding is unsafe and a string
  encoding needs agreement with the reader.
