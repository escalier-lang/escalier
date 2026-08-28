# `dts_to_esc`

Converts TypeScript `.d.ts` declarations into Escalier `.esc` source.
The committed `std:*` and `web:*` pseudo-packages under
[internal/interop/data/](../../internal/interop/data/) are its output.

See §5 and §6 of
[planning/builtins/implementation_plan.md](../../planning/builtins/implementation_plan.md)
for the design. This file covers running the tool.

## Build

```
go build -o ./bin/dts_to_esc ./tools/dts_to_esc
```

## Inputs

The `.d.ts` inputs are the `lib.*.d.ts` files that ship with the
TypeScript version pinned in [package.json](../../package.json). After
`pnpm install` they are in `node_modules/typescript/lib`. That directory
is the `<lib-dir>` argument below. Files ending in `.full.d.ts` are
skipped: they declare nothing themselves and only pull in the lib files
beside them through `/// <reference lib="..." />`.

The `<esc-dir>` argument is `internal/interop/data`, the root holding
the `std/`, `web/`, and `node/` subtrees.

## Subcommands

| Command                                          | Writes  | Use                                        |
| ------------------------------------------------ | ------- | ------------------------------------------ |
| `dts_to_esc <path-to-d.ts>`                      | stdout  | Convert one file, for trying things out.   |
| `dts_to_esc check <lib-dir> <esc-dir>`           | nothing | Verify the committed tree.                 |
| `dts_to_esc regenerate <lib-dir> <esc-dir>`      | tree    | Fold upstream additions into that tree.    |
| `dts_to_esc bootstrap [--cfg …] <lib-dir> <out>` | tree    | Seed a tree from scratch.                  |

`check` prints the unified diff a `regenerate` run would apply and exits
non-zero when that diff is non-empty. `regenerate` only ever inserts: it
adds declarations and members upstream has and the committed tree lacks,
and leaves every existing declaration byte-for-byte alone, so hand-edits
survive a re-run. `bootstrap` writes each package file whole, so pointing
it at the committed tree discards those hand-edits.

`bootstrap --cfg <cfg.json>` additionally joins every `std:*` member it
emits against the ECMA-262 effect facts derived from that control-flow
graph and reports the names present on one side only. See §5 of
[planning/ecma-262/implementation_plan.md](../../planning/ecma-262/implementation_plan.md).

## Bumping the pinned TypeScript version

1. Raise the `typescript` version in `package.json` and run
   `pnpm install`.

2. Run `check` to see what changed upstream:

   ```
   ./bin/dts_to_esc check node_modules/typescript/lib internal/interop/data
   ```

   The output is one unified diff per package the bump changes, plus a
   note for each finding a diff cannot express. A clean bump prints the
   summary line alone and exits zero.

3. Apply the additions:

   ```
   ./bin/dts_to_esc regenerate node_modules/typescript/lib internal/interop/data
   ```

   Review `git diff` and commit. Running `check` again should now report
   nothing missing.

4. Port the rest by hand. `regenerate` never deletes and never rewrites,
   so three kinds of upstream change are left for you:

   - **Removals.** A declaration the `.d.ts` no longer has is reported
     as `is absent from the .d.ts; the diff does not remove it`. Decide
     whether to delete it or keep it as an Escalier-side extension.
   - **Signature changes.** A member whose type changed upstream keeps
     its committed signature, and the check does not yet flag it. See
     the coverage note below.
   - **Members the splice could not place.** A declaration whose body
     braces the write pass cannot locate is reported as
     `could not locate the body of <name>`. Add its members yourself.

5. Commit the ported changes, and confirm `check` is clean.

## What `check` does not cover

Only the presence of a declaration or member is compared. A member that
still exists but whose signature or property type drifted upstream
passes. Those are checks 2 and 3 of §6.4, and they compare the two sides
through the solver's `constrain`, which needs the solver to ingest a
declaration module — SimpleSub M7.5. Until that lands, a passing check
means every `.d.ts` name has an `.esc` counterpart, not that every
counterpart still means the same thing. The footer of every check run
repeats this.

§6.6 has CI run `check` on every PR. That job waits on §7, which seeds
the committed tree from the full lib set; until then `check` against
`internal/interop/data` reports the whole stdlib as missing. That same
job is the hook point for the optional §6.6 nudge, a PR annotation when
the diff grows past some threshold. Nothing implements it yet.
