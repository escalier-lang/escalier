# Jelly prototype: call targets for the third-party front end

**Status:** Days 1–4 of the one-week plan in
[jelly_evaluation.md](jelly_evaluation.md) are done. Day 5 is blocked, as the
last section explains.

The prototype asks whether Jelly can supply the call targets the hand-lowering
check in [hand_lowering.md](hand_lowering.md) could not resolve, and how many
open members that settles. The scripts are in
[spike/jelly/](spike/jelly/README.md).

## Result

Jelly resolves call targets well. Inside the code each driver reaches, it gives
a target to between 83% and 100% of each package's call sites that are not
calls into native code.

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

Jelly ran with `--approx --no-callgraph-external` over each driver:

| Package         | Driver statements | Time  | Memory  |
| --------------- | ----------------- | ----- | ------- |
| `mitt`          | 7                 | 1.3 s | 42 MB   |
| `uuid`          | 12                | 1.5 s | 53 MB   |
| `eventemitter3` | 22                | 1.4 s | 43 MB   |
| `lru-cache`     | 83                | 1.8 s | 62 MB   |
| `immer`         | 38                | 2.0 s | 64 MB   |
| `express`       | 710               | 6.2 s | 177 MB  |
| `rxjs`          | 409               | 7.5 s | 246 MB  |
| `date-fns`      | 255               | 6.7 s | 122 MB  |
| `lodash-es`     | 322               | 61 s  | 2.2 GB  |
| `zod`           | 2,347             | 15 s  | 507 MB  |

## Day 3: call sites

`report.mjs` counts each package's own call sites that Jelly saw. A site with
no callee is unresolved when Jelly's `--zeros` report lists it and a call into
native or external code otherwise. Positions map back through each package's
source maps, so lru-cache's minified build reads as `src/index.ts`.

| Package         | Sites | One target | Several | Unresolved | Native or external | Resolved, of non-native |
| --------------- | ----- | ---------- | ------- | ---------- | ------------------ | ----------------------- |
| `mitt`          | 16    | 2          | 2       | 0          | 12                 | 4/4                     |
| `uuid`          | 143   | 75         | 1       | 15         | 52                 | 76/91                   |
| `eventemitter3` | 38    | 14         | 0       | 2          | 22                 | 14/16                   |
| `lru-cache`     | 281   | 119        | 30      | 14         | 118                | 149/163                 |
| `immer`         | 575   | 334        | 31      | 11         | 199                | 365/376                 |
| `express`       | 470   | 228        | 7       | 49         | 186                | 235/284                 |
| `rxjs`          | 3,232 | 1,992      | 480     | 66         | 694                | 2,472/2,538             |
| `date-fns`      | 2,629 | 1,850      | 8       | 45         | 726                | 1,858/1,903             |
| `lodash-es`     | 4,107 | 3,494      | 277     | 62         | 274                | 3,771/3,833             |
| `zod`           | 5,137 | 3,316      | 161     | 613        | 1,047              | 3,477/4,090             |

The native column mixes builtins, which the ECMA-262 facts already answer,
with host and dependency code, which they do not. Sites with several targets
are where context-insensitivity shows. A callback parameter collects every
function any caller passes. lodash's `mapValues` calls an `iteratee` with 377
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
- **The evaluation overstated one row.** [jelly_evaluation.md](jelly_evaluation.md)
  listed eventemitter3's `listeners.fn.call(...)` as resolved under `--approx`.
  Neither its own runs nor these give that call a target. The dynamic key
  `this._events[evt]` defeats it.

## Day 5: blocked

Day 5 was to measure Jelly's silent misses against a dynamic call graph from
each package's test suite. Jelly records that graph under graal-nodejs, and
GitHub, where graal-nodejs is released, refuses this environment's requests.
The npm tarballs also ship no tests.

A driver run under an instrumented Node would stand in for both. A Babel pass
over the package files could record, at each function entry, the call site
that entered it. Diffing those edges against Jelly's would show which targets
Jelly missed for the calls the driver makes. That measures recall on the
driver's calls only, not on a test suite's.
