# Pattern Matching Requirements

## Background

GitHub issue [#174](https://github.com/kevinbarabash/escalier/issues/174) identifies two
problems with how `unify` works during pattern matching:

1. **Structural patterns against nominal unions**: Object patterns like `{x, y}` cannot
   match against a union of nominal class instance types like `Point | Event`, because
   unification requires structural patterns to be subtypes of nominal types — which they
   aren't, since nominal types require matching IDs.

2. **Missing validation of match targets against enum constructors**: Matching `Color.RGB`
   (the constructor object) against extractor patterns like `Color.RGB(r, g, b)` does not
   produce an error, even though `Color.RGB` is an object with callable/newable signatures,
   not a `Color` instance.

Both stem from the fact that pattern matching unification uses the same `Unify` path as
general type checking, but pattern matching has different semantics: patterns describe
the *shape* a value might have, not a type that must be assignable to the target.

## Current Behavior

### Problem 1: Structural patterns vs. nominal unions

```ts
class Point(x: number, y: number) { x, y }
class Event(kind: string) { kind }

declare val obj: Point | Event
val result = match obj {
    {x, y} => x + y,   // ERROR: cannot unify {x, y} with Point | Event
    {kind} => kind,     // ERROR: cannot unify {kind} with Point | Event
}
```

**What happens today:** `Unify` is called with the pattern type (a structural open object)
against the target type (`Point | Event`). When trying each union member, the nominal
check at `unify.go:863-873` rejects the structural pattern because it has no matching
nominal ID. Unification fails for all members, so the pattern is rejected.

**Expected behavior:** The structural pattern `{x, y}` should successfully match against
the `Point` member of the union (which has `x` and `y` properties), binding `x` and `y`
to their respective types. The pattern `{kind}` should match the `Event` member.

### Problem 2: Enum constructor used as match target

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match Color.RGB {    // should error: Color.RGB is not a Color instance
    Color.RGB(r, g, b) => r + g + b,
    Color.Hex(code) => code,
}
```

**What happens today:** `Color.RGB` is an object type with static members (including
callable/newable signatures), not a nominal `Color` instance. The extractor pattern
unification ignores the callable/newable signatures and does not check whether the
match target is actually an instance of the enum. No error is reported.

**Expected behavior:** The type checker should report an error because `Color.RGB` (a
constructor/static object) is not assignable to `Color` (a union of nominal instance types).

## Requirements

### R1: Partial matching — patterns need not include all fields

Object patterns should not require listing every field of the type being matched. A
pattern like `{x}` should match any object type that has an `x` property, regardless of
how many other properties that type has. This is important because:

- Objects may have many fields and requiring all of them would be verbose and impractical.
- Patterns should express the shape the developer cares about, not the full structure.

### R2: Structural object patterns must be able to match against nominal types

When a structural (non-nominal) object pattern is unified against a nominal object type
during pattern matching:

- The unifier should check whether the nominal type's properties are compatible with the
  pattern's properties (structurally), rather than requiring matching nominal IDs.
- Pattern bindings should be inferred from the matched nominal type's property types.
- This relaxed check applies only in the pattern-matching context, not in general type
  assignment.

### R3: Structural patterns against unions should match compatible members

When a structural pattern is unified against a union type during pattern matching:

- The unifier should attempt to match the pattern against each union member.
- A match succeeds if the pattern is structurally compatible with at least one member.
- If the pattern matches no member, report an error.

### R4: Binding types for shared fields should be a union of matched members

When a pattern's fields appear in multiple members of a union type, the binding types
should reflect all matching members:

- A pattern matches a union member if the member has all of the fields in the pattern.
- The type of each binding should be the union of that field's type across all matched
  members.
- Members that lack a pattern's field are excluded from the match (and from the union).

For example:
```ts
type FooBarBaz = {kind: "foo", value: string}
               | {kind: "bar", value: number}
               | {kind: "baz", flag: boolean}
declare val fbb: FooBarBaz
match fbb {
    {value} => value,  // matches "foo" and "bar" members, value: string | number
    {flag} => flag,    // matches "baz" member only, flag: boolean
}
```

### R5: Match target type must be validated against pattern expectations

The type of the match target expression must be checked for compatibility with the patterns:

- If patterns are extractor patterns for an enum (e.g. `Color.RGB(...)`, `Color.Hex(...)`),
  the target must be an instance of that enum type, not a constructor or static object.
- The type checker should report an error when the target type is an object with
  callable/newable signatures (a constructor) where an instance type is expected.

### R6: Pattern matching unification must be distinguishable from general unification

The unification logic needs a way to know it is operating in a pattern-matching context:

- Introduce a mechanism (e.g. a flag, a separate mode, or a wrapper function) to indicate
  pattern-matching unification.
- In pattern-matching mode, relax the nominal ID check for structural-vs-nominal
  unification (per R1).
- In non-pattern-matching contexts, preserve existing nominal type checking behavior
  unchanged.

### R7: Existing pattern types must continue to work

The following pattern kinds must remain functional and not regress:

- Literal patterns (`1`, `"hello"`, `true`)
- Identifier patterns (`x`)
- Wildcard patterns (`_`)
- Tuple/array patterns (`[a, b, c]`)
- Object patterns (`{x, y}`)
- Instance patterns (`Point {x, y}`)
- Extractor patterns (`Color.RGB(r, g, b)`) using `Symbol.customMatcher`
- Rest patterns (`...rest`)
- Guard expressions (`{x, y} if x > 0 => ...`)

### R8: Narrowing within match arms

When a structural pattern matches a subset of a union's members:

- The bound variables should have types narrowed to only the matching members.
- For example, matching `{x, y}` against `Point | Event` should bind `x` and `y` with
  types from `Point` only (not from `Event`, which lacks those properties).

## Test Cases

### Case 1: Structural destructuring of a nominal union

```ts
class Point(x: number, y: number) { x, y }
class Event(kind: string) { kind }

declare val obj: Point | Event
val result = match obj {
    {x, y} => x + y,
    {kind} => kind,
}
// result: number | string
```

### Case 2: Enum constructor as match target (should error)

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

val result = match Color.RGB {
    Color.RGB(r, g, b) => r + g + b,
    Color.Hex(code) => code,
}
// ERROR: Color.RGB is not a Color instance
```

### Case 3: Correct enum instance matching (should succeed)

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
    Color.Hex(code) => code,
}
// result: number | string
```

### Case 4: Structural pattern matching no union member (should error)

```ts
class Point(x: number, y: number) { x, y }
class Event(kind: string) { kind }

