# Design: Written function signatures

## 1. Scope

This document proposes that a function's signature be written where a reader
needs it, and that a written signature mean exactly what it says. It changes
which programs the checker accepts, how it reads a partly written signature,
and which inference rules in
[planning/ownership_rules/requirements.md](../ownership_rules/requirements.md)
remain. It is the design for
[#2611](https://github.com/escalier-lang/escalier/issues/2611).

The rules here use the terms and rule names of the ownership rules. A reference
such as F3 or D16 names a rule in that document. Rules this document adds are
numbered W1 to W5.

A *signature* is a function's parameter types, its return type, its `throws`
clause, and its lifetime parameters. A signature is *written* when the source
spells it out. A type the source leaves out is *unwritten*, and the checker
fills it in either by *elision*, which follows fixed rules that read only the
signature, or by *inference*, which reads the function's body.

## 2. Problem

The checker infers most of an unwritten signature from the body:

- F3 decides whether an unannotated parameter is owned, `&` or `&mut` from how
  the body uses it, and D9 makes an escaping parameter the body only reads a
  `'static &` borrow.
- The return type is the join of the body's return points, and D16 decides
  whether a returned borrow becomes owned.
- The lifetimes that connect parameters to the result are inferred by
  constraint flow, and L3 decides which of them to render.

Each answer is right, but a reader has to run the checker in their head to get
it. The same body read in two ways gives two signatures:

```esc
var sink = {x: 0}

fn store(p) { sink = p }     // store: fn (p: {x: number}) -> undefined
fn read(p) { return p.x }    // read: fn <T0, 'a>(p: &'a {x: T0}) -> &'a T0
```

A caller of `store` has its argument moved, and a caller of `read` lends it,
and nothing at either call says so.

A partly written signature is harder still. A written parameter type and an
unwritten return type, or a written return type with an unwritten lifetime,
leave the reader unsure which parts the body decides. The ownership extensions
design in [#2430](https://github.com/escalier-lang/escalier/pull/2430) met this
with a promise's lifetime:

```esc
async fn loadBare(pick: fn () -> string) -> Promise<number> {
    val key = pick()
    await tick()
    return key.length
}
```

The written `Promise<number>` can be read as tied to `pick` by elision, or as
`'static` because the body reads `pick` only before it suspends. The two
readings give a caller different rules for the closure it passes. L2 adds a
third reading, since it requires a signature to state exactly the relations
its body needs, so a tie the body does not use would be rejected.

The cost reaches the solver too. All of this runs in `inferFunc` in
[infer_expr.go](../../internal/solver/infer_expr.go), which types module-level
functions, class methods and closures alike, and in the passes it calls after
the body walk:

| Inference | Where |
|---|---|
| F3, an unannotated parameter's ownership | `paramOnlyReadThrough` and its alias scanner in [param_escape.go](../../internal/solver/param_escape.go), about 350 lines |
| D9, an escaping read-only parameter | `noteOpenEscapes` and `markOpenEscapeParams` in param_escape.go |
| Settling a parameter once its flows are known | `registerUnsettledParams`, `settleParams`, `markMovedParams` and `flowMovesParam` in [flow_settle.go](../../internal/solver/flow_settle.go), and the unsettled-parameter flows in `ownership_events.go` and `ownership_events_emit.go` |
| D16, a returned borrow that becomes owned | `shareOutlivingBorrowLifetimes`, `frameOwnedReturn` and `commitOwnedReturns` in [lifetime_frame.go](../../internal/solver/lifetime_frame.go) |
| L3, which lifetimes to render | [lifetime_coalesce.go](../../internal/solver/lifetime_coalesce.go) |

Two facts about this code shape the design:

- No branch in `inferFunc` treats a declaration differently from a closure,
  except a closure passed as a call argument, which skips F3's read-through
  borrow and D9. Requiring signatures on declarations alone removes no code.
  The F3 and D9 machinery goes away only if closure parameters stop being
  inferred from the body too.
- D16 runs on written return types as well. A function written
  `-> {x: number}` that returns `&local` still needs D16 to make the result
  owned. D16 stays under any rule this document considers.

More code is now written by language models. A full signature costs a model
nothing to write, and it lets a model or a reviewer reason about a call from
the signature alone, without re-deriving the callee's body.

## 3. Rules

### 3.1 W1. A written type is complete

A type the source writes is read on its own. Elision fills in any lifetime it
leaves out, by D17 rule 2 and its extension to promises in #2430, with the
change section 5.4 makes for a returned borrow. The body is then checked
against the result. The body never widens or narrows a written
type.

This holds for every function, closures included, and for each part of a
signature on its own. A written parameter type is complete even when the
return type is unwritten.

Under W1, `loadBare`'s written `Promise<number>` is tied to `pick` by elision,
so `loadBare` renders as
`fn <'a>(pick: 'a fn () -> string) -> 'a Promise<number>`. An author who wants
the call-only signature writes `-> 'static Promise<number>`, or leaves the
return type off a closure so that inference decides it.

### 3.2 W2. A module-level function and a method write their full signature

A function declared at module level, exported or not, and a class method write
every parameter type, the return type, and every lifetime parameter that
elision does not supply. A missing piece reports `MissingSignatureError`:

```esc
fn read(p) { return p.x }
// error: 'read' is declared at module level, so its parameters need types; write `fn read<T>(p: &{x: T}) -> &T`
```

The suggested signature in the message comes from today's inference, which
stays available to the language server as a suggestion. The checker verifies
whatever the author writes, so the suggestion need not be sound.

A function that returns nothing writes `-> undefined`. A `throws` clause stays
optional, since a sync function with no clause already declares that it raises
nothing.

### 3.3 W3. A closure parameter needs a type unless its context supplies one

A parameter of a function expression is typed by one of:

- its written annotation;
- the `fn` type the closure flows into, when the closure is a call argument,
  the initializer of an annotated binding, a store into a typed field or
  element, or the return of a function whose return type is written.

A parameter with neither reports `MissingParamTypeError`:

```esc
val f = fn (p) { return p.x }
// error: parameter 'p' has no type, and nothing the closure flows into supplies one; write `p: &{x: number}`

declare fn each(xs: &Array<Flat>, f: fn (x: &Flat) -> undefined) -> undefined
each(&arr, fn (x) { log(x.x) })    // accepted, since each's parameter type supplies x's
```

This is TypeScript's rule under `noImplicitAny`, with Escalier's ownership in
the type. A contextual type always says whether the parameter is owned or
borrowed, so F3 never has to decide it.

### 3.4 W4. A closure's return type and captures stay inferred

A closure's return type, when unwritten, is the join of its return points, and
D16 applies to it. Its captures and its lifetime are inferred by D17 rule 4. A
closure's signature is read where it is written, so a reader does not need it
from a distance the way they need a module-level function's.

A written closure return type follows W1, so a partly written closure has the
same reading as a partly written declaration.

### 3.5 W5. A signature wildcard is allowed only in a closure

`_` in a type, as in `throws _` or `-> Promise<_, _>`, asks the checker to infer
that part. It is a partly written type by design, so it conflicts with W2 in a
declaration. A module-level function or method that writes `_` reports
`MissingSignatureError` with the inferred type in the message. A closure may
still write `_`.

## 4. What changes in the ownership rules

These changes to [requirements.md](../ownership_rules/requirements.md) land with
the PRs in section 7 that make each one true.

- **F3.** The bullet that infers an unannotated parameter's ownership from the
  body is removed, along with `store`, `read` and the alias-equivalence
  examples. Every parameter has a written or contextual type by W2 and W3. The
  bullets for returns, module-level stores and closure captures stay, since
  they decide destinations other than parameters.
- **D9.** It applied only to an unannotated parameter, so it is removed. A
  converter that declares a function from JavaScript writes the parameter's
  type, and I3 still chooses the form that rejects more programs.
- **D13.** Its last paragraph, on inferring `&` or `&mut` for a parameter that
  does not escape, is removed.
- **L2 and D4.** L2's rule that a signature states exactly the relations its
  body needs changes for elided lifetimes. An elided lifetime may tie more than
  the body uses, as `loadBare` does under W1. A bound the author writes out,
  `'a: 'b`, still has to be established by the body, as D4 decides.
- **L3.** It still decides which lifetimes to render in a closure's inferred
  type and in hover. A written signature renders as written.
- **D16.** It stays. It applies to closures and to a written owned return type
  whose body returns a borrow of a local.
- **D17.** Rule 2's elision of a return type changes for a returned borrow,
  as section 5.4 decides. A returned `&T` or `&mut T` takes the one input
  lifetime, or the receiver's, and is an error when neither picks one. A
  returned `fn` type, promise or generator keeps the rule as written. Rule 4's
  closure lifetime inference stays. The open question in its
  rendering section, whether to drop a connected output lifetime that rule 2
  would recover, affects only closures.
- **Comparison with Rust.** The rows for F3, L2, L3 and D4 move toward "Same".
  Rust writes every parameter type and infers lifetimes only inside a body, as
  W2 and W3 do now.
- **Conformance tests.** The per-rule tests for F3's unannotated parameters and
  for D9 are removed. Tests of closure inference replace the ones that need
  inference to stay covered.

The ownership extensions design in #2430 reads a written promise type by W1.
Its A4 inference applies to a closure with no written return type, and to
nothing else.

## 5. Choices

### 5.1 How far to require written types

The options were:

- **Declarations only.** W2 alone, like Rust. Closures keep F3's parameter
  inference. A reader never needs a declaration's body, but `param_escape.go`
  and the parameter settling in `flow_settle.go` stay for closures.
- **Declarations and closure parameters.** W2 and W3, like TypeScript under
  `noImplicitAny` with a required return type on declarations. F3's parameter
  inference and D9 go away entirely, and a closure parameter is typed by its
  context in the common case of a callback.
- **Every signature.** Closures write their return types too. This removes
  little more from the solver than the second option, since D16 and D17 rule 4
  stay, and it adds annotations to every callback.

Recommendation: declarations and closure parameters. It is the only option that
removes solver code, and it costs a closure an annotation only where nothing
supplies its parameter types, as in `val f = fn (p) { … }`.

### 5.2 Whether to require written return types on declarations

TypeScript infers a declaration's return type. Escalier's return type carries
ownership and lifetimes, which are what make the inferred signature hard to
read, so W2 requires it. The survey in #2611 found that a missing return type
is almost the whole migration cost: most of the functions it counted write
their parameters and leave the return off. Many of them return nothing and need
only `-> undefined`.

Recommendation: require it, and let the language server write it.

### 5.3 What a written bare lifetime means

The options for a written type with an elided lifetime were:

- **Elision decides.** W1. The type is complete as written.
- **Inference decides.** The body fills in an elided lifetime in a function
  that has one, and elision applies only to a bodyless declaration.

Recommendation: elision decides. A reader can then read any written type
without the body, and a bodyless declaration and a function with a body mean
the same thing when they write the same signature.

### 5.4 How elision picks a returned borrow's lifetime

D17 rule 2 gives an elided lifetime in a return type a fresh lifetime tied to
every input lifetime. Rust's elision picks one instead:

1. Each elided lifetime in a parameter gets its own lifetime.
2. When there is exactly one input lifetime, every elided output lifetime takes
   it.
3. When there is a `&self` or `&mut self` receiver, every elided output
   lifetime takes the receiver's.

When neither 2 nor 3 applies, Rust rejects the signature. For an `impl Trait`
return, including the future an `async fn` returns, Rust captures every input
lifetime instead, as D17 rule 2 does.

The options were:

- **Tie a returned borrow to every input.** D17 rule 2 as written. Every
  signature means something, and the tie is always sound, since it can only
  keep more borrows alive than the body needs. It over-constrains callers,
  though. `fn pick(a: &Flat, b: &Flat) -> &Flat` leaves the caller's `b`
  borrowed for as long as the result lives, even when the body returns only
  `a`.
- **Pick one input as Rust does, and reject an ambiguous signature.** The
  author says which input the result borrows from. A signature never claims
  more than the author meant, and W1's reading of a written type stays exact.

Recommendation: Rust's rules for a returned borrow, and the tie to every input
for a returned `fn` type, promise or generator. A pending computation or a
closure really can hold any input, which is why Rust captures every lifetime
there too. A return type that holds both kinds, such as
`{f: fn () -> undefined, r: &Flat}`, applies each rule to its own part.

An ambiguous returned borrow reports `AmbiguousReturnLifetimeError`:

```esc
fn pick(a: &Flat, b: &Flat) -> &Flat { return a }
// error: the return type borrows from 'a' or 'b', and elision cannot tell which; write a lifetime, as in `fn pick<'x>(a: &'x Flat, b: &Flat) -> &'x Flat`

fn first(a: &Flat) -> &Flat { return a }
// accepted: fn <'a>(a: &'a Flat) -> &'a Flat, since a is the one input

class Box {
    inner: Flat,
    peek(&self, other: &Flat) -> &Flat { return &self.inner },
    // accepted: the result takes the receiver's lifetime
}
```

The language server's code action from step 1 of the plan writes the lifetime
the body establishes, so the error is fixed by one action.

## 6. Interop

Declarations are already written. The declaration converter in
`internal/dts_to_esc` writes every parameter and return type, and every file
under `internal/interop/data` is `declare`. W1 changes how an elided lifetime
in those declarations reads only where #2430 adds one, on a returned promise.

## 7. Plan

Each step below is a PR. Each runs `UPDATE_SNAPS=true go test ./...` and
`UPDATE_FIXTURES=true go test ./cmd/...` and reviews the fixture deltas.

1. **A code action that writes a signature.** The language server offers to
   write the inferred signature of a function that lacks one. It uses today's
   inference, so it lands before anything is required, and the later steps use
   it to migrate code.
2. **W1 for written types.** A written type is read on its own, and L2 accepts
   an elided lifetime that ties more than the body uses. A returned borrow's
   elided lifetime follows section 5.4, and `AmbiguousReturnLifetimeError`
   reports one elision cannot pick.
3. **W2 as a warning.** `MissingSignatureError` reports as a warning, so the
   migration can proceed file by file.
4. **Migrate fixtures and tests.** The survey for #2611 counted:
   - in `fixtures/`, 55 of 130 module-level functions lacking a full signature,
     all of them missing a return type and 5 of them a parameter type, and 17
     of 37 class methods missing a return type;
   - in Escalier source inside `internal/solver/*_test.go`, about 2,500 of
     about 3,750 module-level functions, about 2,500 of them missing a return
     type and about 200 a parameter type.

   Tests that exist to cover signature inference become tests of closure
   inference rather than being annotated.
5. **W2 as an error, and pre-binding.** The warning becomes an error. Every
   module-level function is then bound from its signature before any body is
   checked, as `annotatedOverloadArms` in
   [module.go](../../internal/solver/module.go) already does for overload arms.
   Function bodies no longer have to be checked in dependency order, and the
   hazard `seedStubReturn` in [infer_class.go](../../internal/solver/infer_class.go)
   guards against, an unannotated method return fixed at `never` by an early
   reader, goes away.
6. **W3, and removal of parameter inference.** Closure parameters with no
   context report `MissingParamTypeError`. F3's parameter inference, D9, and
   the parameter settling in `flow_settle.go` are removed.
7. **W5.** `_` in a declaration's signature is rejected.
8. **The rules document.** `requirements.md` and `docs/09_ownership.md` change
   as section 4 lists.

Steps 1 and 2 need nothing else. Step 3 follows step 1. Steps 5 to 7 follow
step 4.

## 8. Open questions

1. **Whether local named functions follow W2.** A `fn` declared inside a body is
   read like a module-level function, but is visible only there. Options: treat
   it as a declaration under W2, or as a closure under W3 and W4.
   Recommendation: a declaration, since the name invites calls from a distance
   within the body.
2. **Whether to require a `throws` clause where a function raises.** A sync
   function with no clause already declares that it raises nothing, so the
   checker reports a body that raises. An async function's rejection type is
   inferred when its return type omits it. Recommendation: treat
   `-> Promise<T>` as `Promise<T, never>` on a declaration, so that its
   rejection type is written too.
3. **Whether script top-level functions follow W2.** A script is often short and
   exploratory. Recommendation: yes, for one rule everywhere, with the code
   action from step 1 to write the signatures.
