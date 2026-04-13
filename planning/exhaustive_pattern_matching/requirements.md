# Exhaustive Pattern Matching Requirements

## Implementation Status

| Requirement | Status | Notes |
|---|---|---|
| R1: Union exhaustiveness | **Implemented** | Enum, nominal, structural, and literal unions |
| R2: Enum variant names in errors | **Implemented** | Uses `TypeRefType.String()` → `Color.Hex` |
| R3: Boolean exhaustiveness | **Implemented** | Expanded to `true \| false` synthetic union |
| R4: Literal union exhaustiveness | **Implemented** | String/number/boolean literal unions |
| R5: Wildcard/identifier catch-all | **Implemented** | Both `_` and `x` recognized as catch-all |
| R6: Guards don't count as coverage | **Implemented** | Guarded branches cover nothing |
| R7: Redundancy detection | **Implemented** | Warnings for duplicate patterns and redundant catch-alls |
| R8: Non-finite types require catch-all | **Implemented** | `number`, `string`, object types |
| R9: Tuple exhaustiveness | **Not started** | Phase 5 |
| R10: Actionable error messages | **Implemented** | Lists all uncovered cases in one error |
| R11: Structured results for LSP | **Implemented** | `ExhaustivenessResult` with `UncoveredTypes` and `RedundantCases` |
| R12: Structural pattern coverage | **Implemented** | Via `MatchedUnionMembers` |
| R13: Nested pattern exhaustiveness | **Not started** | Phase 7 |

## Background

Escalier's pattern matching currently type-checks each branch independently but does not
verify that the branches collectively cover all possible values of the target type. This
means a `match` expression can silently miss cases, leading to potential runtime failures.

The existing implementation already tracks which union members each structural pattern
matches via `MatchedUnionMembers` on `ObjectType`
([types.go](../../internal/type_system/types.go)), and instance/extractor patterns resolve
to specific nominal types. This infrastructure provides the foundation for exhaustiveness
checking.

## Goals

1. **Exhaustiveness checking**: Report an error when a `match` expression's branches do
   not cover all possible values of the target type.
2. **Redundancy detection**: Warn when a branch can never match because all values it would
   match are already covered by earlier branches.

See also the [Future Considerations](#future-considerations) section for LSP code actions
and interface pattern handling.

## Current Behavior

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
    // Missing Color.Hex case -- no error reported today
}
```

**What happens today:** The match expression type-checks successfully. The inferred result
type is `number` (from the single branch). No error is reported for the missing `Hex` case.

**Expected behavior:** The type checker should report an error indicating that the `match`
is not exhaustive: `Color.Hex` is not covered by any branch.

## Requirements

### R1: Exhaustiveness checking for union target types

When the target expression of a `match` has a union type, the type checker must verify
that every member of the union is covered by at least one branch. Coverage is determined by:

- **Extractor patterns** (`Color.RGB(r, g, b)`): Cover the specific union member that the
  extractor resolves to.
- **Instance patterns** (`Point {x, y}`): Cover the specific nominal type named in the
  pattern.
- **Structural object patterns** (`{x, y}`): Cover the union members recorded in the
  pattern's `MatchedUnionMembers` field (populated during unification in Phase 4).
- **Literal patterns** (`"foo"`, `42`, `true`): Cover the specific literal type within the
  union, if the union contains literal types.
- **Wildcard (`_`) and identifier (`x`) patterns**: Cover all remaining uncovered members.
  A match expression with a wildcard or bare identifier arm is always exhaustive (for union
  coverage purposes).

If any union member is not covered by any branch (and no wildcard/identifier catch-all
exists), the type checker should report an error listing the uncovered members.

### R2: Exhaustiveness checking for enum types

Enum types in Escalier desugar to union types of nominal instance types. Exhaustiveness
checking for enums follows from R1, but the error messages should use the enum variant
names (e.g., `Color.Hex`) rather than raw type descriptions for clarity.

### R3: Exhaustiveness checking for boolean target types

When the target type is `boolean` (equivalent to `true | false`), the checker should
verify that both `true` and `false` are covered by literal patterns, or that a
wildcard/identifier catch-all exists.

```ts
declare val b: boolean
val result = match b {
    true => "yes",
    // ERROR: non-exhaustive, missing: false
}
```

### R4: Exhaustiveness checking for literal union types

When the target is a union of literal types (e.g., `"foo" | "bar" | "baz"`), each literal
must be covered by a matching literal pattern or a catch-all.

```ts
type Direction = "north" | "south" | "east" | "west"
declare val dir: Direction
val result = match dir {
    "north" => 0,
    "south" => 180,
    // ERROR: non-exhaustive, missing: "east", "west"
}
```

### R5: Wildcard and identifier patterns make match expressions exhaustive

An **unguarded** branch with a wildcard pattern (`_`) or a bare identifier pattern (`x`)
matches any value. If such a branch exists, the match expression is always considered
exhaustive (for the purposes of union member coverage). This remains true regardless of
where the catch-all appears in the branch list. A wildcard or identifier branch **with** a
guard does not count as a catch-all (see R6).

### R6: Guard expressions do not guarantee coverage

A branch with a guard expression (`{x} if x > 0 => ...`) does not guarantee that it
covers its matched types, because the guard may reject some values at runtime. For
exhaustiveness purposes, guarded branches should be treated as if they do not cover any
types.

```ts
declare val n: number
val result = match n {
    x if x > 0 => "positive",
    x if x < 0 => "negative",
    // ERROR: non-exhaustive (guards don't guarantee coverage)
}
```

A trailing unguarded catch-all is required:

```ts
val result = match n {
    x if x > 0 => "positive",
    x if x < 0 => "negative",
    _ => "zero",  // OK: catch-all covers remaining cases
}
```

### R7: Redundant branch detection

A branch is redundant if all values it could match are already fully covered by earlier
branches. The type checker should report a warning (not an error) for redundant branches.

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
    Color.Hex(code) => code,
    _ => "unreachable",  // WARNING: redundant branch, all cases already covered
}
```

