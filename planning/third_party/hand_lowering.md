# Third-party source analysis: hand-lowering check

**Status:** Done. This is the temperature check that precedes the spike in
[source_analysis.md](source_analysis.md).

The check lowered 56 exported members of ten npm packages by hand into the
control-flow IR that [internal/ecma262/](../../internal/ecma262/) reads, then
ran the ECMA-262 mutation analysis over them. The question was what fraction
of members the analysis settles. A member is settled when every parameter,
and the receiver for a method, comes out as a proven `&` or a proven `&mut`
rather than unknown.

The check ran twice. The first run used the analysis as the builtins
workstream left it. The second ran after the origin-lattice changes that
section A below describes, which are now part of `internal/ecma262`. A third
run added the callback rule that section C describes, which changed no
member's settled status, so the tables below report the first two runs.

## Result

| Package         | First run | After lattice changes | Shape                                      |
| --------------- | --------- | --------------------- | ------------------------------------------ |
| `date-fns`      | 0/6       | 0/6                   | pure functions over a shared `toDate`      |
| `lodash-es`     | 1/6       | 1/6                   | untyped utility helpers, many layers deep  |
| `mitt`          | 1/4       | 1/4                   | methods are closures over a captured map   |
| `eventemitter3` | 2/6       | 5/6                   | ES5 prototype methods over `this._events`  |
| `express`       | 2/7       | 3/7                   | CommonJS prototype objects, many deps      |
| `immer`         | 2/5       | 3/5                   | class-field arrows, module-level caches    |
| `lru-cache`     | 1/5       | 1/5                   | private fields holding closures            |
| `rxjs`          | 2/6       | 2/6                   | classes, operators built from closures     |
| `uuid`          | 1/5       | 3/5                   | compiled TypeScript, host `crypto` calls   |
| `zod`           | 2/6       | 3/6                   | classes built at runtime by `$constructor` |
| Total           | 14/56     | 22/56                 |                                            |

The lattice changes took the settled members from 14 to 22. One member in each
run has only primitive parameters, so the non-trivial counts are 13 of 55 and
21 of 55. The plan's gate asked for about half of the utility packages'
members. `date-fns` still settles 0 of 6 and `lodash-es` 1 of 6, so the
analysis is still short of the gate.

Every answer but one is correct. The first run's 14 settled members all match
the source. In the second run 21 of 22 match, and so do the 12 proven
positions inside members that stayed open, such as `pullAll`'s `&mut` array.
The exception is `app.enabled`, which comes out `&mut self` where the source
only reads. The analysis ignores control flow, as the caveats below describe,
so that answer is sound but stricter than needed. The analysis never claimed
`&` for a value the source writes.

Receivers are the determination the plan rated highest. Of 30 instance
members, 19 receivers settle after the changes, up from 16. Twelve of those
are a proven `&mut self` and 7 a proven `&self`. The `&self` methods are the
simple ones in the sample, such as `BehaviorSubject.getValue`,
`ZodType.brand` and `EventEmitter.listenerCount`.

The return alias is known for 35 of 56 members, up from 24. Nearly all of
those are a fresh value or a primitive. The borrows it found are
`return this` and `freeze`'s `return obj`, which matches the plan's
expectation that lifetimes beyond those shapes stay out of reach.

## What blocks the rest

Each open member is blocked by one or more causes, which group into six
themes. The table counts the open members a theme blocks at all, and those it
blocks alone, in each run.

| Theme                                                       | First run | Alone | After | Alone |
| ----------------------------------------------------------- | --------- | ----- | ----- | ----- |
| A. Origin lattice: writes through properties, call results  | 23        | 4     | 11    | 0     |
| B. Module-level and captured state                          | 10        | 3     | 2     | 0     |
| C. Callbacks the caller supplies                            | 16        | 1     | 16    | 1     |
| D. Dynamic dispatch and functions built at runtime          | 29        | 4     | 29    | 5     |
| E. Code outside the package                                 | 11        | 0     | 10    | 1     |
| F. Not lowered for this check                               | 4         | 0     | 4     | 0     |

Removing the themes one at a time, in that order, settles this many of the
open members. There were 42 in the first run and 34 after the changes.

| Themes removed | First run | After lattice changes |
| -------------- | --------- | --------------------- |
| A              | 4         | 0                     |
| A–B            | 9         | 2                     |
| A–C            | 10        | 3                     |
| A–D            | 28        | 21                    |
| A–E            | 38        | 30                    |

A member that settles on this ladder is no longer blocked. Its answer could
still turn out to be `&mut`.

### A. The origin lattice

The analysis gives every value one of four origins: the receiver, a
parameter, a fresh allocation, or Unknown. A write is charged to its holder
only when the written value has one of the first three. In the first run,
library code wrote through values the lattice could not place in four ways:

- **Through a property.** `this._events[evt] = listener` in `eventemitter3`
  writes `this._events`, a property read, which comes out Unknown. The spec
  keeps internal state in slots such as `[[MapData]]`, which the lattice marks
  as interior to their holder. Library code keeps it in ordinary properties.
