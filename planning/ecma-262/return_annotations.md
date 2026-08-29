# Return annotations from the `returns` fact

§8.2 of [implementation_plan.md](implementation_plan.md) seeds the `&` and
lifetime annotations of the override layer from FR4's `returns` fact. This file
is that seed written out. It gives each member of the alias lattice the
annotation it stands for, names the builtins that carry it, and separates the
rows a curator has to act on from the rows the compiler already reaches on its
own.

The fact is review input rather than an annotator. Annotations are re-applied
when the `.esc` is generated, so a curator edits the override layer and never
the generated file. The checker's lifetime inference and elision rules,
[../lifetimes/requirements.md](../lifetimes/requirements.md), and the borrow
model, [../affine_semantics/requirements.md](../affine_semantics/requirements.md),
stay the mechanism.

**Notation.** `&self`, `&mut self`, and `-> &mut Self` are the shorthand these
planning docs use for a borrowed receiver and for a return that borrows it. The
declaration a curator writes spells the same relationship with an explicit
lifetime parameter:

```esc
declare fn Array.fill<'a, T>(self: mut 'a Array<T>, value: T) -> mut 'a Array<T>
```

The lifetimes requirements define that form under
[Lifetime Annotations](../lifetimes/requirements.md).

## The mapping

| `returns` | What the return hands back | Annotation | Builtins |
| --- | --- | --- | --- |
| `receiver` | the receiver itself | a borrow of the receiver, carrying the receiver's lifetime and its mutability | 15 |
| `param:<n>` | the declared parameter at 0-based position `n` | a borrow tied to that parameter's lifetime | 6 |
| `fresh` | a value the algorithm allocated, or a primitive it computed | an owned return, no lifetime | 232 |
| `union` | a different value on different paths | a lifetime union over the members the fact names | 1 |
| `unknown` | a value the walk could not tie to anything the caller holds | none the fact can seed | 247 |

The counts are over the 501 builtins of the pinned graph, read from the merged
facts of §4.4.

Three things a `returns` value leaves open, which is why no row is applied
without review:

- **Whether a lifetime applies at all depends on the return type.** A
  value-typed return has nothing to bound. `Array.prototype.indexOf` publishes
  `fresh` and declares a number, so the row is moot there. The type arrives at
  the §5 join, not from the shape-free fact.
- **The borrow's mutability comes from the receiver or the parameter it
  borrows**, not from the alias kind.
- **A union's lifetime variables have to be named** in the declaration. The
  `union` section below says which members get one.

## `receiver`

Fifteen builtins hand back the value they were called on. Twelve of them mutate
it first, and those are the fluent chains:

```
Array.prototype.copyWithin   Array.prototype.fill   Array.prototype.reverse   Array.prototype.sort
TypedArray.prototype.copyWithin   TypedArray.prototype.fill   TypedArray.prototype.reverse   TypedArray.prototype.sort
Map.prototype.set   Set.prototype.add   WeakMap.prototype.set   WeakSet.prototype.add
```

Each already takes `&mut self`, so each returns `-> &mut Self`:

```esc
declare fn Array.fill<'a, T>(self: mut 'a Array<T>, value: T) -> mut 'a Array<T>
declare fn Array.sort<'a, T>(self: mut 'a Array<T>, cmp?: fn(T, T) -> number) -> mut 'a Array<T>
declare fn Array.reverse<'a, T>(self: mut 'a Array<T>) -> mut 'a Array<T>
declare fn Map.set<'a, K, V>(self: mut 'a Map<K, V>, key: K, value: V) -> mut 'a Map<K, V>
```

The return-borrow carrying the receiver's mutability is what keeps a chain's
mutability constant. Every link takes `&mut self` and hands back `&mut Self`,
so `arr.fill(0).reverse()` stays `mut` from end to end. A fixed `-> &Self`
would break the second call.

The remaining three return the receiver without mutating it, and they are the
open case of the section below.

## `param:<n>`

Six builtins hand back a declared parameter, all of them `Object` statics and
all of them position 0:

```
Object.assign   Object.defineProperty   Object.freeze
Object.preventExtensions   Object.seal   Object.setPrototypeOf
```

The return borrows that parameter's lifetime:

```esc
declare fn Object.seal<'a, T>(o: 'a T) -> 'a T
```

A static has no receiver, so nothing competes for the return's lifetime and one
lifetime parameter is enough.

## `union`

