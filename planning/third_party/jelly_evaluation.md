# Call-target tools for the third-party front end

**Status:** Evaluation. It weighs tools that could supply call targets to the
front end in [source_analysis.md](source_analysis.md), against the blockers
[hand_lowering.md](hand_lowering.md) found.

## Short answer

Jelly is the only candidate worth prototyping with. It is a maintained, BSD-licensed Node tool that already emits a JSON call graph keyed by source ranges, and it resolved most of theme D's patterns on the hand-lowering packages when run locally. It has two catches. It is a whole-program analysis, so a library needs a small generated driver that constructs its classes. It is also deliberately unsound, so a missing callee looks the same as a complete answer. The TypeScript checker stays in the pipeline for the join and for typed receivers. It cannot resolve functions that exist only at runtime.

## What I ran

I installed `@cs-au-dk/jelly@0.13.0` outside the repository and ran it on the hand-lowering packages, with a small driver per package and a script that prints each call site's source text next to its callees. The npm package was last published 2026-05-11. The drivers and outputs are not committed.

| Blocker from hand_lowering.md | Package and call | Plain Jelly | `--approx` |
| --- | --- | --- | --- |
| Closure stored in a field | lru-cache `this.#isStale(i)` | resolved with a driver, to the real arrow and the no-op default | not needed |
| Value computed at module load | lodash `stringToPath(...)` → `memoize` → `func.apply` | resolved to `memoized`, then `_stringToPath` | not needed |
| Runtime-built class | zod `s.parse`, `s.brand` | none | right target plus about 24 spurious ones |
| Same, inside `$constructor` | zod `initializer(inst, def)` | 10 candidates, context-insensitive merge | — |
| Call through an interface | rxjs `observer.next`, `destination.error` | resolved to the 4–5 in-package implementations | — |
| Function read out of state | eventemitter3 `listeners.fn.call(...)` | none, dynamic key `this._events[evt]` | none; [jelly_prototype.md](jelly_prototype.md) re-ran it |
| CommonJS prototype mixin | express `app.set`, `this.set` | none, because `merge-descriptors` copies with dynamic names | resolved precisely |

There were three surprises.

1. Library mode gives `this` an "unknown" token instead of an instance. Every `this.#field(...)` call in lru-cache came out with no callee until a four-line client called `new LRUCache({...})` and its methods. The cause is in `lib/analysis/escaping.js`, which seeds exported functions' parameters and `this` with an unknown access path.
2. Precision depends on the entry set. When all 644 lodash-es modules are entry points, `isSymbol(value)` resolves to about 35 functions, because `lodash.default.js` pollutes the module objects. With `get.js` and `set.js` as the only entries, every call is exact.
3. By default the graph adds heuristic edges from calls into unknown code to the callbacks passed in. For example, express's `app.use(cb)` got an edge to `cb`. Pass `--no-callgraph-external`.

Driver runs finished in under 3 s each, including express with 65 dependencies. Using every rxjs file as an entry took 100 s and 1.9 GB.

## The candidates

**Jelly.** It runs a flow-insensitive, field-sensitive points-to analysis with access paths, and it models ES2022, CommonJS, ESM and TypeScript through Babel. Node's own library is treated as unknown code. `--approx` runs an approximate interpretation. It executes module initialization and functions with proxies, records dynamic property reads and writes, and feeds them back to the static analysis as hints. The authors report that this raised recall from 75.9% to 88.1% on 36 projects with dynamic call graphs, with negligible precision loss. They also report 17.7% more resolved call sites across 141 Node packages ([PLDI 2024](https://pldi24.sigplan.org/details/pldi-2024-papers/48/Reducing-Static-Analysis-Unsoundness-with-Approximate-Interpretation)). The README says it is intentionally not fully sound ([repo](https://github.com/cs-au-dk/jelly)). `-j` writes `files`, `functions` and `calls` as `file:line:col:line:col` ranges, plus `call2fun` and `fun2fun` edges. `--tokens-json` dumps points-to sets per constraint variable, which could supply function-value flow beyond call edges. `-d` and `-s` build a dynamic call graph under graal-nodejs and measure recall against it.