- **Through a call result.** `addDays` writes the `Date` that `toDate`
  returns. The origin map gives every call result Unknown, even when the
  callee allocates.
- **Through a rebound parameter.** `options = options || {}` joins a
  parameter with a fresh object, and the join of two different origins is
  Unknown.
- **Through a closure parameter.** `arr.forEach((x) => { x.n = 1 })` writes an
  element whose origin the closure does not know.

The changes to `internal/ecma262/origin.go` address the first three:

- **A property read is interior to its holder.** Escalier's `mut` is deep
  ([docs/08_mutability.md](../../docs/08_mutability.md)), so a write through a
  property of the receiver is a write to the receiver. A property read off the
  receiver or a parameter is now interior to it, the way a backing-store slot
  already was.
- **A parameter joined with a fresh value stays the parameter.** The join is
  marked shallow. A write to it is charged to the parameter, which is sound
  because writing the fresh half is invisible. A read inside it is Unknown,
  since its contents may come from either half.
- **A call result takes its origin from the callee.** `NewOriginMaps`
  computes every function's origin map together, in rounds. A call into an
  abstract operation reads that operation's returns from the round before.
  The first round answers every call Unknown, so each round is sound and the
  rounds only sharpen.

What remains in theme A is mostly a consequence of other themes. `addDays`
still writes a call result, because `toDate` returns whatever the `options.in`
callback returns. lodash's `path = castPath(path)` joins a parameter with a
call result that is itself blocked by an unresolved call. The closure-parameter
case is unchanged.

#### What the changes do to the builtins

The changes apply to the ECMA-262 builtins too. Of 501 builtin facts, 115
changed, and every change gives a more precise answer:

- 96 returns went from `unknown` to `fresh`. `Date.prototype.setTime`
  returns what `TimeClip` computes, and every return in `TimeClip` is a fresh
  number.
- `Object.defineProperties` now returns `param(0)`, its target, and `RegExp`
  gained a union return.
- `RegExp.prototype [ @@replace ]` no longer needs its curated receiver. The
  list its appends land on comes from `__FLAT_LIST__`, which now reads as
  fresh, so the analysis proves `&mut self` itself. The curated entry is
  retired.
- 16 builtins lost an `unattributable` warning, among them `Function` and the
  typed-array constructors.

No receiver claim weakened, and `dts_to_esc`'s generated tree is unchanged.

### B. Module-level and captured state

A name the function does not bind reads as Unknown, so writing through it
blocks every parameter. The writes found were:

- `RegExp.prototype.test` on a module-level regex, which the ECMA-262 facts
  rightly charge, since it writes `lastIndex`;
- memo caches such as `immer`'s `cachedCtorStrings` and lodash's
  `stringToPath`;
- the map `mitt`'s methods capture.

The IR now has a `GlobalExpr` for a module-level binding, with an origin of
its own. A write to one charges neither the receiver nor a parameter, so it no
longer blocks them. The hand-lowered members name their module-level regexes,
caches and function values that way.

`mitt` needs the opposite answer and is still open. Its methods capture the
map the factory created, which is the emitter's own state, so a write to it
should charge the receiver. A closure over the object a factory returns is the
same shape as `this`. Recognizing that takes a front end, not a change to the
lattice.

### C. Callbacks

Some callbacks belong to the caller, such as `immer`'s `recipe` and the
`options.in` context function that every `date-fns` member reaches through
`constructFrom`. Others are internal higher-order helpers that the package
always calls with a function it writes itself, such as lodash's `arrayMap`,
`immer`'s `each` and `rxjs`'s `errorContext`.

The analysis now applies a callback rule to both kinds. A callback takes its
arguments as `&` borrows and returns an owned value. Checking that the
function a caller passes keeps to that is the caller's job, which Escalier's
checker does where the call is written. The mutation summary records each
call through a parameter under that parameter's position, as `callbacks{k}`,
instead of marking the function incomplete. A caller that passes the
parameter on records the call under its own parameter. A caller that passes
one of the package's own functions, such as `uuid`'s `v5` passing `sha1` to
`v35`, charges what that function mutates against the arguments the call
gives it. A closure the caller allocates is held to the same contract.

The rule removed the 14 callback blockers but settled no member in this run.
Every member they blocked had another blocker:

- `date-fns`: `toDate` passes `context || argument` to `constructFrom`'s
  callback position. The join of two parameters is Unknown, so the callback
  blocker moves from `constructFrom` to `toDate`. Every member also reaches
  the methods `constructFrom` calls on the caller's date, which are dynamic
  dispatch.
- lodash's `mapValues` reassigns `iteratee` to `baseIteratee(iteratee)`,
  which returns either the parameter or a matcher closure. That call result
  is Unknown, so the call through it stays unresolved.
- The rest are blocked by theme D or E as well.