One builtin publishes a union. `Object` returns an `OrdinaryObjectCreate`
result on one path and `ToObject(value)` on another, so its fact is
`union(fresh, param(0))`.

A union's members are the values the algorithm hands back on its several paths.
The annotation has to cover every one of them, so the return borrows all of
them at once and the lifetime is the union of the members' lifetimes.

`fresh` is the identity of that union rather than a member of it. An owned
value has no lifetime to bound, so a caller holding it constrains nothing, and
dropping `fresh` from the set leaves the same annotation the remaining members
already require. `Object` therefore annotates as the borrow of `value` alone:

```esc
declare fn Object<'a, T>(value?: 'a T) -> 'a T
```

A caller that keeps the returned object alive keeps the argument alive on the
path that hands it back. The types there come from the §5 join; the lifetime is
what the fact contributes.

A union of two input origins instead names both, and neither drops out.
Escalier writes that with the parenthesized form, `('a | 'b) T`.

## `fresh` and `unknown`

`fresh` is an owned return and needs no annotation. `Array.prototype.slice`
allocates the array it returns, and a caller may keep it after the receiver is
gone.

`unknown` is the lattice top. The walk read the returns and could tie none of
them to a value the caller holds, so the fact seeds nothing. Two later stages
narrow the 247:

- The §5 join settles one as owned when the overload declares a primitive
  return type, since a primitive cannot alias whatever the algorithm did.
  `SignatureFact.ReturnOwned` carries that answer.
- §4.4 curates one when a reviewer can read the return type the graph does not
  carry. `Date.prototype.getTime`, `Date.prototype.valueOf`, and
  `get TypedArray.prototype [ @@toStringTag ]` are curated `fresh`, because a
  primitive read out of a slot is a copy.

What is left after both is a return with no seed, and the curator writes the
annotation from the declaration and the spec text.

## Which rows a curator has to write

Per §7, the generated `.esc` omits a lifetime annotation unless the override
layer supplies one, and the result is then checked as ordinary Escalier source.
So what a missing annotation costs is decided by lifetime elision, the rules
that fill the lifetimes of a body-less declaration.
[../../internal/checker/elision.go](../../internal/checker/elision.go) is all of
it, and it does two things a curator has to know.

**It reaches only body-less `declare fn` declarations.** Interface and class
methods are deferred to Phase 12, because `inferInterface` is the same path
that ingests a `.d.ts` and elision would misfire on the signatures there. A
method the converter emits as an interface member gets no elision at all.

**Where it does run, it never binds a return to the receiver.** `SelfParam` is
a field of its own on `FuncType`, separate from `Params`, and elision counts
only `Params`. `ApplyLifetimeElision` says so in as many words and points at
the method-call machinery as where a receiver's lifetime comes from instead.
The lifetimes requirements do list a method-receiver elision rule. The checker
does not implement it. What is implemented is four cases:

1. A non-reference return gets no lifetime.
2. Exactly one reference-typed parameter, and the return takes that parameter's
   lifetime.
3. Zero reference-typed parameters, and the return is treated as freshly
   allocated.
4. More than one, and the signature is left unannotated.

That decides the table:

| `returns` | What an unannotated declaration gets | The curator |
| --- | --- | --- |
| `receiver` | never a borrow of the receiver. Where elision runs at all, a signature with no reference parameter reads as fresh, and one with exactly one takes that parameter's lifetime, naming the wrong source | writes all fifteen |
| a receiver-lifetime borrow that is not `Self` | the same, and both `buffer` getters declare no parameter, so the return reads as fresh | writes both, once the lattice can spell the kind |
| `param:<n>` | case 2 is right where the fact's position is the signature's only reference parameter | writes the rest, and the fact names which position |
| `fresh`, value-typed | case 1, no lifetime | writes nothing |
| `fresh`, reference-typed | case 3 is right where there is no reference parameter. Case 2 binds the return to a reference parameter that is not its source | writes the over-constrained ones |
| `union` | no case joins two sources | writes it |

A value-typed `fresh` return is the only row that needs nothing. Elision gets
some of the others right, but by counting parameters rather than by knowing
what the algorithm returns. So the fact is what tells a reviewer which of
those to leave alone, as much as it is what corrects the rest.

### The declared type does not carry the aliasing either

A reader might expect the `.d.ts` to reveal a receiver return, since TypeScript
spells one `this`. It does for eleven of the fifteen. Four declare a named type
instead, at `typescript@5.7.2`, the version the repo pins:

