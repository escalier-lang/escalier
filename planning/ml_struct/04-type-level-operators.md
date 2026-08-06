# 04 — Interaction with TypeScript-style type-level operators

How adopting MLstruct interacts with the type-level operators Escalier borrows
from TypeScript and ships in [`../simple_sub/01-milestones.md`](../simple_sub/01-milestones.md)
M9: `keyof`, indexed access `T[K]`, conditional types `T extends U ? X : Y` with
`infer`, mapped types, template literal types, and object/tuple spread.

The short version: **operators are a reduction layer that sits above the subtyping
algebra, so MLstruct is mostly orthogonal to them — with three coupling points,
one a genuine upgrade and one a genuine hazard.**

---

## The key structural fact: operators only ever see *surface* negation

The graft ([03-graft-sketch.md](03-graft-sketch.md) §2, §7) keeps DNF/CNF normal
forms **solver-internal and transient** — they exist only inside a `constrain`
call, never in the `Info` side table or in coalesced output. M9 already reduces
operators *post-coalescing* over ordinary `soltype.Type`. So the operator reducers
never meet a `Conjunct` or `LhsNf`; they meet the same surface union / intersection
nodes they do today, **plus one new surface node, `NegationType`**
([03-graft-sketch.md](03-graft-sketch.md) §1).

That collapses the question from "how do operators interact with Boolean-algebra
normalization" to "how does each operator reduce in the presence of a `¬T` surface
node, and how does it compose with union / intersection distribution." The two
layers stay cleanly separated, exactly as M9 separates operator reduction from
constraint solving.

---

## Coupling point 1 (the upgrade): negation makes the set-difference family total

M9's `Exclude<U, V>` / `Extract` / `NonNullable` / `Omit` are **distributive
conditionals** — they only compute when the operand is a *ground* union, because
TS-style distribution filters concrete members. Native negation expresses them
directly:

- `Exclude<U, V>` is `U & ¬V`,
- `NonNullable<T>` is `T & ¬(null | undefined)`,
- `Omit<T, K>` keys become `keyof T & ¬K`.

The payoff: these become **total on type variables**, not just on ground unions.
`Exclude<U, V>` where `U` is still an inference variable has a representable answer
(`U & ¬V`) instead of getting stuck or over-approximating. That is a real
expressiveness gain over the Simple-sub M9 baseline.

**But it is a design fork with a fidelity cost.** TS's `Exclude` is *defined* as
`T extends U ? never : T` distributed — a structural-assignability filter, not a
set difference. The two diverge in corner cases. Choose per operator whether it
means "TS distributive conditional" or "native `& ¬`," and document it, because
users porting TS code will assume the former.

---

## Coupling point 2 (the hazard): conditional `extends` runs on MLstruct's `<:`

`T extends U ? X : Y` decides its branch by asking the subtyping decider whether
`T <: U`. That decider is now MLstruct's Boolean-algebra relation, which
deliberately diverges from the naive set-theoretic model and has the lossy-union
behavior (see [02-caveats-and-mitigations.md](02-caveats-and-mitigations.md) §4).
Conditional types are precisely where users **observe subtyping reflectively**, so
any place MLstruct's `<:` differs from a TS-faithful `<:` becomes a visible,
surprising conditional-type result. Caveat #4 is not only an internal-precision
concern; it surfaces here as user-facing semantics, and M9 is where it becomes
observable.

### Worked example A — where they diverge (codomains agree)

```ts
type Fn = ((x: number) => boolean) & ((x: string) => boolean)
type Test = Fn extends (x: number | string) => boolean ? "callable" : "not"
```

- **TypeScript → `"not"`.** TS reads `Fn` as an overload table `{ (x: number):
  boolean; (x: string): boolean }`. To decide the `extends`, it asks whether `Fn`
  is callable with a parameter of type `number | string`, tries each signature
  independently — `number | string` is assignable to neither `number` nor
  `string` — and finds no matching overload.
- **MLstruct → `"callable"`.** Set-theoretically, a value that is both
  `number → boolean` and `string → boolean` returns a `boolean` for every input in
  `number | string`, so it genuinely has type `(number | string) => boolean`.
  MLstruct's algebra captures this; the `extends` succeeds.

The branch flips. This is also where `infer` diverges: `F extends (x: infer A) =>
any ? A : never` over `Fn` yields `number` in TS (it picks the last overload's
parameter) but `number | string` under MLstruct's set-theoretic reading. So
`Parameters<Fn>` and `ReturnType<Fn>` change too. The case becomes reachable
exactly under adoption trigger 3 ([00-overview.md](00-overview.md)) — making
inferred intersection-of-arrows first-class — so the feature that motivates
MLstruct is the same feature that flips this branch.

### Worked example B — where they reconverge (codomains conflict)

```ts
type Fn = ((x: number) => boolean) & ((x: string) => null)
type Test = Fn extends (x: number | string) => boolean ? "callable" : "not"
```

Both say **`"not"`**. The set-theoretic argument: take any `f` that is both
`number → boolean` and `string → null`, and feed it a string — a valid `number |
string` input. Because `f` is `string → null`, the result is `null`, which is not
a `boolean`, so `f` does not have type `(number | string) => boolean`. A sound
MLstruct must reject the subtyping, agreeing with TypeScript.

