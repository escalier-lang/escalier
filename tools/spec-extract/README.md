# spec-extract

The maintainer-only toolchain for the ECMA-262 builtin annotation pipeline. It
builds [ESMeta](https://github.com/es-meta/esmeta), runs its
`extract → compile → build-cfg` pipeline over a pinned ECMA-262 revision, and
serializes the resulting control-flow graph to
[internal/ecma262/cfg.json](../../internal/ecma262/cfg.json).
[planning/ecma-262/implementation_plan.md](../../planning/ecma-262/implementation_plan.md)
plans the pipeline; §2 covers the toolchain and §3 the serializer.

`cfg.json` is committed, and regenerating it is the only reason to run anything
here. It lives beside the Go analysis that reads it, which embeds it so a
`dts_to_esc generate` run carries the receiver claims without being handed a
path. A contributor building the compiler never needs a JVM.

Nothing here is part of the Go build or CI. The compiler builds from the repo
root with the tools in the root `mise.toml`, which lists neither Java nor sbt.
The JVM pins live in this directory's `mise.toml`, and mise reads config from
the current directory and its ancestors only, so activating the repo root never
picks them up.

## Pinned revisions

| Component                | Revision                                             | Pinned by                           |
| ------------------------ | ---------------------------------------------------- | ----------------------------------- |
| ESMeta                   | `7d237fd1680f473e674320cc97932702d950fa98`           | the `esmeta` submodule              |
| ECMA-262 spec            | `84b38ad852ff426795fa29cebc06949027336c64`, `es2025` | ESMeta's own `ecma262` submodule    |
| ECMA-402 spec            | `4a2dae4c04e7fdc9e9de5946fbcdb31b073e4040`, `es2025-candidate-2025-04-01` | the `ecma402` submodule |
| sbt that compiles ESMeta | 1.10.11                                              | ESMeta's `project/build.properties` |
| JDK and sbt launcher     | see `mise.toml`                                      | `mise.toml`                         |

Pinning the ESMeta revision pins the ECMA-262 revision with it, because ESMeta
tracks that specification as a submodule of its own. The pin sits one commit
past ESMeta's `v0.7.3` tag, on `main`. That is the revision the §1 spike ran, so
the control-flow-graph dumps committed under
[planning/ecma-262/spike_evidence/](../../planning/ecma-262/spike_evidence/)
describe the build this directory produces.

ECMA-402 is pinned separately, because nothing tracks it for us. **The two
specification pins move together.** They describe one language, and a document
merged from two editions holds algorithms written against different definitions
of the operations they call. ECMA-402 tags an edition as
`esYYYY-candidate-<date>` where ECMA-262 tags it `esYYYY`, so the tag names do
not match on their face. `MergedSpec` catches the coarsest form of a mismatch
and reports it as a pin problem: a clause ECMA-402 supersedes that the pinned
ECMA-262 does not define. It cannot catch two editions that agree on clause
names, so a bump is where someone checks the pair by hand.

## Setup

1. Install [mise](https://mise.jdx.dev/getting-started.html) if you do not have
   it.

2. Check out the vendored ESMeta source. `.gitmodules` sets `update = none` on
   the submodule, so `git clone --recurse-submodules` of Escalier skips it and
   contributors never fetch ESMeta or the large `test262` tree hanging off it.
   `--checkout` overrides that for the one submodule you name:

   ```sh
   git submodule update --init --checkout tools/spec-extract/esmeta
   ```

3. Check out the spec ESMeta extracts from. ESMeta declares three submodules of
   its own and the build needs only `ecma262`:

   ```sh
   git -C tools/spec-extract/esmeta submodule update --init ecma262
   ```

   Check out ECMA-402 beside it. It carries the same `update = none`, so it
   needs naming the same way:

   ```sh
   git submodule update --init --checkout tools/spec-extract/ecma402
   ```

4. Install the JVM toolchain:

   ```sh
   cd tools/spec-extract
   mise install
   ```

5. Regenerate `cfg.json`:

   ```sh
   mise run serialize-cfg
   ```

   The first run compiles ESMeta and takes a couple of minutes; later runs take
   about twenty seconds. It prints what it extracted, what it wrote, and the
   result of the schema check, and exits nonzero if that check fails.

Building writes `target/` and `lib_managed/` into this directory, both ignored,
and the same plus a `.scala.semanticdb` beside every source into the vendored
tree. The submodule entry carries `ignore = untracked` so none of the latter
reaches the parent repo's `git status`. Run
`git -C tools/spec-extract/esmeta clean -xfd` to remove it.

## Reading a single algorithm

ESMeta can print the control-flow graph of one specification function as text,
which is far easier to read than the JSON when checking what the serializer did
with a step. That needs the standalone launcher:

```sh
mise run build-esmeta
cd esmeta
ESMETA_HOME=$PWD ./bin/esmeta build-cfg -build-cfg:log
```

One `.cfg` file per function lands in `logs/cfg/func/`, named after the ESMeta
function — `INTRINSICS.Array.prototype.push.cfg`. The dumps for the methods the
§1 spike read its findings from are committed under
[planning/ecma-262/spike_evidence/](../../planning/ecma-262/spike_evidence/).

## Lockfile

`mise.lock` is not committed yet. Writing it means resolving every pinned tool
against its download host to record the asset checksums, which needs network
access to `mise-java.jdx.dev` and to the conda-forge index. Generate and commit
it with:

```sh
cd tools/spec-extract
mise install
mise lock --platform linux-x64,macos-arm64
```

## The merged document

`MergedSpec` builds the document the extractor reads: ECMA-262's `spec.html`
with ECMA-402's clauses spliced into it. The two have to be extracted together.
ECMA-402 is built against ECMA-262 as an external bibliography, so its own
document holds none of the abstract operations it calls, and a graph built from
it alone would reach no body for any of them.

ESMeta extracts from ecmarkup source rather than built output. ECMA-402's
source is that same form, split across the files `spec/index.html` names in
`emu-import` elements. The merge resolves those imports and appends the result,
so no `ecmarkup` run is involved. Every element ECMA-402 contributes
carries an `esc-spec-source` attribute naming the file it came from, which is
how a report says which clause a function belongs to.

ECMA-402 replaces ten ECMA-262 clauses, nine locale-sensitive functions and
`AvailableNamedTimeZoneIdentifiers`. The merge removes each one before
appending, since leaving it in gives two functions under one name. Which
clauses those are is read out of the sentence ECMA-402 writes above each
replacement rather than listed in the source. A revision that supersedes one
more clause is then handled rather than silently half-merged. The count is
pinned the way the recognized phrasings are, so a rewording fails the run
instead of quietly dropping a removal.

Run the merge and its checks on their own with:

```sh
mise run check-merge
```

It reports what was imported, appended, and replaced. To read a clause as the
extractor sees it, run the entry point directly with the spec directory and an
output path, and it writes the merged document out:

```sh
sbt 'runMain escalier.specextract.MergeCheck ecma402/spec merged.html'
```

Two ECMA-402 heads declare a type in wording ECMA-262 never uses, and ESMeta
raises on a head it cannot parse. The raise ends extraction for the whole
document, so one unreadable head costs every algorithm rather than its own,
which is why the merge restates those two and leaves the unread steps alone. A
step falls back to a `yet` and costs only itself. `MergedSpec.HeadRewrites`
records what each restatement says and why it declares the same values, and the
run fails if a pattern stops matching its head exactly once.

`Main` serializes ECMA-262 alone and writes the committed `cfg.json`.
Committing a merged graph is
[#1455](https://github.com/escalier-lang/escalier/issues/1455).

## The serializer

`build.sbt` names the vendored checkout as a source dependency, so `sbt` builds
ESMeta from the pinned revision and puts `esmeta.cfg` on the classpath. Nothing
is published to a local repository and the vendored tree is never edited.

`.scalafmt.conf` is the vendored tree's own configuration, so the Scala here
reads like the Scala it is compiled against. `mise run format` applies it.

`src/main/scala/escalier/specextract/` holds four files. `Main` runs the
pipeline in process, `Lowering` turns `esmeta.cfg.CFG` into the schema,
`Validation` reads the written file back and checks it, and `Schema` carries the
case classes and the writer. The schema itself is Appendix A of the
implementation plan, and the Go analysis reads the same shape, so a field
renamed on one side has to be renamed on the other.

The lowering copies structure and makes no mutability or alias judgement. Three
shapes need reconstruction rather than a copy, because the IR compiler has
already lowered them away, and each is described where it is handled in
`Lowering.scala`:

- The `?` and `!` completion guards, which survive as a fixed pattern of an
  assertion, an abrupt-check branch, and an unwrap. The serializer records the
  guard on the call and drops all three, because the unwrap reads a field and
  would otherwise break the origin chain through every coerced receiver.
- The argument prologue, which takes a builtin's declared formals out of the
  argument list. The parameters come from the algorithm head instead, which is
  also where the serializer reads which formal is the rest parameter.
- `Throw a *T* exception`, which is a call constructing the error object
  followed by a `ThrowCompletion` of it.

One shape needs care in the other direction. An allocation keeps the operands
stored into it, because a parameter put in a fresh record escapes into whatever
that record is stored in, and lowering the allocation to a bare literal would
drop the only edge that shows it.

A fourth shape is read out of prose rather than out of the IR. ESMeta leaves
some algorithm steps unformalized, and those become opaque nodes that tell the
analysis it could not read the whole algorithm. A few of them state a write or
an allocation plainly enough to lower, so `Lowering.scala` carries a small table
of recognized phrasings and emits the ordinary node each one states.
`Set.prototype.clear` is the shape. Its only mutation is "Replace the element of
_S_.[[SetData]] whose value is _e_ with an element whose value is ~empty~",
which names both the object and the slot.

Each entry is reviewed against the wording at the pinned revision and records
how many steps it matched then. The run prints those counts and fails when one
moves, since a reworded step would otherwise fall back to opaque and lose the
fact again. Re-read the step against the new wording before changing a count.

## Bumping the spec

Bump the ESMeta submodule, which carries the ECMA-262 revision with it, then
rebuild:

```sh
git -C tools/spec-extract/esmeta fetch origin
git -C tools/spec-extract/esmeta checkout <new-esmeta-revision>
git -C tools/spec-extract/esmeta submodule update --init ecma262
git add tools/spec-extract/esmeta
```

Move ECMA-402 to the edition that new ECMA-262 revision belongs to. Its tags
read `esYYYY-candidate-<date>`:

```sh
git -C tools/spec-extract/ecma402 fetch origin --tags
git -C tools/spec-extract/ecma402 checkout <matching-ecma402-tag>
git add tools/spec-extract/ecma402
```

Update the revision table above, then re-run the steps under "Setup" from
step 4 and commit the regenerated `cfg.json`. A bump can change what the
control-flow graph carries, so re-check the per-method evidence in
[planning/ecma-262/spike_evidence/](../../planning/ecma-262/spike_evidence/)
against fresh dumps. §10 of the plan turns this into a full runbook with a
drift report.
