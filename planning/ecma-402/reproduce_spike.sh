#!/usr/bin/env bash
#
# Reproduce the ECMA-402 feasibility spike: merge ECMA-402's ecmarkup sources
# into the pinned ECMA-262 spec.html, run the maintained extraction pipeline
# over the merged document, and report how much of ECMA-402 survives each
# stage.
#
# Findings:  planning/ecma-402/spike_findings.md
# Evidence:  planning/ecma-402/spike_evidence/  (what this script regenerates)
#
# The spike reuses the maintained toolchain under tools/spec-extract/ rather
# than building its own ESMeta, so the numbers describe the serializer the Go
# analysis actually reads. Set that directory up first — tools/spec-extract/
# README.md covers the JDK and sbt pins and the submodule checkout.
#
# Pinned revisions:
#   - ESMeta and ECMA-262   the tools/spec-extract/esmeta submodule, which
#                           carries ECMA-262 as a submodule of its own
#   - ECMA-402              tag es2025-candidate-2025-04-01, the revision that
#                           matches the ES2025 ECMA-262 pin
#
# Usage:  planning/ecma-402/reproduce_spike.sh [workdir]
#   workdir defaults to ./ecma402-spike under the current directory.

set -euo pipefail

ECMA402_REV="es2025-candidate-2025-04-01"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKDIR="${1:-$PWD/ecma402-spike}"
SPEC_DIR="$WORKDIR/ecma402/spec"
OUT_DIR="$WORKDIR/out"
HARNESS_DIR="$REPO_ROOT/tools/spec-extract/src/main/scala/escalier/specextract"
HARNESS="$HARNESS_DIR/Ecma402Spike.scala"

# The manual IR stubs ESMeta supplies for locale-sensitive functions ECMA-402
# defines for real. The compiler prefers a manual function over a compiled
# algorithm of the same name, so leaving these in place discards the ECMA-402
# definition without a word. The spike measures the surface with them out of
# the way and puts them back afterwards.
STUBS=(
  "INTRINSICS.Number.prototype.toLocaleString.ir"
  "INTRINSICS.String.prototype.toLocaleLowerCase.ir"
  "INTRINSICS.String.prototype.toLocaleUpperCase.ir"
)
STUB_DIR="$REPO_ROOT/tools/spec-extract/esmeta/src/main/resources/manuals/funcs"

cleanup() {
  rm -f "$HARNESS" "$REPO_ROOT/internal/ecma262/zz_spike402_test.go"
  for stub in "${STUBS[@]}"; do
    [ -f "$WORKDIR/stubs/$stub" ] && mv "$WORKDIR/stubs/$stub" "$STUB_DIR/"
  done
}
trap cleanup EXIT

mkdir -p "$WORKDIR" "$OUT_DIR" "$WORKDIR/stubs"

echo "==> checking out ECMA-402 $ECMA402_REV"
if [ ! -d "$WORKDIR/ecma402" ]; then
  git clone --depth 1 --branch "$ECMA402_REV" \
    https://github.com/tc39/ecma402.git "$WORKDIR/ecma402"
fi

echo "==> moving the colliding manual stubs aside"
for stub in "${STUBS[@]}"; do
  [ -f "$STUB_DIR/$stub" ] && mv "$STUB_DIR/$stub" "$WORKDIR/stubs/"
done

echo "==> running the spike harness"
cp "$REPO_ROOT/planning/ecma-402/spike_harness/Ecma402Spike.scala" "$HARNESS"
cd "$REPO_ROOT/tools/spec-extract"
sbt -batch "runMain escalier.specextract.Ecma402Spike $SPEC_DIR $OUT_DIR"

echo "==> deriving facts from the merged graph"
# The harness is stored with a .txt suffix so `go test ./...` does not try to
# build planning/ as a package. It is Go source, and only compiles once it sits
# in internal/ecma262 where the identifiers it reads are declared.
cp "$REPO_ROOT/planning/ecma-402/spike_harness/spike402_test.go.txt" \
  "$REPO_ROOT/internal/ecma262/zz_spike402_test.go"
cd "$REPO_ROOT"
ESC_SPIKE_CFG="$OUT_DIR/cfg.json" \
  go test ./internal/ecma262/ -run TestSpike402 -v |
  sed 's/^ *zz_spike402_test.go:[0-9]*: //' > "$OUT_DIR/facts.txt" || true

echo
echo "wrote $OUT_DIR:"
ls -1 "$OUT_DIR"
