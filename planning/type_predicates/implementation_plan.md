# Type predicates

Refs #229.

A type predicate is a return type that says what a `true` result proves about an
argument. TypeScript writes it `x is T`, and its assertion form `asserts x is T`
says the function throws unless the argument is a `T`. Escalier has no surface
for either. `dts_to_esc` lowers `x is T` to `boolean` and `asserts x is T` to
`undefined`, which loses the narrowing and leaves 16 stdlib signatures, such as
`filter<S: T>` and `find<S: T>`, with a type parameter nothing can infer.

## Where predicates are used

Inside Escalier, `if val s: string = x` already narrows to a new binding, so a
predicate whose body is a plain type test adds nothing. Predicates are needed for
these cases:

- Interop. Third-party libraries and the TypeScript lib files define many guards
  and assertion functions, such as `Array.isArray` and Node's `assert`. Escalier
  should also emit `x is T` in its `.d.ts` output so TypeScript callers get
  narrowing from Escalier functions.
- Passing a narrowing test as a value. Patterns are not first-class, so
  `xs.filter(isString)` returning `Array<string>` needs a predicate type.
- Checks a pattern cannot express, such as brands (`isEmail(s: string) -> s is
  Email`), refinements (`isNonEmpty(xs) -> xs is [T, ...Array<T>]`) and
  validation logic.

Escalier never re-types an existing binding, so `if isFoo(x) { … }` cannot
narrow `x`. Without predicate patterns, the only way to get a `Foo` binding from
a library guard is a cast. Predicate patterns are therefore part of the interop
work, not an extra.

## Semantics

### One-sided meaning

A predicate `x is S` means a `true` result proves the argument is an `S`. A
`false` result proves nothing. TypeScript also narrows the `false` branch by
excluding `S`, which is unsound whenever the predicate can return `false` for a
value that is an `S`.

The one-sided meaning has three consequences:

- Predicates are covariant. `x is string` is a subtype of `x is string | number`.
- A predicate arm in a `match` is refutable, like a guarded arm, so it does not
  count toward exhaustiveness. A `match` over `string | number` still needs a
  catch-all after `isString(s)`.
- An Escalier-written predicate body only has to justify its `true` returns,
  which makes checking the body tractable (see below).

### Type representation

Add a soltype `PredicateType{ParamIndex, Type, Asserts}`, valid only as a
function's return type. It names the parameter by index, so `fn (x) -> x is S`
and `fn (value) -> value is S` compare equal.

A predicate reads as `boolean` wherever its value is used. Subtyping:

- `x is S` is a subtype of `boolean`.
- `x is S1` is a subtype of `x is S2` when both name the same parameter and
  `S1` is a subtype of `S2`.
- `boolean` is not a subtype of `x is S`.

An assertion, `asserts x is S`, reads as `undefined` and is related only to
other assertions on the same parameter, by the same covariant rule.

### Checking predicate bodies

A predicate defined in Escalier is checked against its signature. Each return
that can yield `true` must occur where the parameter is already known to be an
`S`. A return is proven when its expression is one of the following:

- `false`.
- `true` inside a `match` arm, `if val` or `val … else` whose scrutinee is the
  parameter and whose pattern narrows it to a subtype of `S`.
- A call to another predicate on the parameter whose type is a subtype of `S`.
- `a && b` where either operand is proven, or `a || b` where both are.

```esc
fn isNum(x: unknown) -> x is number {
    return match x {
        _: number => true,  // proven: x is narrowed to number here
        _ => false,
    }
}

fn isPos(x: unknown) -> x is number {
    return x > 0  // error: cannot show x is number when this is true
}
```

The UCS binder records a fact mapping the parameter to its narrowed type in each
arm's scope, reusing what `narrowedBy` in
[internal/solver/ucs_bind.go](../../internal/solver/ucs_bind.go) computes. A
`true` return is checked against those facts.

Restrictions for the first version:

- The parameter must be immutable, so a fact cannot be invalidated by mutation.
- Aliases such as `val y = x` do not carry facts.

Brand, refinement and validation predicates cannot be proven this way. They need
an explicit opt-out, either a `declare fn` or an `unchecked` marker on the
function, so a trusted predicate stays visible in the source. Predicates
imported from `.d.ts` files are trusted, as in TypeScript.

