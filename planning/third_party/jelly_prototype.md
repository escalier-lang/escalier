# Jelly prototype: call targets for the third-party front end

**Status:** Done. All five days of the one-week plan in
[jelly_evaluation.md](jelly_evaluation.md) ran. Day 5 used a stand-in for
Jelly's own soundness check, as its section explains.

The prototype asks whether Jelly can supply the call targets the hand-lowering
check in [hand_lowering.md](hand_lowering.md) could not resolve, and how many
open members that settles. The scripts are in
[spike/jelly/](spike/jelly/README.md).

## Result

Jelly resolves call targets well. Inside the code each driver reaches, it gives
a target to between 83% and 100% of each package's call sites that are not
calls into native code. Against the edges a traced run of each driver took,
Jelly has 93%. It misses 4 edges into package code silently, at sites where it
names other targets. Its other 205 silent misses all go to functions the
caller passed in, nearly all of them in zod.

Resolving a call does not settle the member that makes it. Feeding Jelly's
targets into the fixtures, with each target lowered by hand, settles one more
member: 23 of 56 against 22 without Jelly. Before the targets were lowered,
the blockers Jelly resolved suggested an upper bound of 28. The gap is the
targets themselves. They bring their own blockers:

- **Captured state.** lru-cache's TTL hooks are closures that write `starts`,
  `ttls` and `sizes`, arrays the constructor created and also stored on
  `this`. Lowered on its own, a captured array is a value the closure does not
  bind, so each write blocks every parameter.
- **Host APIs.** The same closures call `setTimeout`, `clearTimeout` and
  `performance.now`.
- **Further dispatch.** rxjs's `observer.next` resolves to seven `next`
  methods, and each of those dispatches again through its destination. zod's
  `new ZodOptional(...)` resolves to the class body `$constructor` builds,
  which calls an initializer with 10 candidates. `parse` reaches a
  `schema._zod.run` call with 50.

Callbacks matter more than call targets for this sample. With Jelly's targets,
treating a call through a parameter as the caller's responsibility settles 6
more members, for 29 of 56. Callbacks still block 18 of the 33 open members.

| Run                                         | Settled | Open members a theme blocks: A / B / C / D / E / F |
| ------------------------------------------- | ------- | -------------------------------------------------- |
| After the lattice changes                   | 22/56   | 11 / 2 / 16 / 29 / 10 / 4                          |
| Jelly's targets, before lowering them       | 28/56   | an upper bound                                     |
| Jelly's targets, lowered                    | 23/56   | 12 / 5 / 18 / 8 / 12 / 10                          |
| The same, with the callback rule            | 29/56   | not measured                                       |

The themes are the ones in [hand_lowering.md](hand_lowering.md): A the origin
lattice, B module-level and captured state, C callbacks, D dynamic dispatch, E
code outside the package, and F not lowered. Theme F grew because the rxjs and
zod targets fan out into code this check does not lower. Every answer the
analysis gave still matches the source except `app.enabled`, the sound but
over-strict `&mut self` the earlier run found.

## Days 1–2: drivers and runs

`gen-driver.mjs` writes a driver per package from its `.d.ts`. The driver
calls every export, constructs every class, and calls the methods of what it
gets back. Getting usable drivers took several rules, each found by a run that
failed or analyzed the wrong thing:

- **Skip members the TypeScript lib declares.** Without this, every returned
  array had its `map` and `push` called, and zod's driver ran to 78,000
  statements.
- **Skip a default export that repeats the named ones, and objects with more
  than 150 members.** lodash's chainable wrapper otherwise ran Jelly out of
  memory.
- **Walk exported objects, not only functions.** express's `res.json` only
  resolved `this.get` and `this.send` once the driver called
  `express.response.json`.
- **Call each instance's methods twice, with teardown methods last.**
  Approximate interpretation runs the driver, so a method that reads state,
  such as `emit`, has to run after the one that writes it, such as `on`.
- **Restrict zod to the exports the sample uses.** Its full surface ran Jelly
  out of memory. The driver covers `string`, `optional`, `object`, `registry`
  and `number`.
- **Fill object arguments with a value for each property.** A constructor that
  requires an option, such as lru-cache's `max`, throws on `{}`, and the run
  day 5 traces only sees code that runs. The literals also give Jelly the
  option callbacks a caller would pass.

Jelly ran with `--approx --no-callgraph-external` over each driver:

