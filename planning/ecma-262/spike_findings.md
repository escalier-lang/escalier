# §1 Feasibility spike: findings

Records the outcome of [implementation_plan.md](implementation_plan.md) §1.
The spike built ESMeta from source, ran `extract → compile → build-cfg`
against a pinned ECMA-262 revision, and inspected the control-flow graph for
the representative method set the analysis must handle.

**Verdict: the happy path holds.** The CFG exposes every signal §4/§8/§9
read: abstract-operation call nodes with their argument operands including
the stored value of a write, `Let` bindings with traceable origins,
internal-slot writes, the `?`/`!`/plain completion guards, explicit `Throw`
steps with their error class, promise-rejection sites distinct from
synchronous throws, callback-parameter calls distinct from resolvable
abstract operations, and return values. No signal is missing wholesale, so
the pure-Go `spec.html` shallow-parser fallback (§3 alternatives) is not
needed. Two structural facts change how §3 lowers the CFG and are detailed
below; neither narrows §8/§9 scope.

## Toolchain and pinned revisions

Reproduced with the following. §2 pins this toolchain under
[tools/spec-extract/](../../tools/spec-extract/).

| Component                | Revision |
| ------------------------ | -------- |
| ESMeta                   | `7d237fd1680f473e674320cc97932702d950fa98`, one commit past the `v0.7.3` tag on `main` |
| ECMA-262 spec            | `84b38ad852ff426795fa29cebc06949027336c64` (tag `es2025`, ESMeta's `ecma262` submodule) |
| Scala                    | 3.3.6 |
| sbt that compiles ESMeta | 1.10.11, from ESMeta's `project/build.properties` |
| sbt launcher             | 1.10.7 |
| JDK                      | Temurin 21 (ESMeta requires 17+) |

`sbt assembly` produced `bin/esmeta` in ~2 minutes on 4 cores.
`esmeta build-cfg -build-cfg:log` ran the full pipeline and dumped one
`.cfg` text file per function to `logs/cfg/func/`. The run produced 2951
functions, covering the whole mechanized spec rather than the builtin library
alone:

| Category | Count | Example |
| -------- | ----- | ------- |
| Syntax-directed operations | 1746 | `AdditiveExpression[1,0].Evaluation` |
| Abstract operations | 548 | `ToString`, `Call`, `SetIntegrityLevel` |
| Builtin methods and statics | 501 | `INTRINSICS.Array.prototype.push` |
| Internal methods | 107 | `Record[OrdinaryObject].Set` |
| Abstract closures | 49 | `INTRINSICS.Promise.prototype.finally:clo0` |

The categories partition the 2951. Closures are counted on their own row rather
than under the algorithm that encloses them, so counting by the `INTRINSICS.`
name prefix alone gives 517 — the 501 above plus the 16 closures defined inside
a builtin algorithm. 517 is the builtin-surface figure used elsewhere in this
document.

The builtins are the target surface, but two other categories carry weight.

An abstract operation is a named subroutine ECMA-262 defines to factor out
behavior shared across algorithms, such as `ToString` or `SetIntegrityLevel`.
It is internal to the specification and not reachable from JavaScript. These
are what the §4.1 fixpoint walks to resolve transitive mutation —
`Object.freeze` reaches its write through `SetIntegrityLevel`, an abstract
operation, not a builtin — so §3 must serialize the operations reachable from
the builtin surface, not the 517 alone. The internal methods are
the dispatch targets §4.1 deliberately does not enter: `[[Set]]` alone has five
implementations, `Record[OrdinaryObject].Set`,
`Record[ProxyExoticObject].Set`, `Record[TypedArray].Set`,
`Record[ArgumentsExoticObject].Set`, and
`Record[ModuleNamespaceExoticObject].Set`. Nothing in the CFG says which one a
given `O.Set(…)` call selects, which is why the FR1 seed stops the fixpoint
above them. The syntax-directed operations are the runtime semantics of
the language itself, one function per grammar production and operation; they
are out of scope for annotating a library surface, so §3 can skip them.

[reproduce_spike.sh](reproduce_spike.sh) automates these steps end to end.
The representative-method dumps this document reads from are committed under
[spike_evidence/](spike_evidence/), so the per-method claims below are
checkable without building ESMeta.

## What the CFG carries

`build-cfg` lowers each algorithm to `esmeta.cfg.CFG`, a graph of three node
kinds — `Block` (a list of straight-line IR instructions), `Call`, and
`Branch` — over the IR instruction set in
`src/main/scala/esmeta/ir/Inst.scala`. This is the compiled IR, so the
spec's `?`/`!` completion sugar is already lowered into explicit control
flow. The IR instruction and expression vocabulary the analysis reads:

- `ILet(name, expr)` — a `Let` binding, rendered `let x = <expr>`.
- `IAssign(ref, expr)` — a write to a reference. A property or internal-slot
  write is `IAssign(Field(base, slot), value)`, rendered `base.slot = value`.
- `IPush(elem, list, front)` — a list append, rendered `push list < elem`.
  This is how "Append … to a `[[List]]` slot" is spelled.
- `ICall(lhs, callee, args)` — an abstract-operation call, rendered
  `call %r = clo<"AOName">(args)`. A dynamic internal-method dispatch is
  `base.Method(args)`, e.g. `target.Set(target, key, V, receiver)`.
- `IReturn(expr)` — a return, rendered `return <expr>`.
- `IAssert(expr)`, `IIf(cond, then, else)`, `IWhile(cond, body)`.
- Allocation expressions `ERecord` / `EList` / `EMap` / `ECopy`, each
  carrying an allocation-site id — these are the `fresh` origins.

The receiver arrives as the explicit first parameter `this`, and arguments
arrive in a `List` parameter `ArgumentsList` from which each named formal is
`pop`ped in order. Origins are therefore recoverable: `this` and its
coercions are receiver-origin, a popped formal is parameter-origin at its pop
index, and an allocation expression is fresh.

## Gate results per representative method

Each method was read from its `logs/cfg/func/INTRINSICS.<name>.cfg` dump.
"Escape operand" means the CFG exposes the stored-value argument of the write
that stores a parameter into the receiver, the signal §8.1 needs.

| Method | Shape | Confirmed in CFG |
| ------ | ----- | ---------------- |
| `Array.prototype.push` | direct receiver mutation + escape | `Set(O, %6, E, true)` where `O = ToObject(this)`, `E` from `ArgumentsList`; receiver mutation and escape operand `E` (arg 2) both exposed; `RangeError` domain throw explicit; returns `fresh` |
| `Array.prototype.fill` | receiver mutation returning receiver | `Set(O, Pk, value, true)`; returns `O` (receiver) |
| `Array.prototype.sort` | receiver mutation returning receiver | writes back **directly** via `Set(obj, %7, sortedList[j], true)`; returns `obj`; `SortIndexedProperties` only reads `obj` via `Get` and returns a fresh sorted list, so it supplies the ordering, not the write — this is not the helper-mediated case |
| `Object.freeze` (and `Object.seal`) | transitive mutation through a helper abstract operation | no write in its own body; mutates only by `SetIntegrityLevel(O, ~frozen~)` on param 0; the mutation is discovered by the FR2 fixpoint, not read off `freeze` directly; `receiver: none`, param 0 `mutBorrow`, returns `param:0` |
| `Array.prototype.slice` | fresh allocation, no receiver mutation | `ArraySpeciesCreate(O, count) → A`; writes target `A` via `CreateDataPropertyOrThrow(A, …)`; receiver only read via `Get`/`HasProperty`; returns `A` (fresh) |
| `Array.prototype.map` | fresh allocation + callback | `ArraySpeciesCreate(O, len) → A`; `Call(callback, …)`; `CreateDataPropertyOrThrow(A, Pk, mappedValue)`; returns `A` (fresh) |
| `Map.prototype.set` | internal-slot mutation + escape | `push M.MapData < p` with `p = record{Key: key, Value: value}`; also `p.Value = value`; both params escape into `[[MapData]]`; returns `M` (receiver) |
| `Set.prototype.add` | internal-slot mutation + escape | `push S.SetData < value`; `value` escapes into `[[SetData]]`; returns `S` (receiver) |
| `Reflect.set` | namespace fn, param mutation + escape | write dispatched through `target.Set(target, key, V, receiver)`; `target` mutated (arg 0), stored value `V` (arg 2) exposed; `TypeError` on non-object `target` explicit; `receiver: none` |
| `String.prototype.charAt` | immutable primitive | `RequireObjectCoercible(this)` + `ToString`; zero mutation ops; returns `fresh` strings |
| `String.prototype.replace` | immutable primitive + callback | zero mutation ops on receiver; `Call(replacer, …)` when replacer is a function; returns `fresh` |
| `String.prototype[%Symbol.iterator%]` | symbol-keyed | own algorithm present under the `[%Symbol.iterator%]` name |
| `Array.prototype.forEach` | callback propagation | `Call(callback, thisArg, «kValue, k, O»)` where `callback` is popped param 0; `?`-guarded; `IsCallable` `TypeError` explicit; returns `undefined` |
| `Number.prototype.toFixed` | domain throw + partial `yet` | two `RangeError` domain guards fully visible; `yet` steps confined to the trailing decimal-string arithmetic |
| `Promise.reject` | async reject, param origin | rejects via `Call(promiseCapability.Reject, undefined, «r»)`, `r` = param 0 |
| `Promise.all` | async reject, combinator | `IfAbruptRejectPromise` lowered to a capability `[[Reject]]` call; element-`E` forwarding runs inside `PerformPromiseAll` |

## How each required signal is exposed

The snippets below are **illustrative**, written with readable names to show
the shape the analysis matches on. Real dumps use compiler temporaries — `%0`,
`%7` — and the surrounding coercion and bounds-check steps are elided here.
Each subsection names the representative method it was read from, so the shape
can be checked against that method's dump in [spike_evidence/](spike_evidence/).

### Abstract-operation calls, arguments, and the stored-value operand

A `Call` node carries the callee and every argument expression as operands, so
the value being stored by a write is always a plain argument the analysis can
read. Three write forms appear, and all three expose both the written object
and the stored value:

| Form | Illustrative shape | Written object | Stored value |
| ---- | ------------------ | -------------- | ------------ |
| Abstract-operation write | `call %r = clo<"Set">(obj, key, val, true)` | `obj` (arg 0) | `val` (arg 2) |
| Backing-slot append | `push obj.MapData < entry` | `obj` | `entry` |
| Dynamic internal-method write | `call %r = obj.Set(obj, key, val, receiver)` | `obj` (arg 0) | `val` (arg 2) |

The stored-value operand is the signal §8.1 needs to decide `escape`: a
parameter appearing in that position outlives the call inside the written
object.

*Seen in:* `Array.prototype.push` stores its element through the abstract-operation form;
`Map.prototype.set` builds an entry record and appends it to the slot;
`Reflect.set` writes through the dynamic form.

### Let bindings and origins

`ILet` gives the bound name and its source expression, and origins propagate
along the binding chain. The three origins the analysis assigns:

| Origin | Illustrative shape | Why |
| ------ | ------------------ | --- |
| Receiver | `call %r = clo<"ToObject">(this)` … `let obj = %r` | traces to `this` through an identity-preserving coercion |
| Parameter | `pop item < ArgumentsList` … `let elem = item` | traces to a popped formal, at its pop index |
| Fresh | `call %r = clo<"ArraySpeciesCreate">(obj, len)` … `let out = %r` | an allocator result, not observable to callers |

A property or slot *read* breaks the chain, since the value read out of a
container is a different object from the container.

*Seen in:* `Array.prototype.push` for the receiver and parameter chains;
`Array.prototype.slice` and `map` bind their `ArraySpeciesCreate` result as
fresh.

### Transitive mutation through a helper abstract operation

FR2's mutation summary is an inter-procedural fixpoint, so it needs two things
the CFG must carry: every helper abstract operation present as a first-class
function with its body, and each call's argument operands, so an argument at a
caller can be matched to the helper's mutated parameter. Both hold — every
abstract operation the representative methods call is dumped as its own
function, so the call graph is complete.

The shape that matters is a caller with **no write of its own**, whose only
effect on a parameter is passing it to a helper:

```
Caller(p):                                  // p is param 0
  call %0 = clo<"Helper">(p, …)             // no write in this body
  return p

Helper(o, …):
  call %1 = clo<"DefinePropertyOrThrow">(o, key, desc)   // seeded mutator
```

The fixpoint resolves this in two hops, with no direct write at either caller:

- `MutArgs[DefinePropertyOrThrow] = {0}` — from the FR1 seed.
- `Helper` passes its own parameter `o` as argument 0 of a function that
  mutates argument 0, so `MutArgs[Helper] = {0}`.
- `Caller` passes its own parameter `p` as argument 0 of `Helper`, so
  `MutArgs[Caller] = {0}`.

*Seen in:* `Object.freeze` is this shape exactly — it delegates to
`SetIntegrityLevel`, which writes through `DefinePropertyOrThrow`. As a static
it has `receiver: none`, param 0 `mutBorrow`, and returns `param:0`;
`Object.seal` is identical but for the integrity level. Note that
`Array.prototype.sort` is *not* this case: it writes the receiver back
directly in its own body, and its `SortIndexedProperties` helper only reads
the receiver and returns a fresh sorted list.

### Internal-slot writes

Two spellings appear, both naming the object expression and the slot:

- **List append** (`IPush`) — `push obj.MapData < entry`, how "Append … to
  *obj*.[[List]]" is lowered.
- **Direct store** (`IAssign` over a `Field`) — `entry.Value = val`.

**The slot name is rendered without its `[[ ]]` brackets** — `MapData`, not
`[[MapData]]` — so §4.1's `BackingStoreSlots` must be keyed by the bare name.

*Seen in:* `Map.prototype.set` uses both spellings; `Set.prototype.add` uses
the append form.

### Completion guards `?` / `!` / plain

The guards are **lowered into control flow, not kept as a flag on the call**.
This is the first fact that shapes §3. The compiled forms are fixed and
deterministic, so the serializer reconstructs the guard from the shape
following a `Call`:

| Guard | Illustrative shape after the call | Distinguishing feature |
| ----- | --------------------------------- | ---------------------- |
| `?` | `assert (? %r: Completion)` → `if (? %r: Abrupt) then return %r else %r = %r.Value` | an abrupt-check branch that returns the completion |
| `!` | `assert (? %r: Normal)` → `%r = %r.Value` | normal assertion and unwrap, no abrupt branch |
| plain | the result is used directly | no completion assertion at all |

`(? e: T)` is the IR's type-check expression `ETypeCheck`, not the spec's `?`
sugar; `(? %r: Abrupt)` reads "is `%r` an abrupt completion." §3 must match
these structurally, because the compiler discards the surface annotation. The
`?`/`!` distinction FR10 depends on survives intact.

### Explicit `Throw` steps

`Throw a *T* exception` lowers to two calls and a return:

```
call %e = clo<"__NEW_ERROR_OBJ__">("%RangeError.prototype%")
call %c = clo<"ThrowCompletion">(%e)
return %c
```

**The error class is the string literal argument to `__NEW_ERROR_OBJ__`**, so
the throw-set fixpoint reads the class name off that operand.

*Seen in:* `Number.prototype.toFixed` raises `RangeError` this way and
`Array.prototype.push` raises `TypeError`. Each sits behind an explicit `if`
domain check, which is what separates them from the receiver- and
parameter-coercion `TypeError`s the FR11 filter drops.

### Promise rejection versus synchronous throw

The two channels are distinguishable by the callee at the raise site:

| Channel | Illustrative shape | Recorded as |
| ------- | ------------------ | ----------- |
| Synchronous throw | `call %c = clo<"ThrowCompletion">(%e)` | `throws` (FR10) |
| Asynchronous rejection | `call %r = clo<"Call">(capability.Reject, undefined, «val»)` | `rejects` (FR13) |

`IfAbruptRejectPromise` lowers to an abrupt-check branch whose then-edge takes
the capability-reject shape and returns `capability.Promise` — a rejection,
never a `ThrowCompletion`. A rejected value that is a parameter gives the
`param:k` origin FR13 records.

*Seen in:* `Promise.reject` rejects with its param 0; `Promise.all` shows the
`IfAbruptRejectPromise` form.

### Callback propagation versus a resolvable abstract operation

Both are `Call` nodes; the callee expression tells them apart:

| Case | Illustrative shape | Throw contribution |
| ---- | ------------------ | ------------------ |
| Resolvable abstract operation | `call %r = clo<"AOName">(…)` | the callee's own throw set, by name |
| Callback parameter | `call %r = clo<"Call">(cb, thisArg, «…»)` where `cb` is a popped formal | `throwsOf:param:k` |

In the callback case the abstract operation being invoked is `Call` itself, which *is*
statically known — the callback is its **first argument**. So §9.1 identifies
the case by the origin of that argument, not by the callee name. The call is
`?`-guarded, so the propagation edge is present.

*Seen in:* `Array.prototype.forEach`, whose `cb` is popped param 0.

### Return values

`IReturn` carries the returned expression. Normal returns are wrapped in
`NormalCompletion` and abrupt returns forward the completion, so the
return-alias classifier reads the operand:

| Alias | Illustrative shape |
| ----- | ------------------ |
| `receiver` | `return obj` / `NormalCompletion(obj)` where `obj` is receiver-origin |
| `fresh` | `NormalCompletion(out)` where `out` came from an allocator |
| `param:k` | `NormalCompletion(p)` where `p` is the k-th formal |

*Seen in:* `Array.prototype.fill` returns the receiver, `slice` returns fresh,
`Object.freeze` returns `param:0`.

## Facts that shape §3 / §8 / §9

1. **Guards are lowered, not flagged (§3).** The `?`/`!`/plain distinction
   lives in the post-call branch shape, not a call attribute. §3's Appendix A
   `Node.Call` still records one guard field, but the serializer must compute
   it by matching the fixed abrupt-check / assert-normal patterns above.
   This is a lowering rule in the serializer, not a scope cut.

2. **Property writes dispatch through dynamic `[[Set]]` (§4.1 seed).** The
   `Set` abstract operation's body is `O.Set(O, P, V, O)`, a dynamic dispatch
   to the object's `[[Set]]` internal method, unresolvable by callee name. The
   FR1 seed that treats `Set`, `CreateDataProperty`, `DefinePropertyOrThrow`,
   and the rest as direct mutators of argument 0 is therefore required, not an
   optimization — the fixpoint cannot recover these writes by descending
   through the method-table dispatch. `Reflect.set` reaches the same dispatch
   as `target.Set(…)`, so its `target` mutation is caught by the same seed.

3. **Internal-slot writes drop the bracket syntax (§3).** Slots render as
   `MapData` / `SetData`, not `[[MapData]]`. The serializer and the
   `BackingStoreSlots` list key on the bare name.

4. **Promise combinators need hand-modeling, as planned (§9.3).**
   `Promise.all` forwards its element promises' reject type through
   `PerformPromiseAll` and the resolve/reject closures, not a traceable value
   operand, so origin tagging alone cannot see the element `E`. This confirms
   the plan's expectation that `Promise.all` / `race` / `any` / `allSettled`
   are hand-modeled rules rather than derived facts. The synchronous reject
   sites inside the combinator — the `IfAbruptRejectPromise` guards — are
   visible and attributable; only the element-`E` union is hand-modeled.

5. **`yet` is per-step, not per-method (§4/§9 fallback granularity).** 71 of
   517 builtins carry at least one `yet` marker for an un-formalized step, but
   the marker is local to that step. `Number.prototype.toFixed` is `yet` only
   in its trailing decimal-string arithmetic, while its `RangeError` throw
   guards and its read-only receiver are fully analyzable. The affected set is
   concentrated in `Math.*`, `Atomics.*`, the `TypedArray` constructors,
   `Date.*` formatting, `JSON.stringify`, and the numeric `toFixed` /
   `toExponential` / `toPrecision` / `toLocaleString` family. The FR5
   conservative fallback should be applied **per signal** — mark a
   determination unclassified only when a `yet` sits on a step that
   determination reads — rather than dropping a whole method because it
   carries a `yet` anywhere.

   Most of that set is low-stakes for this workstream: `Math.*`, the `Date.*`
   and numeric formatters, and `JSON.stringify` compute a fresh return value
   without mutating the receiver or any parameter, so the facts worth
   extracting there are `receiver: borrow` and `returns: fresh`, which the
   visible part of each algorithm already establishes. `Atomics.*` is the
   exception and the one to watch: `Atomics.add` carries a `yet` *and* mutates
   its `typedArray` parameter, through `AtomicReadModifyWrite` and
   `GetModifySetValueInBuffer` writing the underlying buffer. There a `yet`
   coincides with a real effect, so the per-signal rule has to be applied
   rather than assumed — the mutation is only safely extractable because the
   `yet` sits off the path that establishes it.

6. **Symbol-keyed names are exposed; some are spec aliases (§5).** A
   symbol-keyed method with its own algorithm appears under the
   `X.prototype[%Symbol.name%]` name, e.g.
   `String.prototype[%Symbol.iterator%]`. `Array.prototype[Symbol.iterator]`
   has no own algorithm — the spec defines it as the same function object as
   `Array.prototype.values` — so it surfaces only as that alias. Resolving
   such aliases is a keying concern for §5, not a CFG gap.

## Scope impact

No new fallback PR is triggered: the CFG carries the stored-value operand and
the reject-versus-throw distinction, the two signals §1 was gated on, so §8
and §9 keep their planned scope. The serializer gains the three lowering
rules in facts 1–3 above, all mechanical pattern-matching over the CFG shape
already dumped here, and §9.3 keeps the hand-modeled combinator layer the
plan already scoped. The §1 → §3 and §1 → fallback branches in the plan's
"Discovery phases may grow the plan" resolve to the happy-path side. The plan
table's §1 gate is met.
