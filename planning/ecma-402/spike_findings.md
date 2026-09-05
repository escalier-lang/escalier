# ECMA-402 feasibility spike: findings

Answers one question: can the ECMA-262 pipeline under
[tools/spec-extract/](../../tools/spec-extract/) derive method facts from
ECMA-402 the way it does from ECMA-262, or does ESMeta need substantial work
first?

**Verdict: it reads ECMA-402 with four small, nameable changes, none of them
inside ESMeta's analysis.** The spike merged ECMA-402's ecmarkup sources into
the pinned `spec.html`, ran the maintained
`extract → compile → build-cfg → serialize` pipeline over the merged document,
and then ran the Go analysis over the graph it produced. 193 ECMA-402
algorithms came through, 77 of them builtins, and the analysis settled the
throw and reject axes for every one of them. What ECMA-402 costs is coverage,
not machinery: its algorithms carry unformalized steps at roughly five times
the ECMA-262 density, and that lands almost entirely on the `returns` axis.

[reproduce_spike.sh](reproduce_spike.sh) runs the whole thing.
[spike_harness/](spike_harness/) holds the two programs it copies into the
build, and [spike_evidence/](spike_evidence/) holds the output this document
reads from.

## Toolchain and pinned revisions

| Component | Revision |
| --------- | -------- |
| ESMeta | `7d237fd1680f473e674320cc97932702d950fa98`, the `tools/spec-extract/esmeta` submodule |
| ECMA-262 | `84b38ad852ff426795fa29cebc06949027336c64`, tag `es2025`, ESMeta's own `ecma262` submodule |
| ECMA-402 | tag `es2025-candidate-2025-04-01`, the revision matching the ECMA-262 pin |
| JDK, sbt | as pinned in [tools/spec-extract/mise.toml](../../tools/spec-extract/mise.toml) |

The spike reuses the maintained serializer rather than a build of its own, so
the numbers below describe `Lowering.scala` and the Go analysis as they stand,
not a prototype of them.

## The merge

ECMA-402 is built with `ecmarkup --load-biblio @tc39/ecma262-biblio`, so its
own document holds none of the ECMA-262 abstract operations it calls. Every
`ToString`, `Construct`, and `Get` in it is an external cross-reference. The
mutation and throw passes are inter-procedural and need those bodies, so the
two specifications have to be extracted as one document.

That is cheaper than it sounds. ESMeta extracts from ecmarkup **source**, not
from built output, and ECMA-402's source is the same form split across 24
files that `spec/index.html` names in `emu-import` elements. Inlining those
imports and appending the result to `spec.html` produces a document the stock
`Extractor` reads. `Extractor(document, version, eval)` already takes a jsoup
`Document`, so nothing in ESMeta is edited to point it at the merged one.

Two things had to be removed from the ECMA-262 side first, and both are
mechanical.

**The ten superseded clauses.** ECMA-402 redefines nine locale-sensitive
functions and one abstract operation, each marked "This definition supersedes
the definition provided in" beside an `emu-xref` naming the clause it
replaces. Six of the ten exist as functions in the ECMA-262 graph, so leaving
them in gives two functions under one name and the serializer's own
`checkUniqueNames` rejects the result.

**Three manual IR stubs.** `Compiler` prefers a manually supplied IR function
over a compiled algorithm of the same name, and ESMeta ships `yet` stubs for
`Number.prototype.toLocaleString`, `String.prototype.toLocaleLowerCase`, and
`String.prototype.toLocaleUpperCase` — precisely three of the functions
ECMA-402 defines for real. This one fails silently rather than loudly: with
the stubs in place all three arrived as a single opaque node, and the ECMA-402
algorithm behind them was discarded without a word. Removing the three
recovers all of them as fully readable bodies of 12, 7, and 7 nodes. This is
the one change that reaches into the vendored checkout, which
`tools/spec-extract/README.md` says is never edited; ESMeta's own
version-keyed bugfix-patch mechanism is the precedent for doing it as a patch
rather than an edit.

Nothing else collided. The two documents share four element ids, none of them
a clause the extraction keys on, and no ECMA-402 abstract operation shares a
name with an ECMA-262 one. The merged run also passes the serializer's two
existing invariants unchanged: `checkUniqueNames` finds no duplicate, and
`checkPhrasingsMatched` still counts 3, 4, 7, and 5 matches for the four
recognized ECMA-262 phrasings, so the merge leaves the ECMA-262 half of the
graph as it was.

## What ESMeta could not read at all

**Two abstract-operation heads, and they abort the run.** ESMeta raises on a
head it cannot parse, and the raise ends extraction for the whole document, so
one unreadable clause costs all 3057 algorithms rather than one. A step is
different: the metalanguage parser has a `yet` fallback, so an unreadable step
costs only that step. Both failures are in `negotiation.html` and both are
about the *type* vocabulary in the head, not about the algorithm:

