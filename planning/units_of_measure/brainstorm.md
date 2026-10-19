# Units of measure: brainstorm

Status: brainstorm. Nothing here is designed or scheduled. It assumes the
operator machinery sketched in
[operator_overloading/brainstorm.md](../operator_overloading/brainstorm.md) only
where it says so.

A unit-checked program rejects `distance + time` at compile time and infers that
`distance / time` is a speed. Scientific code wants this because unit mistakes
are common, silent, and expensive.

Two terms recur below. A **dimension** is the kind of quantity, such as length or
time. A **unit** is a particular scale for a dimension, such as metres or
kilometres for length.

## Prior art

| Approach | Examples | Checked | Runtime cost | Notes |
|---|---|---|---|---|
| Built into the type system, erased | F# | Compile time | None | `float<m/s>`. Unit variables are solved by unification over an abelian group, following Andrew Kennedy's work, so `let sq x = x * x` infers `float<'u> -> float<'u^2>`. Distinct units such as `m` and `km` are distinct types and conversion is explicit. `0.0<_>` asks for an inferred unit. |
| Library on type-level integers | Rust `uom`, Haskell `dimensional`, Boost.Units | Compile time | Usually none | Dimension exponents are type parameters. Error messages expose the encoding. Storage is in base units, so `km` is a constructor, not a type. |
| Library with units as values | C++ mp-units, Julia Unitful.jl | Compile time | None to small | `5 * si::metre` builds a quantity, so no literal syntax is needed. mp-units is proposed for C++29 as P3045. |
| Runtime units | Python pint, Swift `Measurement<UnitLength>` | Runtime | Allocation and checks | Good for parsing user input and display. Errors surface late. |
| Branded numbers | TypeScript `number & {__brand: "m"}` | Compile time, partially | None | Rejects mixing units, but `a * b` cannot compute a new unit, so most arithmetic needs casts. |
| Units native to the language | Frink | Runtime | Varies | Every number carries a dimension. Useful as a model for the conversion table. |

The F# lesson is that units belong in the type of `number` itself, inferred and
erased. The mp-units and Unitful lesson is that a unit can be a value you
multiply by, which avoids new literal syntax. The library lesson is to store
every quantity in its dimension's base unit so conversions are just constants.

## Why erased, built-in units fit Escalier

- Escalier compiles to JavaScript, where a wrapper object per number would cost
  an allocation for every arithmetic result. Erased units cost nothing at runtime
  and emit the same JS as unitless code.
- Escalier has no type-level integers, so the library encoding is unavailable
  without building that first, and it would produce poor diagnostics.
- Escalier already infers types without annotations. Units should be inferred the
  same way, which F# shows is possible.
- Operator overloading is not required. The prelude's arithmetic operators simply
  get unit-generic signatures. This also means units can land before or without
  user-defined operators.

## Sketch

### Declaring units

A `unit` declaration binds two things, the way an enum or class name binds both a
type and a value.

```esc
unit m          // base unit of a new dimension
unit s
unit kg
unit N = kg * m / s^2      // derived unit, an alias for the expression
unit km = 1000 * m         // scaled unit
```

`m` the type is `number<m>`'s unit. `m` the value is the constant `1` with type
`number<m>`. So quantities are built by multiplication and need no literal
syntax.

```esc
val d = 100 * m            // d: number<m>
val t = 9.58 * s           // t: number<s>
val v = d / t              // v: number<m / s>
val bad = d + t            // ERROR: cannot add number<m> and number<s>
```

A scaled unit is a constant in its dimension's base unit. `km` is the value
`1000` typed `number<m>`, not a distinct type. `5 * km` stores `5000`, and
`d / km` reads a length back out in kilometres as a plain `number`. This is the
Unitful and `uom` model. It makes conversion automatic and correct by
construction, at the price of losing the distinction between "stored in metres"
and "stored in kilometres". The alternative is F#'s model, where `km` is its own
type and a conversion factor is written explicitly. That keeps storage exact and
makes mixed-unit code verbose. This is the first decision to make.

Units would be exported and imported like any other declaration. A `std:si`
package would provide the SI base and derived units.

### Literal syntax as an alternative

F# writes `9.58<s>`. In Escalier, `5<m>` collides with `<` as less-than at the
parser level, so it would need lookahead. A suffix such as `9.58_s` collides with
numeric separators. Multiplication by a unit value avoids both problems and is
the suggested default. `x : number<m>` already works as a cast for the rare case
that needs one.

### The type `number<U>`

- `number` with no argument means `number<1>`, the dimensionless number. Every
  existing program keeps its meaning.
- Numeric literals are dimensionless. `5` is still the literal type `5`, a
  subtype of `number<1>`.
- `number<U>` is invariant in `U`. `number<m>` is not a subtype of `number<s>` or
  of `number<1>`. A join of `number<m>` and `number<s>` is an error, not a union.
- A unit expression is a product of unit names and unit variables raised to
  integer powers. The checker normalizes it, so `m * s / s` displays as `m` and
  `m / s / s` displays as `m / s^2`.

### Unit-generic operators

The prelude operators become generic over units.

```esc
// +, -  : <U>(a: number<U>, b: number<U>) -> number<U>
// *     : <U, V>(a: number<U>, b: number<V>) -> number<U * V>
// /     : <U, V>(a: number<U>, b: number<V>) -> number<U / V>
// <, >  : <U>(a: number<U>, b: number<U>) -> boolean
// unary - : <U>(a: number<U>) -> number<U>
```