| Package         | Driver statements | Time  | Memory  |
| --------------- | ----------------- | ----- | ------- |
| `mitt`          | 7                 | 1.6 s | 42 MB   |
| `uuid`          | 12                | 1.6 s | 53 MB   |
| `eventemitter3` | 22                | 1.4 s | 43 MB   |
| `lru-cache`     | 83                | 2.2 s | 60 MB   |
| `immer`         | 38                | 2.2 s | 68 MB   |
| `express`       | 710               | 8.5 s | 228 MB  |
| `rxjs`          | 409               | 11 s  | 435 MB  |
| `date-fns`      | 255               | 7.1 s | 128 MB  |
| `lodash-es`     | 322               | 68 s  | 2.2 GB  |
| `zod`           | 2,347             | 17 s  | 609 MB  |

## Day 3: call sites

`report.mjs` counts each package's own call sites that Jelly saw. A site with
no callee is unresolved when Jelly's `--zeros` report lists it and a call into
native or external code otherwise. Positions map back through each package's
source maps, so lru-cache's minified build reads as `src/index.ts`.

| Package         | Sites | One target | Several | Unresolved | Native or external | Resolved, of non-native |
| --------------- | ----- | ---------- | ------- | ---------- | ------------------ | ----------------------- |
| `mitt`          | 16    | 2          | 2       | 0          | 12                 | 4/4                     |
| `uuid`          | 143   | 78         | 1       | 12         | 52                 | 79/91                   |
| `eventemitter3` | 38    | 14         | 0       | 2          | 22                 | 14/16                   |
| `lru-cache`     | 281   | 139        | 31      | 14         | 97                 | 170/184                 |
| `immer`         | 575   | 334        | 31      | 11         | 199                | 365/376                 |
| `express`       | 470   | 228        | 7       | 49         | 186                | 235/284                 |
| `rxjs`          | 3,232 | 1,877      | 605     | 60         | 690                | 2,482/2,542             |
| `date-fns`      | 2,629 | 1,824      | 35      | 44         | 726                | 1,859/1,903             |
| `lodash-es`     | 4,107 | 3,494      | 277     | 62         | 274                | 3,771/3,833             |
| `zod`           | 5,137 | 3,315      | 164     | 613        | 1,045              | 3,479/4,092             |

The native column mixes builtins, which the ECMA-262 facts already answer,
with host and dependency code, which they do not. Sites with several targets
are where context-insensitivity shows. A callback parameter collects every
function any caller passes. lodash's `mapValues` calls an `iteratee` with 378
targets, most of them the driver's own placeholder callbacks.

## Day 4: the fixtures' blockers

[spike/jelly/sites.json](spike/jelly/sites.json) lists the 41 call sites the
fixtures leave unresolved, with what Jelly said about each and how this
prototype reads that answer:

| Outcome    | Sites | Meaning                                                                      |
| ---------- | ----- | ---------------------------------------------------------------------------- |
| resolved   | 19    | targets in the package, such as lru-cache's TTL closures                     |
| builtin    | 2     | a native builtin the ECMA-262 facts answer, such as `Object.defineProperty`  |
| caller     | 11    | a value the caller supplies, such as an option callback or the caller's date |
| dependency | 2     | another package, such as express's `router`                                  |
| external   | 2     | host code, such as Node's `http` or `AbortController`                        |
| unresolved | 5     | no target, though the code is the package's own                              |

[spike/hand/zz_jelly_test.go](spike/hand/zz_jelly_test.go) replaces each
resolved site in the fixtures with its targets, lowered by hand. Set
`JELLY=1` to run the check over them. The report is
[spike/hand/report-jelly.txt](spike/hand/report-jelly.txt).

The drivers gained their option literals after this step. Re-reading the sites
against the later run changes no outcome. lru-cache's `dispose` and uuid's
`rng` now resolve to the driver's own option callbacks instead of to nothing,
which was already how this table read them.

## What the prototype found

- **Captured state needs points-to, not only call targets.** Jelly's
  `--tokens-json` output does link lru-cache's captured `starts` to
  `this.#starts`, since both point to the same abstract object. That object
  stands for every `ZeroArray` any cache creates, though. The link says the
  array may belong to some cache, not that it belongs to this one. A sound use
  of it would charge a write to every parameter and receiver that may reach the
  written object, which is a points-to charging rule rather than the origin
  lattice the analysis has now.
- **Callbacks are the next lever.** They block 18 of the 33 open members after
  Jelly, more than any other theme. The caller's-responsibility rule settles
  6 of them.