declare val obj: Point | Event
val result = match obj {
    {foo} => foo,  // ERROR: neither Point nor Event has a 'foo' property
}
```

### Case 5: Mixed nominal and structural patterns

```ts
class Point(x: number, y: number) { x, y }
class Event(kind: string) { kind }

declare val obj: Point | Event
val result = match obj {
    Point {x, y} => x + y,   // instance pattern (nominal)
    {kind} => kind,           // structural pattern
}
// result: number | string
```

### Case 6: Partial match — pattern uses subset of fields

```ts
class User(name: string, age: number, email: string) { name, age, email }

declare val user: User
val result = match user {
    {name} => name,  // only matches 'name', ignores 'age' and 'email'
}
// result: string
```

### Case 7: Shared fields across union members produce union bindings

```ts
type FooBarBaz = {kind: "foo", value: string}
               | {kind: "bar", value: number}
               | {kind: "baz", flag: boolean}

declare val fbb: FooBarBaz
val result = match fbb {
    {value} => value,  // matches "foo" and "bar", value: string | number
    {flag} => flag,    // matches "baz" only, flag: boolean
}
// result: string | number | boolean
```

### Case 8: Shared field with same type across all union members

```ts
type Shape = {kind: "circle", radius: number}
           | {kind: "square", side: number}
           | {kind: "rect", width: number, height: number}

declare val shape: Shape
val result = match shape {
    {kind} => kind,  // all members have 'kind', kind: "circle" | "square" | "rect"
}
// result: "circle" | "square" | "rect"
```

### Case 9: Pattern field not present in any union member (should error)

```ts
type FooBar = {kind: "foo", value: string}
            | {kind: "bar", value: number}

declare val fb: FooBar
val result = match fb {
    {missing} => missing,  // ERROR: no member has a 'missing' field
}
```
