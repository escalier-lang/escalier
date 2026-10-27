# Third-party deps: deriving ownership facts from package source

**Status:** Feasibility study and draft plan. Nothing here is scheduled.

**Depends on:** [requirements.md](requirements.md), the third-party
lazy-conversion workstream. That workstream defines the override fragments
this plan writes, and it is itself gated on
[../builtins/requirements.md](../builtins/requirements.md). This plan also
reuses the analysis that [../ecma-262/requirements.md](../ecma-262/requirements.md)
built for the `std:*` builtins.

## Background

The third-party workstream converts a dependency's `.d.ts` into Escalier
declarations and seeds receiver mutability with `dts_to_esc.Classify`. The
tiers in [mutability.go](../../internal/dts_to_esc/mutability.go) that reach
a third-party method are the explicit author signals, the `get*` prefix
rule, the name heuristics, and the `&mut self` default. A `.d.ts` says
nothing about four things Escalier needs at a call site:

1. whether a method writes its receiver, which picks `&self` or `&mut self`;
2. whether a function writes a parameter in place, which picks `&` or `&mut`;
3. whether a function stores a parameter somewhere that outlives the call,
   which makes the parameter an escape;
4. whether a return value borrows the receiver or a parameter, which decides
   the return's lifetime.

Name heuristics answer the first question badly and the other three not at
all. The third-party plan leaves the rest to hand-written override fragments
under FR2.

The ECMA-262 workstream answered the same four questions for the builtins by
reading the algorithms the spec defines. ESMeta parses each algorithm into a
control-flow graph, a Scala serializer writes that graph to
[cfg.json](../../internal/ecma262/cfg.json), and the Go analysis in
[internal/ecma262/](../../internal/ecma262/) computes the facts. Over 218
instance methods its receiver claims agreed with the hand-written answer on
215, and the three disagreements were resolved, as
[../ecma-262/validation_diff.md](../ecma-262/validation_diff.md) records.

This document asks whether the same approach works when the input is a
package's own implementation instead of a specification.

## Verdict

The approach is feasible for receiver mutability and parameter mutation. It
is weak for lifetimes beyond a few simple shapes. The effort is roughly one
and a half to two times the ECMA-262 milestone. The output should be
reviewed override fragments produced by an offline tool, not facts the
compiler applies on its own at conversion time.

The sections below give the reasoning. The first phase is a spike whose
result decides whether the rest goes ahead.

## Goals

- Derive, per exported function and method of a third-party package, the
  four facts listed under Background.
- Reuse the origin map and mutation fixpoint of
  [internal/ecma262/](../../internal/ecma262/) rather than writing a second
  analysis.
- Emit the facts as `.esc` override fragments in the form FR2 of
  [requirements.md](requirements.md) defines, so the existing merge path
  applies them and a person can review them.
- Report, per package, how many members the analysis settled and how many it
  left to the name tiers. That count is the measure of whether the tool is
  worth running on a given package.

## Non-goals

- **Running inside the compiler.** The tool runs offline, as `spec-extract`
  does. Compiling Escalier code never requires Node or a JavaScript parser.
- **Unreviewed lifetimes.** Return-alias facts are review input for a person
  writing an override, matching how §8.2 of the ECMA-262 plan treats them.
  The AI-assisted refinement described below proposes lifetimes, and the
  same review applies to its proposals.
- **Throws.** The ECMA-262 throw sets work because the spec names the error
  each step constructs. Library code throws values of any type through any
  call, and the noise would swamp the signal. This can be revisited after
  the mutability facts prove out.
- **Bundled or minified packages without source maps.** The join described
  below needs to find the implementation of each declaration, and these
  packages give it nothing to work with. They stay on the name tiers.

## What gets analyzed

Most npm packages publish compiled JavaScript and `.d.ts` files. Their
TypeScript source ships only when the package includes a `src/` directory or
a source map that carries the original text in `sourcesContent`. Fetching
source from a repository by version is unreliable, since tags drift from
what was published.

