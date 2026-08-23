# spec-extract

The maintainer-only toolchain for the ECMA-262 builtin annotation pipeline. It
builds [ESMeta](https://github.com/es-meta/esmeta) and runs its
`extract → compile → build-cfg` pipeline over a pinned ECMA-262 revision.
[planning/ecma-262/implementation_plan.md](../../planning/ecma-262/implementation_plan.md)
plans the pipeline; §2 covers this directory.

Nothing here is part of the Go build or CI. The compiler builds from the repo
root with the tools in the root `mise.toml`, which lists neither Java nor sbt.
The JVM pins live in this directory's `mise.toml`, and mise reads config from
the current directory and its ancestors only, so activating the repo root never
picks them up.

## Pinned revisions

| Component                | Revision                                             | Pinned by                           |
| ------------------------ | ---------------------------------------------------- | ----------------------------------- |
| ESMeta                   | `7d237fd1680f473e674320cc97932702d950fa98`, v0.7.3   | the `esmeta` submodule              |
| ECMA-262 spec            | `84b38ad852ff426795fa29cebc06949027336c64`, `es2025` | ESMeta's own `ecma262` submodule    |
| sbt that compiles ESMeta | 1.10.11                                              | ESMeta's `project/build.properties` |
| JDK and sbt launcher     | see `mise.toml`                                      | `mise.toml`                         |

Pinning the ESMeta revision pins the spec revision with it, because ESMeta
tracks ECMA-262 as a submodule of its own.

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

4. Install the JVM toolchain and build ESMeta:

   ```sh
   cd tools/spec-extract
   mise install
   mise run build-esmeta
   ```

   `sbt assembly` writes a self-contained launcher to `esmeta/bin/esmeta`. It
   compiles 288 Scala sources and takes a couple of minutes.

5. Run the pipeline and dump one control-flow graph per specification function:

   ```sh
   cd tools/spec-extract/esmeta
   ESMETA_HOME=$PWD ./bin/esmeta build-cfg -build-cfg:log
   ```

   The dumps land in `esmeta/logs/cfg/func/`. `ESMETA_HOME` is required; ESMeta
   resolves its resource paths from it.

Building writes `target/`, `lib_managed/`, `bin/esmeta`, and a
`.scala.semanticdb` beside every source into the vendored tree. The submodule
entry carries `ignore = untracked` so none of that reaches the parent repo's
`git status`. Run `git -C tools/spec-extract/esmeta clean -xfd` to remove it.

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

## Bumping the spec

Bump the ESMeta submodule, which carries the spec revision with it, then
rebuild:

```sh
git -C tools/spec-extract/esmeta fetch origin
git -C tools/spec-extract/esmeta checkout <new-esmeta-revision>
git -C tools/spec-extract/esmeta submodule update --init ecma262
git add tools/spec-extract/esmeta
```

Update the revision table above, then re-run the steps under "Setup" from
step 4. A bump can change what the control-flow graph carries, so re-check the
per-method evidence in
[planning/ecma-262/spike_evidence/](../../planning/ecma-262/spike_evidence/)
against the fresh dumps. §10 of the plan turns this into a full runbook with a
drift report.