Redundancy detection should also catch duplicate patterns:

```ts
declare val b: boolean
val result = match b {
    true => "yes",
    true => "also yes",  // WARNING: redundant, true already covered
    false => "no",
}
```

### R8: Non-union types require a catch-all

When the target type is not a union of a finite set of types (e.g., `number`, `string`,
or an open object type), the match expression must have a wildcard or identifier catch-all
branch to be considered exhaustive.

```ts
declare val n: number
val result = match n {
    0 => "zero",
    1 => "one",
    // ERROR: non-exhaustive, type 'number' requires a catch-all branch
}
```

### R9: Tuple pattern exhaustiveness

When the target is a tuple type, exhaustiveness should check that all possible combinations
are covered, considering each element position independently. For simple cases (e.g.,
tuples of booleans or literal unions), this should be fully checked. For tuples containing
non-finite types, a catch-all is required.

```ts
declare val pair: [boolean, boolean]
val result = match pair {
    [true, true] => "both",
    [true, false] => "first",
    [false, true] => "second",
    // ERROR: non-exhaustive, missing: [false, false]
}
```

### R10: Error messages should be actionable

Exhaustiveness errors should clearly list the uncovered cases in a human-readable format:

- For enum variants: `Non-exhaustive match: missing cases for Color.Hex`
- For literal unions: `Non-exhaustive match: missing cases for "east", "west"`
- For booleans: `Non-exhaustive match: missing case for false`
- For non-finite types: `Non-exhaustive match: type 'number' is not fully covered; add a catch-all branch`
- For nested partial coverage (R13): `Non-exhaustive match: Result.Ok is not fully covered; add a catch-all branch`
- For nested finite inner types (R13): `Non-exhaustive match: Wrapper.Bool is missing case for false`

When multiple cases are missing, list them all in a single error rather than reporting
one error per missing case.

### R11: Design must not impede future LSP integration

The exhaustiveness checker must produce structured results (e.g., a list of uncovered types
or union members) that can be consumed programmatically — not just formatted error strings.
This ensures a future LSP code action can access the uncovered cases to generate match arm
stubs without re-implementing the exhaustiveness logic.

### R12: Interaction with structural patterns and overlapping coverage