**ACG**, from Feldthaus et al. at ICSE 2013, merges every property named `f` into one location. It needs no driver, but every `.next` call would reach every `next` in the program. Its descendants `js-callgraph` and [jscg](https://www.npmjs.com/package/jscg) add ES6, and Jelly's README lists ACG as an ancestor.

**TAJS** is a precise abstract interpreter that fully covers only ES3, with ES5 in part. Newer syntax goes through Babel, and activity stopped around 2020 ([repo](https://www.github.com/cs-au-dk/TAJS)). It will not scale to zod or rxjs.

**WALA JavaScript** has pointer analysis, but its JavaScript documentation is thin ([repo](https://github.com/WALA/wala)). A 2024 study found that only two of the tools it compared could analyze current multi-file Node modules ([study](https://arxiv.org/html/2405.07206v1)). A secondary source puts WALA's recall at 62% ([2025 paper](https://web3.arxiv.org/pdf/2506.18191)).

**Closure Compiler** type passes such as `DisambiguateProperties` assume Closure-style code and a whole program ([limitations](https://developers.google.com/closure/compiler/docs/limitations)). They export no call graph.

**CodeQL** builds a JavaScript and TypeScript call graph on type tracking, and `getACallee` carries an imprecision score ([docs](https://codeql.github.com/codeql-standard-libraries/javascript/semmle/javascript/dataflow/Nodes.qll/predicate.Nodes$InvokeNode$getACallee.1.html)). It skips `node_modules` by default ([discussion](https://github.com/github/codeql/discussions/18901)). Getting output means building a database per package and writing QL queries, and the license restricts use on closed code. I found no npm recall numbers for it.

**Joern with jssrc2cpg** links calls by static name and within a file, with type hints on top ([docs](https://docs.joern.io/frontends/javascript/), [passes](https://javadoc.io/static/io.joern/jssrc2cpg_3/1.1.926/io/joern/jssrc2cpg/passes.html)). It has no points-to analysis, so it cannot resolve field-stored closures.

**The TypeScript checker** is the baseline. `getResolvedSignature` returns a declared signature, not a function value. For `this.#isStale` that is the field's type, and in plain JavaScript it is `any`. The checker covers the join and typed builtins such as `arr.push`, and nothing in theme D. typescript-go ships as TypeScript 7, but its programmatic API is still being built ([Visual Studio Magazine](https://visualstudiomagazine.com/Articles/2026/03/06/TypeScript-6-0-RC-Bridges-to-Go-Based-Future.aspx)).

I found nothing relevant in Semgrep or in Datalog analyses for JavaScript.

| Tool | Computes | Runtime and license | Maintained | Theme D coverage |
| --- | --- | --- | --- | --- |
| Jelly | points-to and call graph, plus approx hints | Node, BSD-3 | yes, 0.13.0 in 2026-05 | most of it, given a driver |
| ACG / jscg | field-based call graph | Node, varies | lightly | resolves by name only, so very imprecise |
| TAJS | abstract interpretation | Java, Apache-2 | no, ~2020 | precise but not modern JS |
| WALA JS | points-to | Java, EPL | core yes, JS weak | poor on modern Node |
| Closure Compiler | type-based passes | Java, Apache-2 | yes | no export, wrong assumptions |
| CodeQL | type tracking and data flow | Java CLI + QL, restricted | yes | partial, heavy to integrate |
| Joern | CPG and name-based linking | Scala, Apache-2 | yes | names only |
| TS checker | declared types | Node, Apache-2 | yes | none, but needed for the join |

## How Jelly maps onto the blockers

- **Closures stored in fields.** Resolved once a driver constructs the class. The default stub shows up as a second target, and that is correct, since both can run.
- **Values computed at module load.** Resolved for lodash. Zod's `$constructor` needs `--approx` and comes back imprecise. The extra targets would union in other schemas' summaries, which makes the result over-approximate, not wrong.
- **Calls through interfaces.** Resolved to the package's own implementations. A callback the caller supplies stays a caller-supplied value, which is theme C's policy anyway.
- **`bind`, `call` and `apply`.** Modeled, as lodash's `func.apply` shows.
- **Untyped JavaScript.** CommonJS and dynamic mixins resolve under `--approx`. Plain JavaScript gains the most over the TypeScript checker.
- **Code outside the package.** Jelly follows `require` into dependencies, but Node built-ins stay unknown. Theme E still needs fact tables.

The main risk is a silent miss. A call site with some edges but not all of them would produce an unsound `&` with nothing marking it. Jelly's `-s` comparison against the package's own test suite, run under graal-nodejs, measures that miss rate.

## Recommendation and one-week prototype

Prototype with Jelly as a call-target oracle beside the TypeScript-API front end. Jelly produces the callees, and the checker does the join and the typed builtins.

1. **Days 1–2.** Generate a driver per package from its `.d.ts`. For each exported function, call it with fresh objects. For each exported class, construct it and call every method. An alternative is a small patch to `escaping.js` that gives an escaping constructor's `this` an allocation token. Run Jelly with `--approx --no-callgraph-external`, using only the package's public entry points.
2. **Day 3.** Map `call2fun` onto front-end call nodes by matching source ranges. Count the call sites that come out with exactly one target, with several, and with none.
3. **Day 4.** Feed the resolved callees into the hand-lowered fixtures for the ten packages and re-run the settle count. After the lattice changes, the ladder in hand_lowering.md puts the members a theme D solution could settle at 21 of the 34 still open.
4. **Day 5.** Run `-d` and `-s` on two packages with test suites, lru-cache and eventemitter3, to measure recall. Record any edge the dynamic graph has and Jelly lacks, since those are the unsound cases.

The prototype passes if most theme-D sites get a target, the dynamic misses are rare and nameable, and the zod-style over-approximation does not turn the settled members back to `&mut`.