| Builtin | Declared return |
| --- | --- |
| `Array.prototype.reverse` | `T[]` |
| `Object.prototype.valueOf` | `Object` |
| `Iterator.prototype [ @@iterator ]` | `Iterator<T, TReturn, TNext>` |
| `AsyncIteratorPrototype [ @@asyncIterator ]` | `AsyncIterator<T, TReturn, TNext>` |

`Array.prototype.reverse` is the sharpest of them. Its own doc comment says the
method "returns a reference to the same array" while its declared type says
`T[]`.

This matters for the other consumer of these declarations. A `.d.ts` imported
directly skips elision, and the FR7 interop rules of
[../lifetimes/requirements.md](../lifetimes/requirements.md) annotate it from
the declared types. Rule D reads a `this` return as an alias of the receiver and
covers the eleven. Rule C reads the four as returning a fresh value, and reads
the two `buffer` getters the same way. The requirements reach the right answer
for `reverse` anyway, by naming it in Rule D's example block rather than by the
rule as written. That is a hand-maintained list of method names, which is the
thing the fact replaces: the algorithm ends in `Return O`, and the fact says so
for all fifteen without anyone keeping a list current.

The second row of the table above is the residue §4.4 records by name, and the
final section answers it.

## Open case: a non-mutating method that returns its receiver

Three of the fifteen publish `receiver: borrow` alongside `returns: receiver`:
`Object.prototype.valueOf`, `Iterator.prototype [ @@iterator ]`, and
`AsyncIteratorPrototype [ @@asyncIterator ]`. Each returns the `this` value
without writing it.

Under the rule that a return-borrow carries the receiver's mutability, each
annotates `-> &Self`, and a caller holding a `mut` receiver gets an immutable
one back. The annotation that would preserve the caller's mutability is a return
abstracted over the receiver's mutability, which the affine checker does not
express today.

It bites on the two `@@iterator` methods. `for (x of it)` calls `@@iterator` and
then `next`, and §11 decides whether Escalier's iterator protocol takes
`&mut self`. If it does, an `@@iterator` annotated `-> &Self` hands back a
receiver `next` cannot be called on. The two decisions have to be made together,
and §11 is where both sit. `Object.prototype.valueOf` is unaffected, because
nothing chains off it.

Mutability polymorphism is out of this workstream's scope and belongs to
[../affine_semantics/requirements.md](../affine_semantics/requirements.md).
Until it lands, `-> &Self` is the annotation, and a `mut` caller that needs the
mutability back re-borrows the original receiver.

## Open case: a borrow of the receiver's lifetime that is not `Self`

`get DataView.prototype.buffer` and `get TypedArray.prototype.buffer` return
`[[ViewedArrayBuffer]]`, an object the receiver holds. Both publish
`returns: unknown`, because no member of the alias lattice spells "reached
through the receiver" and `receiver` would claim the getter hands back the view
itself.

**The annotation is the receiver's lifetime on the declared return type, with
no claim that the return is `Self`:**

```esc
get buffer<'a>(self: 'a DataView) -> 'a ArrayBuffer
```

That is a third shape beside the two the mapping already has. `receiver` puts
`Self` in the return position; this puts a different type there. `fresh` puts no
lifetime in the return position; this puts the receiver's.

Nothing else in the pipeline produces it. Both getters declare no parameter, so
elision's case 3 reads the return as freshly allocated wherever it runs. Interop
Rule C reaches the same answer down the `.d.ts` path, because the return type
matches no parameter. Both are wrong here, and both fail quietly:

```esc
val view: mut DataView = new DataView(bytes)
val buf = view.buffer         // buf is left out of view's alias set
val frozen: DataView = view   // accepted, though buf still reaches the bytes
```

Both getters are accessors, so `Match.ReceiverApplies` keeps the join from
writing a receiver claim onto them and the converter sets their mutability
itself. The return lifetime is unaffected and stays a curated annotation.

This is the accessor pattern, and the set grows with every such getter the spec
adds. Answering it makes the `receiverInterior` alias kind proposed in
[#1284](https://github.com/escalier-lang/escalier/issues/1284) worth adding,
because this row is what would read it. `aliasOf` in
[../../internal/ecma262/classify.go](../../internal/ecma262/classify.go)
already resolves these returns to `Interior(Receiver)` and then discards the
provenance, so the analysis holds the fact and the lattice has nowhere to put
it.
