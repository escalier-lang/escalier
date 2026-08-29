# §6 Validation diff: findings

Records the outcome of [implementation_plan.md](implementation_plan.md) §6,
which proves the ECMA-262 fact source before §7 lets the converter trust it.
That is requirements.md FR9. The diff measures the receiver-mutability claim of
every published fact against the hand-written answer the converter reaches
today. This document gives a disposition for each entry the diff turned up.

**Verdict: the fact source holds.** Over the committed graph the diff compares
218 methods. Before the fixes recorded below, the two sources agreed on 215 of
them. All three disagreements are resolved, so they now agree on every one. Two of the three were a heuristic mis-classification the facts caught, and
one was an analyzer gap the facts missed. §7 is authorized to rank the facts
above the name tiers and to delete the 24 override entries listed below.

## What the diff compares

The hand-written answer is the union of two sources, read in the order
`checker.UpdateMethodMutability` reads them:

1. `nonMutatingOverrides` in
   [internal/dts_to_esc/mutability.go](../../internal/dts_to_esc/mutability.go).
   An entry names one method of one owner as non-mutating.
2. `dts_to_esc.ClassifyMethodByName` in the same file, which answers from the
   method's name alone. It covers the well-known allow-list of tier 3, the
   `get*` prefix rule of tier 5, and the name-based prefixes of tier 6.

A method neither source answers falls through to the tier-7 `&mut self`
default. That default is an absence of a claim rather than a claim, so the diff
counts those methods apart instead of scoring them as disagreements.

The override table is not a rung of `dts_to_esc.Classify`. Classify's tier 4
reads the override store of `internal/interop`, whose built-in subtree holds
only a README, so the converter reaches a `.d.ts` method through the name-only
tiers alone. The prelude is the table's one production reader today, which is
what §7 has to sequence around — see below.

The comparison is `internal/dts_to_esc.ValidateReceivers`. A fact takes part
only when it addresses an instance method by a string key and carries a
receiver claim. Three shapes are left out, each because the hand-written
sources cannot answer them:

- A static or a namespace function has no receiver to mutate.
- An accessor's polarity is fixed where the object type is built, so no tier is
  consulted for it.
- A symbol-keyed member cannot be addressed by the string-keyed override table,
  and `ClassifyMethodByName` reads a bare name.

## Results over the committed graph

| Bucket | Count | Meaning |
| ------ | ----- | ------- |
| Confirmed | 194 | A heuristic the fact agrees with. |
| Redundant | 24 | An override entry the fact agrees with. §7 deletes it. |
| Disagreement | 0 | The two sources answer differently. |
| Answered by the facts alone | 48 | Neither hand-written source answers. The converter reaches these through the `&mut self` default today. |
| Override with no fact | 37 | An override entry no fact answers. §7 keeps it. |

The run prints these counts. `dts_to_esc bootstrap --cfg <cfg.json> <lib-dir>
<out-dir>` writes them to stderr beside the curation and join reports.

## Disposition of each disagreement

The diff found three disagreements before the fixes below landed. Each is
resolved in this PR, which is why the table above reads zero.

### `Array.prototype.copyWithin` and `TypedArray.prototype.copyWithin`

- **Fact:** `mutBorrow`. The mutation fixpoint charges the receiver a write.
  The `Array` algorithm calls `Set` and `DeletePropertyOrThrow` on the object
  `ToObject(this)` returned, and the `TypedArray` one calls `SetValueInBuffer`
  on the buffer it read off the receiver. All three are seeded direct mutators.
- **Hand-written answer:** non-mutating, from the tier-6 `copy` prefix.
- **Disposition: the fact is correct and the heuristic was wrong.** Both
  methods write their receiver in place, and what they hand back is the
  receiver itself rather than a copy. The `copy` prefix reads the name as a
  projection, which is what the prefix is for on every other name it matches.
  Narrowing the prefix would cost those, so the fix is an exact-name entry.
  `copyWithin` now sits in `mutatingExact` beside `sort` and `reverse`, and the
  prefer-mutating rule makes the exact match win over the prefix.
