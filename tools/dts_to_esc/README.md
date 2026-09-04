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

Two more are skipped. `lib.scripthost.d.ts` declares the Windows Script
Host surface, which Escalier has no target for. The `lib.webworker.*.d.ts`
files are the Web Worker host lib: TypeScript ships it and `lib.dom` as
alternatives, so the two restate every shared global — `interface
ReadableStream` is byte-identical in both — and differ only in the
surface each host has. The partition covers the browser, so reading both
would double the members of every shared interface and add names such as
`ServiceWorkerGlobalScope` that no browser program can reach. Serving
workers means pseudo-packages scoped to that host, which is deferred
along with Node. Each run reports how many declarations every skipped
file held.

The `<esc-dir>` argument is `internal/interop/data`, the root holding
the `std/`, `web/`, and `node/` subtrees.

## Subcommands

| Command                                          | Writes  | Use                                        |
| ------------------------------------------------ | ------- | ------------------------------------------ |
| `dts_to_esc <path-to-d.ts>`                      | stdout  | Convert one file, for trying things out.   |
| `dts_to_esc generate [--cfg …] [--overlay …] [--update-digests] <lib-dir> <esc-dir>` | tree | Write the whole tree from its inputs. |

`generate` is the mode a TypeScript version bump uses. It writes every
package from scratch and reads no file it wrote, so seeding an empty tree
and re-running against a populated one are the same operation. Every fact
in a package it writes comes from one of three committed inputs: the
pinned `.d.ts` set, the ECMA-262 derived facts in
[internal/ecma262/](../../internal/ecma262/), and the overlay in
[internal/interop/overlay/](../../internal/interop/overlay/). Correcting
the output means editing one of those and re-running.

Two consequences worth knowing before the first run. Every file the run
writes opens with a `Code generated` header, which is what tells a reader
of the tree that the file is a build output. And a generated package the
run no longer emits is deleted, so a package that stops being routed
leaves the tree rather than lingering as a file no input accounts for.
Hand-authored packages, listed in `HandAuthoredPackages` in
[internal/dts_to_esc/generate.go](../../internal/dts_to_esc/generate.go),
are exempt from both.

The overlay defaults to the `overlay` directory beside `<esc-dir>`, so
`internal/interop/data` as `<esc-dir>` resolves it to
`internal/interop/overlay`. `--overlay` names another one.
`internal/interop/overlay/README.md` covers what each operation does and
how a file's name carries it.

`--update-digests` rewrites the digest sidecar beside each `replace`
overlay file instead of checking it, and writes nothing else differently.
A `replace` records the converted form it stands in for so a later run
fails when that form moves, and this is the flag that records it. Run it
after writing a new `replace`, and again to accept a form that has moved.

`--cfg <cfg.json>` additionally joins every `std:*` member the run emits
against the ECMA-262 effect facts derived from that control-flow graph
and reports the names present on one side only. It also reports what the
curated layer and the coercion filter did to those facts, and diffs the
receiver claim of every instance method against the hand-written
mutability sources. See §5, §6, and §9.2 of
[planning/ecma-262/implementation_plan.md](../../planning/ecma-262/implementation_plan.md).

## Bumping the pinned TypeScript version

1. Raise the `typescript` version in `package.json` and run
   `pnpm install`.

2. Write the tree:

   ```
   ./bin/dts_to_esc generate node_modules/typescript/lib internal/interop/data
   ```

   `internal/interop/data/std/` still holds the two §2-era stubs,
   `array.esc` and `math.esc`, rather than generated packages. Both name
   packages the partition table routes to, so a run overwrites them and
   the fixtures that read them fail. The first run against the committed
   tree is §7's stdlib bootstrap,
   [#1232](https://github.com/escalier-lang/escalier/issues/1232), which
   reviews the whole output and lands it together with the inputs that
   produce it. Until then, point `<esc-dir>` at a scratch directory.

3. Review `git diff` and commit. A declaration TypeScript removed shows
   up as a deletion in that diff rather than as a report, because the run
   does not carry the old tree forward.

An overlay `replace` or `drop` naming a declaration or member the
upstream source no longer has fails the run and names it. That is the
removal signal for the one input the diff cannot show, since the overlay
wins by construction wherever it applies. A `replace` whose converted
counterpart has been retyped rather than removed fails the same way, off
the digest recorded beside it. Read the new upstream form, decide whether
the overlay still says what it should, then re-run with
`--update-digests` and commit the sidecar with the rest of the bump.