Structural patterns can match multiple union members (see R4 in the pattern matching
requirements). For exhaustiveness checking, each union member must be fully covered by at
least one branch. A structural pattern that matches a subset of members covers those
members.

```ts
type Shape = {kind: "circle", radius: number}
           | {kind: "square", side: number}
           | {kind: "rect", width: number, height: number}

declare val shape: Shape
val result = match shape {
    {radius} => "circle",   // covers circle
    {side} => "square",     // covers square
    // ERROR: non-exhaustive, missing: {kind: "rect", width: number, height: number}
}
```

### R13: Nested pattern exhaustiveness

When multiple branches match the same union member with different nested patterns, the
checker must verify that the nested patterns collectively cover all possible values of the
inner type. A union member is only fully covered when its nested patterns are exhaustive
(or a catch-all exists for that member).

For example, the following should be non-exhaustive because `Result.Ok` is only matched
for literal `0` and `1`, not all `number` values:

```ts
enum Result {
    Ok(value: number),
    Err(message: string),
}

declare val r: Result
val result = match r {
    Result.Ok(0) => "zero",
    Result.Ok(1) => "one",
    // ERROR: non-exhaustive — Result.Ok is not fully covered (missing other number values)
    Result.Err(message) => message,
}
```

## Future Considerations

The following requirements are out of scope for the current work. They are documented here
to inform the design so that the current implementation does not impede future work.

### F1: LSP code action to generate missing match cases

The LSP should provide a code action (quick fix) on non-exhaustive match expressions that
generates stub branches for each uncovered union member. The generated branches should use
the appropriate pattern form:

- For enum variants: extractor patterns (e.g., `Color.Hex(code) => todo`)
- For nominal class types: instance patterns (e.g., `Point {x, y} => todo`)
- For literal types: literal patterns (e.g., `"east" => todo`)
- For non-finite types: a wildcard branch (`_ => todo`)

The generated branch bodies should use a `todo` placeholder (or similar) that produces a
type error to remind the developer to fill in the implementation.

### F2: Interface patterns in exhaustiveness checking

Instance patterns (`InstancePat`) can reference interfaces as well as classes. Unlike
classes, interfaces are not mutually exclusive — a single value may satisfy multiple
interface patterns. This means interface patterns cannot be treated as covering a distinct
union member the way class patterns can.

For the current implementation, exhaustiveness checking only needs to handle class-based
nominal types and enum variants, where each value belongs to exactly one variant/class.
A future iteration should define semantics for interface patterns in exhaustiveness
checking, including:

- Whether an interface pattern can "cover" a union member (e.g., if all members of a union
  implement the interface, does matching on that interface count as exhaustive?).
- How overlapping interface patterns interact with redundancy detection.

## Test Cases

### Case 1: Exhaustive enum match (should succeed)

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
// OK: all variants covered
```

### Case 2: Non-exhaustive enum match (should error)

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
}
// ERROR: non-exhaustive match, missing case for Color.Hex
```

### Case 3: Wildcard makes match exhaustive

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
    _ => "other",
}
// OK: wildcard covers Color.Hex
```

### Case 4: Non-exhaustive boolean match (should error)

```ts
declare val b: boolean
val result = match b {
    true => "yes",
}
// ERROR: non-exhaustive match, missing case for false
```

### Case 5: Exhaustive boolean match (should succeed)

```ts
declare val b: boolean
val result = match b {
    true => "yes",
    false => "no",
}
// OK: both cases covered
```

### Case 6: Non-exhaustive literal union match (should error)

```ts
type Direction = "north" | "south" | "east" | "west"
declare val dir: Direction
val result = match dir {
    "north" => 0,
    "south" => 180,
}
// ERROR: non-exhaustive match, missing cases for "east", "west"
```

### Case 7: Exhaustive structural pattern match (should succeed)

```ts
class Point(x: number, y: number) { x, y }
class Event(kind: string) { kind }

declare val obj: Point | Event
val result = match obj {
    {x, y} => x + y,
    {kind} => kind,
}
// OK: {x, y} covers Point, {kind} covers Event
```

### Case 8: Non-exhaustive structural pattern match (should error)

```ts
type Shape = {kind: "circle", radius: number}
           | {kind: "square", side: number}
           | {kind: "rect", width: number, height: number}