- `CanonicalizeUValue` types a parameter as "a Unicode locale extension
  sequence key defined in `<a href="…">Unicode Technical Standard #35 …</a>`".
  The head parser expects a type from ECMA-262's own vocabulary and stops at
  the `_uvalue_` that follows.
- `GetBooleanOrStringNumberFormatOption` declares its return as "either a
  normal completion containing either a Boolean, String, or _fallback_, or a
  throw completion", naming a parameter inside the return type.

Both are ECMA-402 wording ECMA-262 never uses. The spike catches the raise and
drops the algorithm, which is the fallback a real pipeline would need anyway.
Two dropped out of the 195 the merged document offers is not what limits this
work.

**Three builtins land under a name the Go keying cannot resolve.** ECMA-402
defines its bound `compare` and `format` functions in clauses titled "Collator
Compare Functions", "DateTime Format Functions", and "Number Format
Functions". ESMeta cannot parse a builtin path out of those titles and falls
back to a `yet:` path, so the serializer keys them by the clause title with
its spaces intact. Every ECMA-262 builtin key parses into a path, so these are
the first three of their kind. It matters because that is where the work of
`Intl.NumberFormat.prototype.format` actually happens: the getter is readable
and returns a bound function whose body sits under an unkeyable name.

## Coverage

193 ECMA-402 algorithms extracted from 187 `emu-alg` blocks, against 2864 for
ECMA-262 in the same run. Per source file, with `yet-steps` counting steps
that are or contain something ESMeta could not formalize:

| file | emu-alg | algorithms | steps | yet-steps | incomplete algorithms |
| ---- | ------: | ---------: | ----: | --------: | --------------------: |
| numberformat.html | 39 | 48 | 914 | 103 | 26 |
| datetimeformat.html | 22 | 22 | 608 | 82 | 15 |
| durationformat.html | 20 | 20 | 452 | 35 | 12 |
| negotiation.html | 14 | 12 | 245 | 16 | 6 |
| locale.html | 21 | 21 | 205 | 28 | 10 |
| locales-currencies-tz.html | 9 | 9 | 148 | 34 | 7 |
| relativetimeformat.html | 11 | 10 | 138 | 17 | 5 |
| listformat.html | 10 | 10 | 131 | 17 | 6 |
| segmenter.html | 11 | 11 | 108 | 10 | 5 |
| collator.html | 5 | 5 | 100 | 18 | 5 |
| displaynames.html | 6 | 6 | 94 | 26 | 6 |
| locale-sensitive-functions.html | 10 | 10 | 84 | 4 | 2 |
| pluralrules.html | 7 | 7 | 74 | 10 | 5 |
| intl.html | 2 | 2 | 32 | 0 | 0 |
| **ECMA-402 total** | **187** | **193** | **3333** | **400** | **110** |
| ECMA-262, same run | — | 2864 | 22003 | 713 | 365 |

At the graph level, where an unformalized step becomes the opaque node the Go
analysis reads as incompleteness. The ECMA-402 column is the merged run and
the ECMA-262 column is the committed `cfg.json` the pipeline produces today:

| | ECMA-262 | ECMA-402 |
| --- | ---: | ---: |
| functions serialized | 1202 | 176 |
| nodes | 20010 | 3470 |
| opaque nodes | 361 (1.8%) | 295 (8.5%) |
| functions carrying one | 198 (16%) | 106 (60%) |
| builtins | 501 | 77 |
| opaque nodes in builtins | 85 (0.9%) | 66 (5.9%) |
| builtins carrying one | 51 (10%) | 40 (52%) |

So ECMA-402's algorithms are about five times as opaque per node as
ECMA-262's, and the density is worse in the abstract operations the builtins
delegate to — 229 of the 295 opaque nodes, 9.8% of their nodes — than in the
builtin bodies themselves.

The 400 unread steps are a long tail rather than two missing parser rules.
Three shapes account for the largest clusters:

- 44 name a value Unicode or CLDR defines rather than ECMA-402.
- 55 read a table row, 41 reading a column of it and 14 more iterating with
  `For each row of <emu-xref href="#table-…">`.
- 30 read an internal slot off an intrinsic, as in
  `Let _localeData_ be %Intl.Collator%.[[SearchLocaleData]]`.

265 of the 400 match none of those shapes. Teaching the parser the table and
intrinsic-slot forms would recover about a fifth of them, and the rest is
wording-by-wording work.

## What the analysis derives

The Go analysis in `internal/ecma262` runs over the merged graph with no
change at all. Its keying already models the `Intl.*` shapes — `key.go` lists
`Intl` as a namespace and `key_test.go` tests
`Intl.DateTimeFormat.prototype.format` — so what was missing was only the spec
source. Unclassified rates, per axis, over the 77 ECMA-402 builtins against
the 496 ECMA-262 builtins in the same graph:

| axis | ECMA-402 | ECMA-262 |
| ---- | -------: | -------: |
| receiver | 20 of 77 (26%) | 20 of 496 (4%) |
| returns | 54 of 77 (70%) | 246 of 496 (50%) |
| throws | 0 | 0 |
| rejects | 0 | 0 |