An assertion body is checked the same way, with "returns normally" in place of
"returns `true`". Every normal exit must occur where the parameter is known to
be an `S`, and every other path must throw.

## Predicate patterns

A predicate pattern reuses the extractor syntax. When `Name(...)` resolves to a
function whose return type is a predicate, the pattern tests the scrutinee with
that function. The inner pattern then matches the scrutinee narrowed to its own
type intersected with `S`, so no `S: T` bound is needed.

```esc
declare fn isString(x: unknown) -> x is string
declare fn isPoint(x: unknown) -> x is {x: number, y: number}

match value {
    isString(s) => s.length,   // s: string
    isPoint({x, y}) => x + y,  // nested pattern on the narrowed value
    _ => 0,                    // required: predicate arms are refutable
}

val isString(s) = value else { return }
```

A guard pattern compiles to `if (isString(subject)) { const s = subject; … }`.

An assertion function used as a pattern is irrefutable, since it throws instead
of returning `false`. It needs no `else`:

```esc
declare fn assertIsString(x: unknown) -> asserts x is string

val assertIsString(s) = x  // s: string; throws if x is not a string
```

It compiles to `assertIsString(subject); const s = subject;`.

The extractor machinery already exists: `ExtractorTest` in
[internal/solver/ucs/test.go](../../internal/solver/ucs/test.go), and
`extractorSignature` and `narrowToExtractor` in
[internal/solver/pattern.go](../../internal/solver/pattern.go). The new work is
a branch that tells a predicate function apart from a class value with a
`[Symbol.customMatcher]` method.

`isString(s)` reads like a call. `is` is already a reserved keyword with no
current use, so `s is isString` is the fallback syntax if the extractor form
proves confusing.

## Implementation steps

Each step is roughly one PR.

1. AST, parser and printer. Add `PredicateTypeAnn` for `-> x is T` and
   `-> asserts x is T`. Accept it only in return position and check that the
   parameter name exists in the signature. Print `x is T` in the `.d.ts` output
   in [internal/codegen/dts_soltype.go](../../internal/codegen/dts_soltype.go).
   Done when the parser, printer and codegen snapshots cover both forms and a
   misplaced or misnamed predicate is rejected.
2. Solver types. Convert the annotation to `PredicateType`, add it to the
   soltype visitor, and add the subtyping rules above. Done when an inference
   test asserts that `xs.filter(isString)` resolves `S` to `string` through a
   `declare fn` overload.
3. `dts_to_esc`. Replace the `TODO(#229)` lowering in
   [internal/dts_to_esc/helper.go](../../internal/dts_to_esc/helper.go) with the
   real conversion. For the guard overloads, drop the `S: T` bound and return the
   narrowed type, as the second comment on #229 proposes:

   ```esc
   filter<S>(&self, predicate: fn (value: T, index: number, array: &Self) -> value is S) -> Array<T & S>,
   find<S>(&self, predicate: fn (value: T, index: number, obj: &Self) -> value is S) -> (T & S) | undefined,
   ```

   Done when the regenerated `std/*.esc` files have no unbound `S` on those
   overloads. This removes 16 of the return-only type parameters #1579 has to
   exclude.
4. Predicate patterns. Add a `PredicateTest` to the UCS IR for guards and an
   irrefutable test for assertions. Treat guard tests as refutable in the
   exhaustiveness check, and add codegen. Done when fixtures cover `match`,
   `if val`, `val … else`, nested inner patterns and assertion patterns.
5. Body checking. Record narrowing facts in the UCS binder and check each `true`
   return or normal exit against them. Add the `unchecked` opt-out. Done when
   tests cover each proven form above and the full error message for an
   unproven return.
6. Docs. Describe predicate types in
   [docs/02_functions.md](../../docs/02_functions.md) and predicate patterns in
   [docs/04_pattern_matching.md](../../docs/04_pattern_matching.md).

## Deferred

- `this is T` method predicates keep lowering to `boolean` until there is a
  design for `self is T` patterns.
- Inferring a predicate return type from an unannotated body.
- An opt-in two-sided predicate that also narrows the `false` case, which would
  let predicate arms count toward exhaustiveness.