- **Why it matters beyond the two spec methods.** §7 ranks the facts above the
  heuristics, so the two ECMA-262 methods would have come out right either way.
  The heuristic is what classifies a `copyWithin` on any other type, and it was
  handing those a non-mut receiver. This is the diff finding a bug outside the
  surface it was pointed at.

### `FinalizationRegistry.prototype.unregister`

- **Fact:** `borrow`.
- **Hand-written answer:** mutating, from the tier-6 `unregister` prefix.
- **Disposition: the fact was wrong and the §4 analysis is fixed.** The
  algorithm's one write is the step "Remove _cell_ from
  _finalizationRegistry_.[[Cells]]", which ESMeta lowers to a call to its
  `__REMOVE_ELEM__` intrinsic. That intrinsic's body writes the list at an
  index it computes, and a computed slot on a declared parameter leaves the
  operation `Incomplete` rather than charging the parameter. `Incomplete` does
  not propagate to callers, so the removal never reached `unregister` and its
  receiver read clean.
- **The fix.** `__REMOVE_ELEM__` is seeded in `directMutators` at position 1,
  its list parameter. The call passes `finalizationRegistry.[[Cells]]`, whose
  origin is the receiver's interior, so the seed charges the write to the
  receiver. `FinalizationRegistry.prototype.unregister` now publishes
  `mutBorrow`.
- **Blast radius.** One published fact changes. Two abstract operations,
  `RemoveWaiter` and the `EnqueueAtomicsWaitAsyncTimeoutJob` closure, gain an
  `Unattributable` flag with no effect on any published receiver.

The narrow seed was chosen over the general rule. Charging every computed-slot
write on a declared parameter to that parameter also reaches
`__APPEND_LIST__`, which callers hand lists the analysis cannot place. That
raises `Unattributable` across the graph and withholds the receiver claim of
about 50 builtins, each of which would then need a curated entry. The general
rule is the more honest answer to what the analysis does not know, and paying
for it is §4 work rather than §6 work.

## The 24 redundant override entries

Each is an entry in `nonMutatingOverrides` whose owner and member a published
fact answers the same way. §7 deletes exactly this list.
`TestCommittedGraphRedundantOverrides` pins it so §7 works from a checked list
rather than recomputing one.

```
Function.apply               String.localeCompare  String.replaceAll
Function.bind                String.match          String.search
Function.call                String.matchAll       String.split
Object.propertyIsEnumerable  String.normalize      String.startsWith
String.charAt                String.padEnd         String.substring
String.charCodeAt            String.padStart       String.trim
String.codePointAt           String.repeat         String.trimEnd
String.endsWith              String.replace        String.trimStart
```

### Sequencing: the deletion is not safe on its own

§7 inserts the facts into `dts_to_esc.Classify`, and the prelude's
`UpdateMethodMutability` is the only code that reads the override table. So
deleting the 24 entries in the same change that wires the facts into the
converter would leave the prelude reaching those methods through the name
heuristics alone, and the heuristics are what the entries exist to correct:
`String.prototype.replace` would go back to a mutating receiver.

The deletion is safe once either holds. The facts reach whichever path applies
receiver mutability to the `.d.ts`-loaded lib types, or the prelude path is
gone, which is what the M12 flip of the builtins workstream does to
`UpdateMethodMutability`. §7 states which of the two it is relying on before
removing an entry.

## The 37 entries §7 keeps

`Body`, `Console`, `Request`, and `Response` are `web:*` owners with no
ECMA-262 algorithm, so no fact can ever address them. They wait on the WebIDL
extractor. `String.substr` is an Annex B method the committed graph does not
carry. `TestCommittedGraphOverridesWithNoFact` pins this list too.

## The gate holds itself

`TestCommittedGraphLeavesNoReceiverDisagreement` fails the build on any new
disagreement, so a spec bump or a heuristic edit cannot reintroduce one
silently. Resolving a new one means the same triage this document records: fix
whichever side is wrong, or, where the spec's own steps put the answer out of
the graph's reach, answer the method with a curated entry in
[internal/ecma262/curated.json](../../internal/ecma262/curated.json). Add the
disposition to this document either way.