Six of the determinations, copied from
[spike_evidence/facts.txt](spike_evidence/facts.txt):

```
Intl.Collator.prototype.resolvedOptions returns:fresh throws:TypeError rejects:none
Intl.NumberFormat.prototype.formatToParts receiver:borrow returns:unknown throws:TypeError rejects:none
Intl.Segmenter.prototype.segment receiver:borrow returns:unknown throws:TypeError rejects:none
Intl.getCanonicalLocales receiver:none returns:fresh throws:RangeError|TypeError rejects:none
Number.prototype.toLocaleString receiver:borrow returns:unknown throws:none rejects:none
String.prototype.localeCompare returns:unknown throws:TypeError|unknown rejects:none
```

Three things stand out.

**The throw sets come out complete.** Every one of the 77 is settled, and the
answers are the `RangeError`/`TypeError` pairs the option-reading and
internal-slot guards produce. A method whose body is half opaque still has a
readable throw set, because the guards that raise sit before the formatting
the `yet` steps land in. This is the per-signal fallback the ECMA-262 spike
argued for, doing exactly what it was meant to.

**No method mutates.** Not one of the 77 comes back `mutBorrow`. The Intl
surface is constructors that allocate, accessors that read an internal slot,
and formatters that build a fresh String or List, so the escape and mutation
machinery that made ECMA-262 hard barely engages. The receiver axis is open on
26% of them rather than 4%, but every case is a withheld answer, not a wrong
one.

**`returns` is where ECMA-402 actually costs coverage.** 70% open against
ECMA-262's already-high 50%. The cause is visible in the
[`Intl.NumberFormat.prototype.formatToParts` dump](spike_evidence/cfg/INTRINSICS.Intl.NumberFormat.prototype.formatToParts.cfg).
Its body is completely readable, but it returns the result of
`FormatNumericToParts`, and the alias classifier stops at a call result it
cannot trace to an allocator. The fix is inter-procedural return-origin
propagation, which is the same gap ECMA-262 has rather than an ECMA-402 one.

## What it buys, measured

`dts_to_esc generate --cfg` classifies from a graph named on the command line,
which is how a spec bump is previewed. Pointing it at the merged graph writes a
tree that differs from the committed-graph run in exactly one file,
`std/intl.esc`, where 24 receivers relax from `mut self` to `self` and none
tighten. Every other package is byte-identical, so ECMA-402 perturbs nothing
outside the surface it describes.

That measurement needs one thing the spike does not have. `generate` refuses a
graph that leaves any receiver determination open, so the 19 the analysis does
not settle have to carry a curated entry before the converter will read the
graph at all. The 24 above were measured with a placeholder entry for each,
marked as a preview rather than a review. The count is what the surface holds,
not what a review has signed off.

Two of those 19 shapes recur: ten are `resolvedOptions` and three are the
`compare`/`format` accessors. The rest of the ECMA-402 curation is the four
committed entries ECMA-402 makes stale. `Array.prototype.toLocaleString`,
`Number.prototype.toLocaleString`, and the two `String.prototype.toLocale*Case`
entries were reviewed against the ECMA-262 algorithm or the ESMeta stub that
stood in for it, and ECMA-402 replaces all four, so each needs re-reading
against the definition that supersedes it. The curated layer reports them as
stale on its own, which is the check working.

## What it would take

Six changes, in the order they bite:

1. Assemble the merged document — inline ECMA-402's `emu-import` files, drop
   the ten superseded ECMA-262 clauses, append. New code in
   `tools/spec-extract`, roughly what the spike harness holds.
2. Patch out the three manual IR stubs so ECMA-402's real definitions are not
   silently discarded, following the version-keyed patch mechanism rather than
   editing the vendored tree.
3. Handle the two unparseable heads. Either pre-edit the two clause headers in
   the merged document or extend ESMeta's head type vocabulary; the spike's
   catch-and-drop is a third option that costs those two operations.
4. Decide what to do with the three unkeyable bound-function builtins, since
   that is where `Intl.NumberFormat.prototype.format` does its work.
5. Curate the 19 open receivers, which `generate` requires before it will read
   the graph.
6. Re-review the four committed curated entries ECMA-402 supersedes.

None of that is ESMeta's analysis, its IR, or its compiler. The manual
`intrinsics` and `types` files, which know nothing about `Intl`, turn out not
to matter: `extract → compile → build-cfg` never reads the intrinsics table,
and the type model feeds internal-method dispatch and type checking, neither
of which this pipeline runs.

What the work buys is the 24 relaxed receivers above, plus 77 builtins with
complete throw and reject sets, a receiver answer for three quarters of them
before curation, and a return answer for under a third — against
[internal/interop/data/std/intl.esc](../../internal/interop/data/std/intl.esc),
809 lines declaring 39 classes and interfaces that get no facts at all today.
