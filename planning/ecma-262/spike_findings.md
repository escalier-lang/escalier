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

Reproduced with the following, which §2 will pin exactly:

| Component      | Revision |
| -------------- | -------- |
| ESMeta         | `7d237fd1680f473e674320cc97932702d950fa98` (v0.7.3) |
| ECMA-262 spec  | `84b38ad852ff426795fa29cebc06949027336c64` (tag `es2025`, ESMeta's `ecma262` submodule) |
| Scala          | 3.3.6 |
| sbt            | 1.10.11 |
| JDK            | Temurin 21 (ESMeta requires 17+) |

`sbt assembly` produced `bin/esmeta` in ~2 minutes on 4 cores.
`esmeta build-cfg -build-cfg:log` ran the full pipeline and dumped one
`.cfg` text file per function to `logs/cfg/func/`. The run produced 2951
functions total, of which 517 are builtin methods and statics named
`INTRINSICS.<path>`.

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
| `Object.freeze` (and `Object.seal`) | transitive mutation through a helper AO | no write in its own body; mutates only by `SetIntegrityLevel(O, ~frozen~)` on param 0; the mutation is discovered by the FR2 fixpoint, not read off `freeze` directly; `receiver: none`, param 0 `mutBorrow`, returns `param:0` |
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

### Abstract-operation calls, arguments, and the stored-value operand

A `Call` node carries the callee name and every argument expression, so a
write's stored value is a plain argument operand. `Array.prototype.push`
stores its element with `call %7 = clo<"Set">(O, %6, E, true)`: `E` traces
to `ArgumentsList`, `O` traces to `ToObject(this)`, and `E` sits at argument
index 2. `Map.prototype.set` stores both key and value by building
`record{Key: key, Value: value}` and appending it, `push M.MapData < p`.
`Reflect.set` stores `V` through `target.Set(target, key, V, receiver)`. The
escape analysis reads the stored operand directly in every case.

### Let bindings and origins

`ILet` gives the bound name and its source expression, and origins propagate
through the chain. In `push`, `let O = %0` follows `%0 = %0.Value` following
`call %0 = clo<"ToObject">(this)`, so `O` is receiver-origin; `let E = %5[%4]`
with `%5 = items = ArgumentsList` is parameter-origin. Fresh origins are the
allocation expressions: `slice` and `map` bind `let A = <ArraySpeciesCreate…>`.

### Transitive mutation through a helper abstract operation

The FR2 mutation summary is an inter-procedural fixpoint, so the gate needs
the CFG to carry two things beyond a single method's own writes: every helper
abstract operation as a first-class function with its body, and each call
node's argument operands, so an origin at a caller's argument can be matched
to the helper's mutated parameter. Both hold. Every abstract operation the
representative methods call is dumped as its own `.cfg` function — `Set`,
`CreateDataPropertyOrThrow`, `DefinePropertyOrThrow`, `SetIntegrityLevel`,
`SortIndexedProperties`, `PerformPromiseAll`, and the rest — so the call graph
is complete and `MutArgs` can propagate along it.

`Object.freeze` is the representative that exercises the propagation rather
than a direct write. Its body performs no write of its own; its only effect on
the argument is one call:

```
INTRINSICS.Object.freeze:
  pop O < ArgumentsList                        // O is param 0
  call %1 = clo<"SetIntegrityLevel">(O, ~frozen~)
  … return O                                   // returns param 0

SetIntegrityLevel(O, level):
  … call %5  = clo<"DefinePropertyOrThrow">(O, k, «Configurable: false»)   // sealed branch
  … call %10 = clo<"DefinePropertyOrThrow">(O, k, desc)                    // frozen branch
```

`DefinePropertyOrThrow` is an FR1 seed mutator of argument 0. The fixpoint
then resolves two hops with no direct write at either caller:

- `MutArgs[DefinePropertyOrThrow] = {0}` — the seed.
- `SetIntegrityLevel` passes its own parameter `O` as argument 0 of
  `DefinePropertyOrThrow`, so `MutArgs[SetIntegrityLevel] = {0}`.
- `Object.freeze` passes its own parameter `O` as argument 0 of
  `SetIntegrityLevel`, so `MutArgs[Object.freeze] = {0}`.

`Object.freeze` is a static, so it has `receiver: none`; the mutated argument
0 is `mutBorrow`, and the return is `param:0`. `Object.seal` has the identical
shape with `~sealed~`. This is the helper-mediated case sort does not cover:
`Array.prototype.sort` performs its receiver write-back directly with
`Set(obj, …)` in its own body, and `SortIndexedProperties` only reads the
receiver, so sort exercises a direct write, not the fixpoint.

### Internal-slot writes

Two spellings appear. A list-backed slot append is `push M.MapData < p`
(`IPush`). A direct slot store is `p.Value = value` (`IAssign(Field(…))`).
Both name the object expression and the slot. **The slot name is rendered
without its `[[ ]]` brackets** — `MapData`, not `[[MapData]]` — so the §3
serializer keys `BackingStoreSlots` by the bare name.

### Completion guards `?` / `!` / plain

The guards are **lowered into control flow, not kept as a flag on the call**.
This is the first fact that shapes §3. The compiled forms are stereotyped and
deterministic, so the serializer reconstructs the guard from the shape that
follows a `Call`:

- `?` (`ReturnIfAbrupt`): `assert (? x: Completion)` then
  `if (? x: Abrupt) then return x else x = x.Value`. An abrupt-check branch
  whose then-edge returns the callee result unwrapped on the else-edge.
- `!` (assert-normal): `assert (? x: Normal)` then `x = x.Value`. No
  abrupt-return branch — the normal assertion and unwrap, nothing else.
- plain: the result is used directly with no completion assertion.

`(? e: T)` here is the IR's type-check expression `ETypeCheck`, distinct from
the spec's `?` sugar; `(? x: Abrupt)` reads "is `x` an abrupt completion." §3
must recognize the `?`/`!` patterns structurally rather than reading a guard
attribute, because the compiler discards the surface annotation. The `?`/`!`
distinction that FR10 depends on survives intact.

### Explicit `Throw` steps

`Throw a *T* exception` lowers to two calls and a return:
`call %e = clo<"__NEW_ERROR_OBJ__">("%T.prototype%")` then
`call %c = clo<"ThrowCompletion">(%e)` then `return %c`. **The error class is
the string literal argument to `__NEW_ERROR_OBJ__`** — `"%TypeError.prototype%"`,
`"%RangeError.prototype%"` — so the throw-set fixpoint reads the class name off
that operand. `toFixed`'s two `RangeError` guards and `push`'s `TypeError`
guard both appear in this form, each gated by an explicit `if` domain check
that distinguishes them from the coercion `TypeError`s the FR11 filter drops.

### Promise rejection versus synchronous throw

The two channels are distinguishable. A synchronous throw is the
`ThrowCompletion` form above. An asynchronous rejection is a call to the
capability's reject slot, `Call(promiseCapability.Reject, undefined, «value»)`.
`Promise.reject` rejects with `r` at param index 0, giving the `param:0`
origin FR13 records. `IfAbruptRejectPromise` lowers to an abrupt-check branch
whose then-edge calls `promiseCapability.Reject` and returns
`promiseCapability.Promise`, again the capability-reject shape, never a
`ThrowCompletion`. §9.3 tells them apart by the callee: `ThrowCompletion` is a
sync throw, a `.Reject` capability call is a rejection.

### Callback propagation versus a resolvable abstract operation

A resolvable call names a fixed abstract operation, `clo<"AOName">(…)`. A
callback call is the `Call` abstract operation applied to a parameter-origin
callee: `forEach` emits `clo<"Call">(callback, thisArg, «…»)` with `callback`
the popped param 0. §9.1 recognizes the callback case as a `Call`/`Construct`
whose callee argument is parameter-origin, yielding `throwsOf:param:k`; every
other guarded call resolves to a named abstract operation with its own throw
set. The call is `?`-guarded, so the propagation edge is present.

### Return values

`IReturn` carries the returned expression. Normal returns are wrapped
`NormalCompletion(v)` and abrupt returns forward the completion, so the
return-alias classifier reads `receiver` (`return O` / `NormalCompletion(O)`),
`fresh` (`NormalCompletion(A)` off an allocator), or `param` off the operand.

## Facts that shape §3 / §8 / §9

1. **Guards are lowered, not flagged (§3).** The `?`/`!`/plain distinction
   lives in the post-call branch shape, not a call attribute. §3's Appendix A
   `Node.Call` still records one guard field, but the serializer must compute
   it by matching the stereotyped abrupt-check / assert-normal patterns above.
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
