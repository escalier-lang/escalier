# Third-party source analysis: hand-lowering check

**Status:** Done. This is the temperature check that precedes the spike in
[source_analysis.md](source_analysis.md).

The check lowered 56 exported members of ten npm packages by hand into the
control-flow IR that [internal/ecma262/](../../internal/ecma262/) reads. It
then ran the unmodified ECMA-262 mutation analysis over them. The question was
what fraction of members the analysis settles. A member is settled when every
parameter, and the receiver for a method, comes out as a proven `&` or a
proven `&mut` rather than unknown.

## Result

The analysis as it stands settles 14 of the 56 members. One of the 14 has only
primitive parameters, so 13 of 55 non-trivial members settle. The plan's gate
asked for about half of the utility packages' members. `date-fns` settled 0 of
6 and `lodash-es` 1 of 6, so the current analysis is well short of the gate.

Every answer the analysis did give is correct. All 14 settled members match
what the source does. So do the 14 proven positions inside members that stayed
open, such as `pullAll`'s `&mut` array. The analysis is sound but blind. It
never claimed a wrong answer, and it withheld most of them.

Receivers are the determination the plan rated highest. Of 30 instance
members, 16 receivers settled, but 10 of those are proven `&mut self`. Only 6
methods came out as a proven `&self`, and they are the simplest ones in the
sample, such as `BehaviorSubject.getValue` and `ZodType.brand`.

| Package         | Settled | Shape                                       |
| --------------- | ------- | ------------------------------------------- |
| `date-fns`      | 0/6     | pure functions over a shared `toDate`       |
| `lodash-es`     | 1/6     | untyped utility helpers, many layers deep   |
| `mitt`          | 1/4     | methods are closures over a captured map    |
| `eventemitter3` | 2/6     | ES5 prototype methods over `this._events`   |
| `express`       | 2/7     | CommonJS prototype objects, many deps       |
| `immer`         | 2/5     | class-field arrows, module-level caches     |
| `lru-cache`     | 1/5     | private fields holding closures             |
| `rxjs`          | 2/6     | classes, operators built from closures      |
| `uuid`          | 1/5     | compiled TypeScript, host `crypto` calls    |
| `zod`           | 2/6     | classes built at runtime by `$constructor`  |

The return alias is known for 24 of 56 members. Nearly all of those are a
fresh value or a primitive. The borrows it found are `return this` and
`freeze`'s `return obj`, which matches the plan's expectation that lifetimes
beyond those shapes stay out of reach.

## What blocks the rest

Each open member was blocked by one or more causes, which group into six
themes. The first column counts the open members a theme blocks at all. The
second counts those it blocks alone.

| Theme                                                       | Blocks | Alone |
| ----------------------------------------------------------- | ------ | ----- |
| A. Origin lattice: writes through properties, call results  | 23     | 4     |
| B. Module-level and captured state                          | 10     | 3     |
| C. Callbacks the caller supplies                            | 16     | 1     |
| D. Dynamic dispatch and functions built at runtime          | 29     | 4     |
| E. Code outside the package                                 | 11     | 0     |
| F. Not lowered for this check                               | 4      | 0     |

Removing the themes one at a time, in that order, settles this many of the 42
open members:

| Themes removed | Open members that would settle |
| -------------- | ------------------------------ |
| A              | 4                              |
| A–B            | 9                              |
| A–C            | 10                             |
| A–D            | 28                             |
| A–E            | 38                             |

A member that settles on this ladder is no longer blocked. Its answer could
still turn out to be `&mut`.

### A. The origin lattice

The analysis gives every value one of four origins: the receiver, a
parameter, a fresh allocation, or Unknown. A write is charged to its holder
only when the written value has one of the first three. Library code writes
through values the lattice cannot place in four ways:

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

These are changes to `internal/ecma262`, not to a front end. Escalier's `mut`
is deep ([docs/08_mutability.md](../../docs/08_mutability.md)), so a write
through a property of the receiver is a write to the receiver. A property read
off the receiver or a parameter can therefore be interior to it. A join of a
parameter with a fresh value can stay the parameter, since writing the fresh
half is invisible to the caller. A call into a summarized function can take
its result's origin from the callee's return alias.

### B. Module-level and captured state

A name the function does not bind reads as Unknown, so writing through it
blocks every parameter. The writes found were:

- `RegExp.prototype.test` on a module-level regex, which the ECMA-262 facts
  rightly charge, since it writes `lastIndex`;
- memo caches such as `immer`'s `cachedCtorStrings` and lodash's
  `stringToPath`;
- the map `mitt`'s methods capture.

A module-level origin, distinct from Unknown, would let a write to it pass
without blocking the parameters. `mitt` needs the opposite answer. Its
captured map is the receiver's state, so writing it should charge the
receiver. A closure over the object a factory returns is the same shape as
`this`.

### C. Callbacks

Some callbacks belong to the caller, such as `immer`'s `recipe` and the
`options.in` context function that every `date-fns` member reaches through
`constructFrom`. Others are internal higher-order helpers that the package
always calls with a closure it writes itself, such as lodash's `arrayMap`,
`immer`'s `each` and `rxjs`'s `errorContext`. Treating a call through a
parameter as the caller's responsibility settles the first kind. The second
kind needs the callee's summary to take the closure argument's effects into
account.

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
  branch too. That member is open for other reasons, so it causes no
  mismatch here. Once theme A charges `this.settings[setting] = val` to the
  receiver, it would come out a sound but wrong `&mut self`.

## What this suggests

Themes A and B are changes to the existing analysis, and they also benefit the
builtins. The hand-lowered members are a fixture that can measure those
changes without a front end. Making them first and re-running this check
would show how far the lattice alone moves the numbers. The ladder puts it at
9 of 42 open members.

Theme D decides the rest, and it is the expensive part. Most of the remaining
members wait on resolving functions that exist only at runtime. Settling the
ladder's 28 would take a front end that handles that well. Whether to build
one is the decision for the spike.

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