**How a sound system knows not to merge.** A naive reading of the normal form —
"intersected arrows merge to `(A|B) → (C&D)`" — would give `(number | string) →
(boolean & null)` = `(number | string) → never`, and then `… → never <: … →
boolean` succeeds, yielding `"callable"`. That is **unsound**, so MLstruct cannot
be doing that collapse. The actual decision uses the **arrow-decomposition rule**
(the Frisch–Castagna–Benzaken Lemma-6.8 shape the `annoying` / `constrainDNF`
layer implements): to decide `⋂(Aᵢ→Cᵢ) <: (E→F)`, require `E <: ⋃Aᵢ` and, for
every subset `P'` of the overloads, either the input is still covered by the
*other* overloads (`E <: ⋃_{i∉P'} Aᵢ`) or this group's combined codomain fits the
target (`⋂_{i∈P'} Cᵢ <: F`). For the `string` overload:

- Is `number | string <: number` (covered by the remaining `number` overload)? No.
- Is the string overload's codomain `null <: boolean`? No.

Neither disjunct holds, so the subtyping is rejected. The rule checks each
overload's codomain against the target *for the inputs that overload is
responsible for*, instead of blindly intersecting codomains.

### The divergence zone is narrow

MLstruct and TS differ only when the set-theoretic algebra can prove *uniform*
behavior over the queried domain that TS's syntactic overload-matching declines to
synthesize (example A). The moment the overloads' codomains conflict on inputs in
the queried domain (example B), even the set-theoretic reading refuses, and the
two reconverge.

Example A is also where MLstruct's static overload resolution can disagree with
Escalier's *runtime* dispatcher, which routes on concrete `typeof` / `in` tests —
see [05-feature-interactions.md](05-feature-interactions.md) §"Function overloading"
and [06-open-items.md](06-open-items.md) Item 3 for that codegen reconciliation.

### Open verification item

Both worked examples assume MLstruct's *sound* answer, which is derivable. What is
**not** confirmed from the source reading is whether MLscript represents
intersected arrows via a single merged `fun` slot or via the un-merged
decomposition — and those differ exactly when codomains conflict (example B). If
MLscript really merged to `… → (boolean & null)` and used the plain arrow rule, it
would be unsound there. Confirm against `NormalForms.scala` / `ConstraintSolver.scala`
before trusting M9 conditional-type semantics. This is the concrete instance of the
caveat-#4 verification prerequisite.

---

## Coupling point 3: distribution must compose with the algebra

Several operators distribute over unions, and that distribution is already
Boolean-algebra-shaped — MLstruct just adds the negation case:

- `keyof (A | B) = keyof A & keyof B` — `keyof` turns a union into an intersection
  (only common keys are safely accessible). A De Morgan-flavored law the operator
  and the algebra must agree on.
- Indexed access `T[K]` distributes over unions in both `T` and `K`.
- Distributive conditionals distribute over naked-type-parameter unions (M9), the
  same shape.
- Mapped types `{[P in K]: ...}` where `K` is now a Boolean combination of key
  literals — e.g. `keyof T & ¬SomeKeys` from an `Omit` — so the mapped-type driver
  must iterate a key set that is an intersection-with-negation, not a plain literal
  union.

None of these break; the operator reducers need a `NegationType` arm and must
respect the distribution laws, the same way they already handle union distribution.

---

## Where MLstruct is orthogonal or mismatched

- **`infer` matching** is structural unification against function args/returns,
  tuple elements, and the like. It works on surface types, so normalization does
  not touch it — but a scrutinee that surfaces with negated components makes "what
  does `infer R` bind in `T extends Array<infer R> ? …` when `T` has a `¬X` part"
  ill-defined. The rule should match against the positive structural skeleton and
  treat negated members as non-matching. Worked example A is the *positive* face of
  this: `infer` over a merged arrow intersection binds the union domain.
- **Template literal types** are a separate symbolic / string-algebra domain.
  Negation over them is regular-language complement — decidable, but not what
  MLstruct's Boolean algebra computes. So template literals neither benefit from
  nor compose with negation; keep them an orthogonal reduction domain and do not
  try to represent `¬(\`on${K}\`)`.

---

## Decidability and cost

The operator layer is already the dominant cost and termination driver — M9 ships
a cycle cache, depth budget, and the level-2 regularity check (`CheckRegular`)
because conditional / mapped types are near-Turing-complete. MLstruct's NP-hard
subtyping does not change that ordering; operator reduction remains the
budget-governed part. Negation adds exactly one new well-formedness obligation that
folds into the existing regularity machinery: **contractivity** must reject `type T
= ¬T` (an unguarded recursive complement), alongside the `type T = T | T` cases M9
already rejects.

---

## Summary

| Operator family | Interaction with MLstruct |
|---|---|
| `Exclude` / `Extract` / `NonNullable` / `Omit` | **Upgrade** — native `& ¬` makes them total on type variables, not just ground unions. Design fork: TS-distributive vs native set difference. |
| Conditional `T extends U ? …` | **Hazard** — `extends` runs on MLstruct's non-TS-standard `<:`; divergence becomes user-visible (examples A/B). Verify against MLscript. |
| `keyof`, indexed access, mapped types | Compose with union/intersection distribution; need a `NegationType` arm and Boolean key sets. |
| `infer` | Orthogonal to normalization; needs a rule for negated members. Binds differently over merged arrow intersections (example A). |
| Template literal types | Orthogonal; negation is not meaningful in the string domain. |
| Recursion / termination | Operator budget dominates; negation adds the contractivity check for `type T = ¬T`. |
