#!/usr/bin/env bash
#
# Reproduce the §1 feasibility spike: build ESMeta, run its CFG pipeline
# against the pinned ECMA-262 revision, and collect the representative-method
# control-flow-graph dumps the findings read from.
#
# Findings:  planning/ecma-262/spike_findings.md
# Evidence:  planning/ecma-262/spike_evidence/  (the dumps this script regenerates)
#
# This is a one-time reproduction runbook, not part of the Go build or CI. It
# clones ESMeta into a scratch workdir of its own, so it stays independent of
# the vendored submodule. The maintained toolchain lives under
# tools/spec-extract/ — pinned JDK and sbt, the vendored ESMeta source, and the
# build steps are in tools/spec-extract/README.md.
#
# Toolchain the spike used. Install these first; the compiler builds without a
# JDK or sbt.
#   - JDK 17+, the spike used Temurin 21
#   - an sbt 1.x launcher, which bootstraps the sbt 1.10.11 that ESMeta's
#     project/build.properties pins
#   - git, curl
#
# Pinned revisions:
#   - ESMeta      7d237fd1680f473e674320cc97932702d950fa98
#                 one commit past the v0.7.3 tag on main
#   - ECMA-262    84b38ad852ff426795fa29cebc06949027336c64  (tag es2025)
#                 pinned transitively as ESMeta's `ecma262` submodule at the
#                 ESMeta revision above, so `git submodule update --init` checks
#                 out the right spec with no extra pinning.
#
# Usage:  planning/ecma-262/reproduce_spike.sh [workdir]
#   workdir defaults to a fresh ./esmeta-spike under the current directory.

set -euo pipefail

ESMETA_REV="7d237fd1680f473e674320cc97932702d950fa98"
WORKDIR="${1:-$PWD/esmeta-spike}"
REPO_DIR="$WORKDIR/esmeta"

# The representative method set spanning every shape the analysis must handle.
# Each name is an ESMeta CFG function; the dump lands at
# logs/cfg/func/<name>.cfg. `Set` is the property-write abstract operation,
# included because it shows the dynamic [[Set]] dispatch that the FR1 seed
# short-circuits.
REPRESENTATIVE=(
  "INTRINSICS.Array.prototype.push"                 # direct receiver mutation + escape
  "INTRINSICS.Array.prototype.fill"                 # receiver mutation returning receiver
  "INTRINSICS.Array.prototype.sort"                 # receiver mutation returning receiver
  "INTRINSICS.Array.prototype.slice"                # fresh allocation, no receiver mutation
  "INTRINSICS.Array.prototype.map"                  # fresh allocation + callback
  "INTRINSICS.Array.prototype.forEach"              # callback propagation
  "INTRINSICS.Map.prototype.set"                    # internal-slot mutation + escape
  "INTRINSICS.Set.prototype.add"                    # internal-slot mutation + escape
  "INTRINSICS.Object.freeze"                        # transitive mutation through a helper AO
  "SetIntegrityLevel"                               # the helper freeze delegates its write to
  "INTRINSICS.Reflect.set"                          # namespace fn, param mutation + escape
  "INTRINSICS.String.prototype.charAt"              # immutable primitive
  "INTRINSICS.String.prototype.replace"             # immutable primitive + callback
  "INTRINSICS.String.prototype[%Symbol.iterator%]"  # symbol-keyed method
  "INTRINSICS.Number.prototype.toFixed"             # domain throw + partial `yet`
  "INTRINSICS.Promise.reject"                       # async reject, param origin
  "INTRINSICS.Promise.all"                          # async reject, combinator
  "Set"                                             # dynamic [[Set]] dispatch (seed rationale)
)

echo "==> workdir: $WORKDIR"
mkdir -p "$WORKDIR"

if [ ! -d "$REPO_DIR/.git" ]; then
  echo "==> cloning ESMeta with submodules"
  git clone --recurse-submodules https://github.com/es-meta/esmeta.git "$REPO_DIR"
fi

echo "==> checking out pinned ESMeta revision $ESMETA_REV"
git -C "$REPO_DIR" checkout --quiet "$ESMETA_REV"
git -C "$REPO_DIR" submodule update --init

echo "==> spec revision (ESMeta ecma262 submodule):"
git -C "$REPO_DIR/ecma262" rev-parse HEAD

echo "==> building ESMeta (sbt assembly)"
( cd "$REPO_DIR" && ESMETA_HOME="$REPO_DIR" sbt assembly )

echo "==> running extract -> compile -> build-cfg with CFG logging"
( cd "$REPO_DIR" && ESMETA_HOME="$REPO_DIR" ./bin/esmeta build-cfg -build-cfg:log )

DUMP_DIR="$REPO_DIR/logs/cfg/func"
echo "==> per-function CFG dumps in: $DUMP_DIR"
echo "    total functions: $(ls "$DUMP_DIR" | wc -l)"

echo "==> representative-method dumps (the committed spike_evidence/ set):"
for name in "${REPRESENTATIVE[@]}"; do
  if [ -f "$DUMP_DIR/$name.cfg" ]; then
    echo "    OK   $name.cfg"
  else
    echo "    MISS $name.cfg"
  fi
done

echo
echo "Done. Compare $DUMP_DIR/<name>.cfg against planning/ecma-262/spike_evidence/"
echo "to confirm the findings still hold on a spec bump."