What the rule did change is visible in the answers. A callback's result is
now an owned value, so `immer`'s `produce` proves `base` is `&mut`, and
zod's `string` returns a fresh value where its return was unknown. The return
alias is known for 36 of 56 members.

`mitt`'s `Emitter.emit` is the one member callbacks still block alone. It
reads the handlers `on` stored in the captured map and calls each with
`evt`. A stored callback is outside the rule, because the function the
caller registered reaches `emit` through state rather than as a parameter.
Covering it means carrying the `&` contract from `on`'s `handler`
parameter to the map that holds it. `lru-cache`'s `dispose` option is the
same shape.

`Array.prototype.forEach` and the other builtins that take a callback still
report `none` rather than `callbacks{0}`. They call it through the `Call`
abstract operation, whose body calls `Call` again, a name collision between
the operation and the `[[Call]]` internal method that resolves back to the
operation itself. Recording that call would also mark incomplete every
builtin that calls a function the analysis cannot name, such as a method
`GetMethod` returns. The harness stubs for these builtins call their
callback parameters directly instead.

### D. Dynamic dispatch and functions built at runtime

This is the largest theme, and it is the one a front end has to solve:

- a function stored in a field, such as `lru-cache`'s `#isStale`, or read out
  of state, such as `eventemitter3`'s `listeners.fn`;
- a function computed at module load, such as lodash's
  `stringToPath = memoizeCapped(...)`, zod's
  `parse = _parse($ZodRealError)` and every zod class built by
  `core.$constructor`;
- a call through an interface, such as `rxjs`'s `observer.next`;
- a call on an untyped value inside a JavaScript package, which covers most of
  `lodash-es` and `express`.

### E. Code outside the package

`express` calls `debug`, `mime`, `router` and Node's `http` types.
`lru-cache` calls `node:diagnostics_channel` and `uuid` calls `crypto`.
Clearing these needs the dependency facts the plan already calls for, plus a
fact table for host APIs. A Node and DOM table would need its own source,
since ECMA-262 covers neither.

## Mutations a `.d.ts` hides

The check found real mutations of caller-owned values, none visible in the
types:

- `z.string(params)` deletes `params.message` and copies it to
  `params.error`.
- `LRUCache.get`, `has` and `peek` write `options.status`.
- `uuid`'s `v4` writes the version bits into `options.random`, the caller's
  byte array.
- lodash's `isSymbol` and `isFunction`, and the `get` and `set` paths that
  reach them, write `Symbol.toStringTag` onto their argument and restore it.
  The write is transient but real.
- `immer`'s `freeze` freezes its argument, which the ECMA-262 facts charge as
  a mutation.

A name heuristic misses every one of these. They are the case for doing the
analysis at all.

## The join needs library knowledge

Matching a declaration to its implementation took knowledge of each library's
pattern:

- zod's `$constructor`, whose methods live in an object literal argument;
- `express`'s prototype objects built by property assignment;
- `immer`'s arrow functions in class fields, exported through
  `immer.produce`;
- `mitt`'s methods, which are properties of an object literal returned from a
  closure.

A general front end would recognize some of these and miss the rest.

## Caveats

- The sample is 56 members chosen by hand, and the expected answers come from
  reading the source.
- A builtin's stub performs the mutations its ECMA-262 summary charges and
  nothing else. A builtin whose own summary is incomplete, such as
  `String.prototype.replace` with a regex argument, is therefore treated as
  precise. That makes the stubs optimistic.
- Four open members call a helper that was not lowered. Each one is blocked by
  something else as well, so leaving the helper out changes no count.
- The analysis ignores control flow. `app.enabled` calls `this.set(setting)`
  with one argument, which only reads, but the analysis sees the writing
  branch too. In the first run the member was open for other reasons. After
  the lattice changes charge `this.settings[setting] = val` to the receiver,
  it comes out a sound but over-strict `&mut self`, the one mismatch in the
  second run.

## What this suggests

The lattice changes did what the first run's ladder predicted. They settled 8
more members against a predicted 9, sharpened 115 builtin facts, and cost
nothing a front end has to supply.

What remains is theme D, and it is the expensive part. Most open members wait
on resolving functions that exist only at runtime. The ladder puts the
members a front end that handles theme D could settle at 21 of the 34 still
open. Whether to build one is the decision for the spike. The callback rule is in
place. It settles nothing on its own, because every member a callback blocked
had another blocker, but it sharpens return aliases and lets a front end that
resolves theme D settle callback-heavy members without a separate pass.
Stored callbacks are the part of theme C it leaves open.

## Reproducing

The lowered members live in [spike/hand/](spike/hand/), one file per package.
A comment above each lowered function quotes the source it comes from. The harness
header in `harness_test.go` lists the lowering conventions. Run the check
with:

```sh
go test -tags spike ./planning/third_party/spike/hand/
```

Set `WRITE_REPORT=1` to rewrite
[spike/hand/report.txt](spike/hand/report.txt), which lists every member's
answer and blockers. The build tag keeps the check out of `go test ./...`.