declare val shape: Shape
val result = match shape {
    {radius} => "circle",
    {side} => "square",
}
// ERROR: non-exhaustive match, missing case for {kind: "rect", width: number, height: number}
```

### Case 9: Redundant branch after full coverage (should warn)

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
    Color.Hex(code) => code,
    _ => "unreachable",
}
// WARNING: redundant branch, all cases already covered
```

### Case 10: Guarded branches don't count as coverage (should error)

```ts
declare val n: number
val result = match n {
    x if x > 0 => "positive",
    x if x < 0 => "negative",
}
// ERROR: non-exhaustive match, type 'number' is not fully covered; add a catch-all branch
```

### Case 11: Non-finite type without catch-all (should error)

```ts
declare val s: string
val result = match s {
    "hello" => 1,
    "world" => 2,
}
// ERROR: non-exhaustive match, type 'string' is not fully covered; add a catch-all branch
```

### Case 12: Redundant duplicate pattern (should warn)

```ts
declare val b: boolean
val result = match b {
    true => "yes",
    true => "also yes",
    false => "no",
}
// WARNING: redundant branch, 'true' is already covered
```

### Case 13: Mixed extractor and wildcard

```ts
enum Color {
    RGB(r: number, g: number, b: number),
    Hex(code: string),
}

declare val color: Color
val result = match color {
    Color.RGB(r, g, b) => r + g + b,
    c => c,
}
// OK: identifier 'c' covers remaining cases (Color.Hex)
```

### Case 14: Exhaustive tuple match (should succeed)

```ts
declare val pair: [boolean, boolean]
val result = match pair {
    [true, true] => "both",
    [true, false] => "first only",
    [false, true] => "second only",
    [false, false] => "neither",
}
// OK: all combinations covered
```

### Case 15: Non-exhaustive tuple match (should error)

```ts
declare val pair: [boolean, boolean]
val result = match pair {
    [true, true] => "both",
    [false, false] => "neither",
}
// ERROR: non-exhaustive match, missing cases for [true, false], [false, true]
```

### Case 16: Nested extractor exhaustiveness with catch-all (should succeed)

```ts
enum Result {
    Ok(value: number),
    Err(message: string),
}

declare val r: Result
val result = match r {
    Result.Ok(0) => "zero",
    Result.Ok(n) => `other: ${n}`,
    Result.Err(message) => message,
}
// OK: Result.Ok covered (0 + catch-all), Result.Err covered
```

### Case 17: Nested extractor exhaustiveness without catch-all (should error)

```ts
enum Result {
    Ok(value: number),
    Err(message: string),
}

declare val r: Result
val result = match r {
    Result.Ok(0) => "zero",
    Result.Ok(1) => "one",
    Result.Err(message) => message,
}
// ERROR: non-exhaustive match, Result.Ok is not fully covered; add a catch-all branch
```

### Case 18: Nested literal exhaustiveness for boolean (should succeed)

```ts
enum Wrapper {
    Bool(value: boolean),
    Str(value: string),
}

declare val w: Wrapper
val result = match w {
    Wrapper.Bool(true) => "yes",
    Wrapper.Bool(false) => "no",
    Wrapper.Str(s) => s,
}
// OK: Wrapper.Bool exhaustively covered (true + false), Wrapper.Str covered
```

### Case 19: Nested structural pattern exhaustiveness (should succeed)

```ts
type Shape = {kind: "circle", radius: number}
           | {kind: "square", side: number}

declare val shape: Shape
val result = match shape {
    {kind: "circle", radius} => radius,
    {kind: "square", side} => side,
}
// OK: both members covered; 'radius' and 'side' are catch-all bindings
```

### Case 20: Nested structural pattern without inner catch-all (should error)

```ts
type Shape = {kind: "circle", radius: number}
           | {kind: "square", side: number}

declare val shape: Shape
val result = match shape {
    {kind: "circle", radius: 0} => "point",
    {kind: "circle", radius: 1} => "unit",
    {kind: "square", side} => side,
}
// ERROR: non-exhaustive match, {kind: "circle"} is not fully covered; add a catch-all branch
```
