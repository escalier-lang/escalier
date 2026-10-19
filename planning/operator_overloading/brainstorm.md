# Operator overloading: brainstorm

Status: brainstorm. Nothing here is designed or scheduled. The companion
document on [units of measure](../units_of_measure/brainstorm.md) builds on the
ideas here.

The motivating case is linear algebra. A program working with vectors and
matrices wants to write `a + b * c` instead of `a.add(b.scale(c))`, and it wants
`2 * v`, `m * v`, and `m * n` to each pick the right operation.

## What Escalier does today

Operators are ordinary value bindings. `addOperatorBindings` in
[internal/solver/prelude.go](../../internal/solver/prelude.go) binds the name `+`
to the function type `(number, number) -> number`. `inferBinary` in
[internal/solver/infer_operator.go](../../internal/solver/infer_operator.go)
looks the name up, constrains each operand against the parameter in its
position, and returns the signature's return type. `operatorSignature` refuses a
binding that holds more than one scheme, so an operator cannot be an overload set
yet. The prelude comment on `addOperatorBindings` already names overloads as the
milestone where richer operator signatures land.

Several existing facts shape any design.

- `++` is string concatenation and `+` is numeric only. Overloading `+` never has
  to coexist with JavaScript's string `+`.
- Free functions and methods already form overload sets. Resolution runs as a
  phase separate from `constrain`, waits until arguments are "ground enough", and
  falls back to declaration order. A set compiles to one JS function that
  dispatches at runtime with `typeof` and `instanceof` checks. See
  [02 Functions](../../docs/02_functions.md#overloading) and
  [M3 in the milestones doc](../simple_sub/01-milestones.md).
- Inference is usage-driven and every expression has one principal type.
  `fn add(a, b) { return a + b }` infers `fn (a: number, b: number) -> number`
  today, and that must keep working.
- Classes are nominal, and JavaScript's `instanceof` can tell two classes apart.
  A call site that knows a class type knows which method a JS object carries.
- Class bodies accept computed symbol keys, as in `[Symbol.asyncDispose](self)`.
  See [well_known_symbols_as_keys](../well_known_symbols_as_keys/requirements.md).
- Parameters say whether they borrow or consume, and a method receiver is
  borrowed with no marker at the call site.
- `@` is taken by decorators. There is no `**` and no compound assignment such as
  `+=`.

## Prior art

| Language | Where operators are declared | Dispatch | Notes |
|---|---|---|---|
| Rust | `impl Add<Rhs> for T` with an associated `Output` type | Static, by trait resolution | Fixed operator set. Mixed operand types are separate impls. Operands are moved unless the impl is on `&T`, which is a common papercut. |
| Swift | `static func + (lhs:, rhs:)` on a type or extension | Static, by overload resolution | Custom operators with declared precedence groups. Type-checker slowdowns on long operator chains are a known problem. |
| Kotlin | `operator fun plus(other)` member or extension | Static | Fixed set mapped to method names. Extension operators retrofit library types. `a += b` maps to `plusAssign` or to `a = a + b`. |
| C# | `public static T operator +(T a, T b)` | Static | C# 11 added static abstract interface members, so generic math can bound `T : INumber<T>`. |
| Python | `__add__` and the reflected `__radd__` | Dynamic | `__add__` returning `NotImplemented` makes Python try the right operand's `__radd__`. PEP 465 added `@` for matrix multiply because `*` was already elementwise in NumPy. |
| Scala | Any method; `a + b` is `a.+(b)` | Static | Symbolic method names are unrestricted, which invites unreadable DSLs. |
| Haskell | Type classes such as `Num` | Dictionary passing | `Num` bundles `+`, `*`, `abs`, `fromInteger`. Vectors cannot implement it lawfully, a long-standing complaint. |
| Lean 4 | Heterogeneous classes `HAdd α β γ`, `HMul α β γ` | Instance resolution | The result type is an output of the instance, so `Matrix * Vector -> Vector` and `Scalar * Vector -> Vector` both fit. |
| Julia | Methods on `Base.:+` | Multiple dispatch on all operands | `2 * v` and `v * 2` are equally natural. |
| JavaScript | None | | A TC39 operator overloading proposal reached stage 1 in 2019 and is listed as withdrawn. JS has `Symbol.toPrimitive`, which only coerces to a primitive. |

Three lessons carry over.

1. A fixed operator set with fixed precedence is enough. Every mainstream design
   that allowed custom operators regretted some of it.
2. The result type has to be free. Rust's `Output` and Lean's `γ` exist because
   `Matrix * Vector` is a `Vector`.
3. The left operand cannot be the only place to look. `2 * v` has a `number` on
   the left. Python solves this with reflected methods, and Swift, C#, and Julia
   solve it by making the operator a function of both operands.

## Design options

### Option A: operator methods on classes

A class declares an operator with a dedicated member form.

```esc
class Vec2 {
    x: number,
    y: number,
    operator +(&self, rhs: &Vec2) -> Vec2 { return Vec2(self.x + rhs.x, self.y + rhs.y) },
    operator *(&self, k: number) -> Vec2 { return Vec2(self.x * k, self.y * k) },
    operator *(&self, rhs: &Vec2) -> number { return self.x * rhs.x + self.y * rhs.y },
    operator unary -(&self) -> Vec2 { return Vec2(-self.x, -self.y) },
}
```

Two `operator *` arms form a method overload set, so the M5 method-overload
machinery picks between them by the right operand.

The member lowers to a symbol-keyed method. `operator +` becomes `[ops.add]`,
where `ops.add` is a symbol exported from a new `std:ops` package. This choice
does three things.

- TypeScript callers can reach the method as `v[ops.add](w)`, and emitted
  `.d.ts` files describe it with an ordinary computed key.
- A structural bound can name the method, which Option C needs.
- Two packages that both define an `add` method cannot collide with the operator.

The scalar-on-the-left case needs a second form. A `static operator` takes both
operands explicitly and is found on either operand's class:

```esc
class Vec2 {
    ...
    static operator *(k: number, v: &Vec2) -> Vec2 { return v * k },
}
```

This is Swift's and C#'s answer rather than Python's reflected methods. It keeps
dispatch static and avoids a runtime "try the other side" protocol.

### Option B: top-level operator arms

Operators are already value bindings, so a module could add arms to the `+`
overload set the same way it declares any other overload.

```esc
fn (+)(a: &Vec2, b: &Vec2) -> Vec2 { return Vec2(a.x + b.x, a.y + b.y) }
fn (*)(k: number, v: &Vec2) -> Vec2 { return Vec2(k * v.x, k * v.y) }
```

This fits the checker's model most directly. It also retrofits types the program
does not own, such as a `Vector3` imported from three.js, the way Kotlin's
extension operators do.

The cost is coherence, meaning whether every module sees the same answer for
`a + b`. Arms would be visible only where imported, so `a + b` could resolve
differently in two files. A declaration would also need to extend the prelude's
`+` rather than shadow it, which is new scoping behavior. Restricting arms to the
module that declares one of the operand classes removes the incoherence and also
removes the retrofitting benefit. That restriction is Rust's orphan rule.

### Option C: structural operator bounds for generic code

Generic code such as `sum<T>(xs: Array<T>) -> T` needs to say that `T` has a `+`.
Escalier has no traits, but a symbol-keyed method is an ordinary member, so an
inexact object type works as the bound.

```esc
import "std:ops"

type Add<R, O> = {[ops.add](&self, rhs: &R) -> O, ...}

fn sum<T: Add<T, T>>(xs: &Array<T>, zero: T) -> T {
    var acc = zero
    for x in xs { acc = acc + x }
    return acc
}
```

`number` does not carry an `[ops.add]` method, so `sum` over numbers fails this
bound unless the checker treats `number` as satisfying the `ops` bounds by fiat.
If it does, codegen for the generic body cannot emit either a bare JS `+` or a
method call. It has to emit a helper such as `$ops.add(a, b)`, which checks
`typeof a === "number"` and otherwise calls the method. Monomorphic call sites
never pay for that helper.

### Option D: no new syntax, only conventional method names

Kotlin's approach without the `operator` keyword. Any class with a method named
`plus` gets `+`. This is cheap to build, but it captures names by accident and
gives TypeScript callers no signal. It is listed for completeness.

## How resolution and inference would work

The rule for `a + b` extends the existing one rather than replacing it.

1. Type both operands, as `inferBinary` does now.
2. If both operands are ground enough to classify, resolve against the operator's
   overload set. The set is the prelude's `number` arm plus every operator method
   and `static operator` on the operands' classes.
3. If an operand is still a free type variable, choose the `number` arm. This
   keeps `fn add(a, b) { return a + b }` inferring `number` and keeps the
   principal-type story intact.
4. If an operand is a union such as `number | Vec2`, reject it for now. The
   overload machinery can dispatch on unions at runtime, but silently picking
   runtime dispatch for an operator hides a cost the user did not ask for.

Step 3 means a generic function only gets a user `+` when its parameter is
annotated or bounded. That matches how M3 treats ambiguous overload calls.

The result type comes from the chosen arm, so heterogeneous operators fall out
of ordinary overload resolution. `m * v` picks `operator *(&self, v: &Vec3) -> Vec3`
on `Mat3`, and `m * n` picks the `Mat3` arm.

## Codegen

A call site with a resolved arm emits a direct call. For a class method that is
`a[ops.add](b)`. A faster form emits each operator as a module-level function,
such as `Vec2$add(a, b)`, and installs the symbol method as a thin forwarder for
interop. Only generic bodies from Option C need the runtime helper.

`number` arithmetic keeps emitting the bare JS operator, so existing programs
compile to identical output.

## Which operators to allow

- Arithmetic: `+`, `-`, `*`, `/`, `%`, and prefix `-`. These are the core of the
  linear algebra case.
- Comparison: one `operator compare` returning a negative number, zero, or a
  positive number, from which `<`, `<=`, `>`, `>=` derive. This is Kotlin's
  `compareTo` and keeps the four operators consistent.
- Equality: leave `==` and `!=` alone at first. They are typed `(unknown,
  unknown)`, pattern matching relies on their JS meaning, and value equality for
  classes is a larger question than operators.
- Indexing: `m[i, j]` for matrices through `[ops.get]` and `[ops.set]`. This
  needs a parser change, since a comma inside `[]` does not parse as an index
  today.
- Not `&&`, `||`, or `??`. They short-circuit, and an overload would have to take
  a thunk.
- No user-defined operator symbols. The parser's fixed `Precedence` table stays
  fixed.

Two new operators are worth discussing separately. `**` is missing entirely and
matters for both numeric code and units. A matrix-multiply operator would let
`*` stay elementwise, as Python's `@` does. `@` is taken by decorators, so it
would need another spelling.

## Ownership and mutability

Operands should not be consumed. `a + b` on two owned matrices that moved both
into the operator would be a constant annoyance, as it is in Rust. Two rules
avoid that.

- Operator parameters, including the receiver, borrow. A call site borrows each
  operand implicitly, the way a method receiver needs no `&` today.
- The result is owned. The operator allocates a new value.

Large matrices also want in-place updates. Escalier has no `+=`, so this could
wait. If compound assignment arrives, `a += b` could resolve to an
`operator +=(&mut self, rhs: &Mat)` arm when one exists and fall back to
`a = a + b` otherwise, which is Kotlin's rule. The exclusivity checks in
[08 Mutability](../../docs/08_mutability.md) then reject `m += m`.

## Recommendation for a first cut

Option A plus Option C. Class-declared operators that lower to `std:ops` symbol
methods, `static operator` for scalar-on-the-left, the free-variable fallback to
`number`, and structural bounds for generic code. Defer Option B until there is a
concrete retrofitting need, and decide coherence then.

## Open questions

- Should `static operator` arms on the right operand's class be searched when the
  left operand's class also has an applicable arm? Swift searches both and ranks.
  Searching only the left class is simpler but breaks `2 * v`.
- Should operators be allowed on enums and on structural object types, or only on
  classes? Classes give a nominal home for dispatch. A structural type has no
  prototype to carry a method.
- How do operators on imported TypeScript classes surface, given TypeScript
  cannot declare them?
- Is the free-variable fallback surprising when a later annotation would have
  picked a different arm? M3's "defer until ground enough" might apply first.