The tool therefore analyzes the published JavaScript. That is also the code
that runs, so the facts describe actual behavior. The `.d.ts` is still read
for two jobs:

- resolving the target of a method call, which needs the type of the
  receiver;
- joining each analyzed function to the declaration the converter emits.

When the package does ship TypeScript, the tool can analyze that instead.
The lowering is the same once types are erased, and the type information is
more precise.

## Pipeline

The design mirrors the ECMA-262 one. A thin front end in another language
lowers code to a graph, and all Escalier-specific judgement stays in Go.

1. **Front end.** A Node tool built on the TypeScript compiler API loads the
   package's JavaScript together with its `.d.ts`. Its checker resolves
   call targets, and the tool lowers every function body to the IR in
   [cfg.go](../../internal/ecma262/cfg.go). esbuild and typescript-go are
   written in Go, but their ASTs live in `internal/` packages that another
   Go module cannot import, so neither removes the need for this step.
2. **Analysis.** The Go analysis runs the origin map and mutation fixpoint
   over the graph and classifies each function.
3. **Join.** Each fact is matched to a declaration in the converted `.d.ts`.
   This step reuses the shape of [join.go](../../internal/ecma262/join.go).
4. **Emit.** The tool writes override fragments for review, along with the
   per-package coverage report.

### Lowering JavaScript into the IR

Most JavaScript constructs map onto nodes the IR already has.

| JavaScript                       | IR                                                |
| -------------------------------- | ------------------------------------------------- |
| `o.p = v`, `o[k] = v`            | a call to `Set` with `o` at position 0            |
| `delete o.p`                     | a call to `DeletePropertyOrThrow`                 |
| `Object.defineProperty(o, ...)`  | a call to `DefinePropertyOrThrow`                 |
| `this`                           | `ThisExpr`                                        |
| `{...}`, `[...]`, `new C()`      | `AllocExpr` holding the stored operands           |
| `x.p`                            | `PropExpr`                                        |
| `return e`                       | `ReturnNode`                                      |
| `if`, loops, `switch`, `try`     | `BranchNode`                                      |
| `throw e`                        | `ThrowNode` with `Value` set                      |
| `eval`, `with`, `arguments[i] =` | `OpaqueNode`                                      |

The analysis is insensitive to control flow, so loops and early exits need
no special treatment. A write to a property is a direct mutation here, where
the spec reached it through a seeded abstract operation. That part is easier
than ECMA-262.

The IR needs three additions:

- **Resolved method calls.** A call such as `arr.push(x)` names a callee by
  receiver type and member. When the target is a builtin, its ECMA-262 fact
  applies directly, so `push` charges a mutation to `arr` and an escape to
  `x`. When the target is a function in the same package, its summary
  applies through the fixpoint the way an abstract operation's does today.
- **Unresolved calls.** A call whose target the checker cannot name gets an
  explicit node, so the analysis can apply the policy below to it.
- **Closures.** A function expression that captures a parameter becomes an
  allocation holding that parameter, which makes the capture visible as an
  escape when the closure is returned or stored.

## The hard parts

### Calls the analysis cannot resolve

This is the problem that decides the tool's value. The spec calls a closed
set of abstract operations. Library code calls through `any`-typed values,
callbacks, dynamic property lookup, and functions in other packages. To stay
sound, a call with an unknown target has to be assumed to mutate every
argument it receives. A single `log(opts)` through an untyped logger then
marks `opts` as `&mut`.

Three things limit the damage:

- **Builtin facts.** Calls into `std:*` resolve through the ECMA-262 facts,
  and in many utility libraries those are the majority of calls.
- **Summaries of dependencies.** A call into another package resolves when
  that package has facts of its own. The tool processes packages in
  dependency order and reads the facts of each dependency it has already
  analyzed.
- **Declared signals.** A callee whose `.d.ts` parameter is `Readonly<T>`
  or `readonly T[]` is taken at its word, as tier 3 of `Classify` already
  does.