`**` does not exist yet. If it lands, literal types make a unit-correct version
possible, because the exponent's type is a literal. `x ** 2` with `x: number<m>`
gives `number<m^2>`, and a non-literal exponent requires a dimensionless base.

### Solving unit constraints

The solver records subtyping constraints of the form `lhs <: rhs` as bounds on
type variables. Units do not fit bounds. `number<U> <: number<V>` has to mean
`U = V`, and equations like `U * V = m^2 / s` need algebra, not a lattice.

The proposal is a side solver.

1. Unit variables are a separate kind of variable that never takes bounds.
2. `constrain` on two `number` types emits an equation between their unit
   expressions.
3. The side solver solves those equations by unification over an abelian group,
   the algorithm Kennedy published and F# uses. It is Gaussian elimination over
   integer exponents and always finds a most general solution or reports a
   contradiction.
4. Generalization at `let` and `fn` boundaries quantifies the unit variables
   still free, the same way it quantifies type variables.

So `fn speed(d, t) { return d / t }` infers
`fn <U, V>(d: number<U>, t: number<V>) -> number<U / V>`. A literal pins a unit,
so `fn f(x) { return x + 1 }` still infers `fn (x: number) -> number`. Most
existing code keeps its displayed type, and only arithmetic between variables
becomes unit-generic.

### Writing unit parameters

Lifetimes already use the `'a` sigil, so F#'s `'u` is unavailable. Options:

- A kind on the binder: `fn sq<unit U>(x: number<U>) -> number<U^2>`.
- A bound that names the kind: `<U: Unit>`. This reads as a subtype bound, which
  is what `:` means elsewhere, so it is probably confusing.
- No syntax at first. Unit variables are only inferred, and `number<_>` asks for
  one in an annotation, the way `_` already asks for an inferred type.

The third option is enough to start, and the first can be added when classes need
unit parameters.

### Units on classes

With a unit parameter, a vector carries its unit.

```esc
class Vec3<unit U> {
    x: number<U>,
    y: number<U>,
    z: number<U>,
}

val p = Vec3(1 * m, 2 * m, 3 * m)          // p: Vec3<m>
```

Combined with the operator overloading sketch, `p / t` with `t: number<s>` can
return `Vec3<m / s>` through a unit-generic `operator /`. This is where the two
features meet the linear algebra case.

## Interactions to resolve

- **TypeScript interop.** A `.d.ts` file says `number` and cannot say more. One
  rule is that an imported parameter typed `number` accepts `number<_>` of any
  unit, and an imported return typed `number` is dimensionless. `Math.abs(d)`
  then type-checks and returns a plain `number`. The cost is that `Math.abs`
  drops the unit. Hand-written overrides for `std:math` could restore unit-generic
  signatures for `abs`, `min`, `max`, `floor`, and `sqrt`. The override merge
  mechanism in [interop_mutability](../interop_mutability/override_merge_semantics.md)
  already supports this kind of correction.
- **Emitted declarations.** Units erase to `number` in `.d.ts` output, with the
  Escalier type in the JSDoc field so an Escalier importer recovers it, the same
  approach exact types use.
- **Narrowing.** A runtime `typeof x === "number"` cannot see units. Narrowing an
  `unknown` to `number` must produce `number<1>`, which is unsound when the value
  was a length. F# has the same hole when boxing. Documenting it is probably
  enough, but an "unknown unit" that refuses arithmetic with anything is a
  stricter alternative.
- **Negation and literal types.** `~number` should exclude every `number<U>`, so
  `number<U>` must sit under `number`'s atom in the normal forms the solver
  builds. Literal types are dimensionless, so `5 * m` widens to `number<m>`.
- **Equality.** `==` takes `unknown` operands, so `d == t` type-checks. A lint, or
  a unit-generic `==` on `number` operands, would catch it.
- **Affine units.** Celsius and Fahrenheit have offsets. Adding two temperatures
  is meaningless, but subtracting two gives a temperature difference. Supporting
  this needs a separate "point" type over a "difference" unit, as mp-units does.
  Leaving offset units out of the first cut is reasonable.
- **Angles.** Radians are dimensionless in SI, which lets `sin(5 * m / m)` pass.
  Some libraries treat angle as a dimension of its own. This is a policy choice
  for `std:si`.
- **Display.** Unit expressions need one normalized printed form, for diagnostics
  and for hover in the LSP.

## Recommendation for a first cut

1. `unit` declarations that bind a type and a value, with scaled units stored in
   base units.
2. `number<U>` with `number` meaning dimensionless, invariant in `U`.
3. Unit-generic prelude operators and an abelian-group side solver.
4. Inferred unit variables only, with `number<_>` as the annotation hole.
5. The permissive interop rule for `.d.ts` numbers.

Unit parameters on classes, `**`, affine units, and runtime unit values come
after.

## Open questions

- Store scaled units in base units, or keep `m` and `km` as distinct types?
- Should `bigint` take units too, or only `number`?
- Should a unit be allowed on a literal type, as in `5<m>` being a subtype of
  `number<m>`, so a `val` binding keeps its literal?
- Does inferring unit polymorphism for every variable-only arithmetic function
  make hover output noisier than users will accept?