- **Context-insensitive targets fan out.** A callback or an initializer that
  many callers share collects all of their functions. Summarizing such a call
  with the union of its targets is sound but merges unrelated effects.
- **Frameworks keep objects Jelly never sees.** express sets `res.app` while
  handling a request, so `app.get('json escape')` has no target, and the
  driver cannot construct a request.
- **Calls through a caller's object are where Jelly is unsound.** Day 5's
  silent misses are almost all calls to methods of an object the caller
  passed. The callback rule would treat such a call as the caller's, which
  makes the miss harmless. Without the rule, a summary built from Jelly's
  targets is unsound there.
- **The evaluation overstated one row.** [jelly_evaluation.md](jelly_evaluation.md)
  listed eventemitter3's `listeners.fn.call(...)` as resolved under `--approx`.
  Neither its own runs nor these give that call a target. The dynamic key
  `this._events[evt]` defeats it.

## Day 5: silent misses

Day 5 was to measure Jelly's silent misses against a dynamic call graph from
each package's test suite. Jelly records that graph under graal-nodejs, and
GitHub, where graal-nodejs is released, refuses this environment's requests.
The npm tarballs also ship no tests.

A traced run of each driver stands in for both. `trace.sh` runs it in three
steps:

1. `instrument.mjs` rewrites a copy of the drivers and packages with Babel.
   Each call pushes its site once its arguments are evaluated, and each
   function records the site on top of the stack when it starts.
2. Each driver runs under `tracer.cjs`, which records the edges.
3. `compare.mjs` diffs them against Jelly's graph for the same driver.

This measures recall on the calls the drivers make, not on a test suite's.

| Package         | Edges taken | Found | Silent, to package | Silent, to caller | Visible | Unmatched |
| --------------- | ----------- | ----- | ------------------ | ----------------- | ------- | --------- |
| `mitt`          | 0           | 0     | 0                  | 0                 | 0       | 0         |
| `uuid`          | 16          | 16    | 0                  | 0                 | 0       | 0         |
| `eventemitter3` | 7           | 7     | 0                  | 0                 | 0       | 0         |
| `lru-cache`     | 63          | 56    | 0                  | 0                 | 2       | 5         |
| `immer`         | 49          | 48    | 0                  | 0                 | 0       | 1         |
| `express`       | 86          | 83    | 0                  | 0                 | 3       | 0         |
| `rxjs`          | 599         | 599   | 0                  | 0                 | 0       | 0         |
| `date-fns`      | 602         | 595   | 0                  | 4                 | 2       | 1         |
| `lodash-es`     | 1,068       | 1,065 | 2                  | 1                 | 0       | 0         |
| `zod`           | 1,482       | 1,205 | 2                  | 200               | 72      | 3         |
| Total           | 3,972       | 3,674 | 4                  | 205               | 79      | 10        |

An edge is silent when Jelly names other targets for its site but not this
one, so nothing marks the site as open. A visible edge is at a site Jelly
gives no target, which reads as open already. An unmatched edge names a
function Jelly's output does not. All 10 are a class whose constructor the
tracer names by the method and Jelly by the class.
[spike/jelly/misses.txt](spike/jelly/misses.txt) lists every edge outside the
found column.

The silent misses into package code are few:

- **lodash's `stringToPath` and `template`.** Each passes a callback to the
  native `String.prototype.replace`. The receiver is untyped, so Jelly takes
  `string.replace` to be lodash's own `replace` export and misses the
  callback the native function runs.
- **zod's two calls into `util.js`.** Jelly names only driver functions at
  those sites.

The silent misses to caller code are the ones that matter for this analysis.
zod's 200 are calls to methods of objects the driver passed in, such as the
`metadata` registry `toJSONSchema` takes. The driver passed a literal with
`get`, `has` and `add` methods. At run time zod calls them, but Jelly names
only zod's own `$ZodRegistry` methods. TypeScript's types are structural, so
any caller can pass such an object. A summary built from Jelly's targets would
claim the call reaches `$ZodRegistry.get` alone, which is unsound if the
caller's `get` writes anything.

Two limits make the run a lower bound on what a test suite would find:

- **It only sees code that runs.** A driver argument that misses a branch's
  condition leaves that branch out. mitt's run took no edge from mitt's own
  sites at all. The driver passes a fresh `{}` as the event key on each
  call, so `emit` never finds the handler `on` registered.
- **The drivers are the same ones Jelly analyzed.** A test suite exercises
  the package the way a real caller does, and Jelly's approximate
  interpretation never ran it.