The spike measures how far these get. If unresolved calls still mark most
parameters `&mut`, the facts add little over the default and the plan stops
there.

### Joining implementation to declaration

Code compiled by `tsc` without a bundler emits one `.js` file per `.d.ts`
file, and exported names line up, so the join is a lookup. Re-exports,
`export =`, CommonJS `module.exports` assignments, and declaration merging
make it harder but stay tractable through the TypeScript checker's symbol
resolution. Bundled output needs source maps to recover which function
implements which declaration. Minified output without maps cannot be
joined at all.

### Lifetimes

The origin lattice of [origin.go](../../internal/ecma262/origin.go) gives
each value one of four origins: the receiver, a parameter, a fresh
allocation, or `Unknown`. Three lifetime shapes read off it cleanly:

- `return this` borrows the receiver;
- `return param` borrows that parameter;
- `this.items.push(item)` stores `item` into the receiver, so `item` must
  outlive it.

Most other returns read a property off an input, as in
`return this.cache.get(key)`. The value comes out `Unknown`, because a
property read can run a getter. A shallow copy such as `return {...opts}`
is fresh at the top but shares everything below with `opts`, which the
`Shallow` marker records without saying which lifetime the contents carry.
A store into module-level state, such as a memoization cache, means the
value escapes for the life of the program. That is detectable, but it is
also how many libraries are written, and spelling it as `'static` would
reject ordinary callers.

The plan treats return aliasing the way the ECMA-262 plan does. The tool
reports it to a reviewer and never writes it into a fragment on its own.

### Precision the type system already settles

A parameter whose declared type is a primitive cannot be mutated, so its
disposition is a borrow regardless of what the analysis found. The join
applies this rule before emitting, the same way the ECMA-262 join settles a
return with a primitive type as owned.

## Determinations and confidence

| Fact                   | Confidence  | Applied as                                   |
| ---------------------- | ----------- | -------------------------------------------- |
| Receiver mutability    | Medium-high | Override fragment, reviewed                  |
| Parameter `&` / `&mut` | Medium      | Override fragment, reviewed                  |
| Parameter escape       | Medium      | Override fragment, reviewed                  |
| Return alias           | Low         | Comment in the fragment for the reviewer     |

Receiver mutability ranks highest because a method that never writes
`this` and passes it to no unresolved call is provably non-mutating. A
false `&mut self` fails loudly at the caller, while a false `&self` would be
silently unsound, so every uncertain answer defaults to `&mut`.

## Where the facts enter

The fragments land in the two override tiers FR2 of
[requirements.md](requirements.md) defines. A library author runs the tool
and ships the result under `node_modules/<pkg>/overrides/`. A user runs it
against a dependency and commits the result under `<project>/overrides/`.
Either way the facts reach `Classify` at tier 1, after a person has read
them, and the merge path needs no change.

A committed fact set for widely used packages is a natural follow-on,
keyed by `<pkg>@<version>` the way the cache in FR3 is. It needs its own
decision about who maintains it.

## AI-assisted refinement

The static analysis stops wherever a value comes out `Unknown` or a call
goes unresolved, and those are the places where lifetimes matter most. A
model reading the source can often answer what the analysis cannot. Two
examples:

- From the surrounding code it can tell that `this.cache.get(key)` returns
  a value the cache keeps alive.
- From a callback's documented contract it can tell that the callback is
  never retained after the call.

A skill that people run against a dependency could fill those gaps. It
would take three inputs:

1. the fragments and coverage report the tool emits;
2. the list of members the analysis left unsettled, with the site that
   stopped it;
3. the package's source.

It would produce proposed edits to the fragments. Each edit would cite the
source lines it rests on, so the reviewer reads evidence rather than a bare
claim.

A model's answer is a guess, not a proof, so the skill sits behind three
checks:

- **Proven facts win.** The skill may settle a member the analysis left
  open. It may never contradict a fact the analysis proved, such as
  weakening a proven `&mut` to `&` or dropping a proven escape. The emitter
  rejects an edit that does.
- **Dynamic checks where they exist.** When the package has a test suite,
  the tool can run it with instrumented arguments and test a proposed
  claim against what the code actually does.
  - A `Proxy` around each argument records any write, which tests a claim
    of `&`.
  - A `WeakRef` to each argument, checked after the call returns and a
    forced collection, shows whether the library kept the argument alive,
    which tests a claim of no escape.

  This is the dynamically observed ground truth that §9.4 of the ECMA-262
  plan describes, applied per package.
- **Human review.** The edits land in the same override fragments as the
  static facts, so they get the same review before they ship.

The skill is cheap to build once phase 5 exists, since its inputs and
outputs are artifacts the pipeline already produces. It could also run
without the static analysis, straight against the `.d.ts` and the source,
but then nothing would separate proven answers from guessed ones, and the
first check above would have nothing to check against.

## Phases

| Phase | Work                                    | Gate                                                                                         |
| ----- | --------------------------------------- | -------------------------------------------------------------------------------------------- |
| 1     | Spike                                   | Hand-lower about ten packages and report the fraction of members settled. See below.         |
| 2     | Front end                               | Node tool lowers a package to the extended IR and round-trips the schema check.              |
| 3     | IR and analysis generalization          | Resolved calls, unresolved calls, closures, and cross-package summaries pass unit tests.     |
| 4     | Join                                    | Every exported function of an unbundled `tsc` package joins to a declaration or is reported. |
| 5     | Fragment emitter and coverage report    | Fragments parse and merge through the FR2 path for the spike packages.                       |
| 6     | Validation                              | Facts diffed against hand-written overrides for the spike packages, every disagreement triaged. |
| 7     | AI refinement skill                     | Skill proposes edits for the unsettled members of the spike packages, each citing source, none contradicting a proven fact. |
| 8     | Instrumented test run                   | `Proxy` and `WeakRef` checks confirm or refute the skill's claims against each spike package's own test suite. |

Phases 2 and 3 carry most of the cost. The front end is the larger of the
two, because it has to handle the whole of JavaScript rather than the
restricted step grammar ESMeta lowers. The ECMA-262 milestone took about
eighteen PRs. This plan has fewer phases, but phases 2 and 3 each split
into several PRs of a size similar to §3 and §4 of that milestone.

### The spike

The spike decides whether the plan continues. It needs no front end. A
script lowers the exported functions of about ten packages by hand or with
a rough TypeScript-API prototype, and the existing analysis runs over the
result with the three IR additions stubbed in.

The packages should cover the shapes that matter:

- pure utility functions, such as `lodash-es` and `date-fns`;
- a builder or fluent API that returns `this`, such as `zod`;
- a stateful class library with collections inside it;
- a callback-heavy library, such as `express` middleware;
- a package compiled from TypeScript with source maps, to exercise the join.

The spike reports, per package:

1. the fraction of members whose receiver and parameters were settled
   rather than left to the default;
2. the calls that went unresolved, grouped by why;
3. a hand check of twenty settled answers against the implementation.

If fewer than about half the members of the utility packages come out
settled, the unresolved-call policy is too costly and the plan stops.

## Open questions

- Should receiver facts with no unresolved calls skip review and enter as a
  tier of their own, the way ECMA-262 facts sit at tier 5? That trades
  review effort for the risk of a wrong `&self` shipping silently.
- Which package owns the extended IR? It could stay in
  `internal/ecma262/` or move to a shared package once a second front end
  exists.
- How does a reviewed fragment survive a version bump of the package it
  describes? The ECMA-262 curated layer records a digest of each algorithm
  for this purpose, and the same idea applies per function here.
- Where does the refinement skill live? A skill under `.claude/skills/` in
  this repo serves Escalier's own curation. Users refining their own
  dependencies need it shipped with the CLI or published on its own.
