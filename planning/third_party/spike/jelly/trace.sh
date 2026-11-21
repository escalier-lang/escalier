#!/usr/bin/env bash
# trace.sh runs each driver run.sh generated under an instrumented copy of its
# packages and compares the call edges it records with Jelly's. See README.md
# beside it.
#
# Usage: trace.sh <work-dir>
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$1"
mkdir -p "$work/traces"

node "$here/instrument.mjs" "$work"

packages=(mitt uuid eventemitter3 lru-cache immer express rxjs date-fns lodash-es zod)
for pkg in "${packages[@]}"; do
  driver="$(ls "$work/drivers/${pkg//-/}".* | head -1)"
  (cd "$work/instrumented" && TRACE_OUT="$work/traces/${pkg}.json" \
    node --require "$here/tracer.cjs" "drivers/$(basename "$driver")" > "$work/traces/${pkg}.log" 2>&1) ||
    echo "$pkg: the traced run exited nonzero, see traces/${pkg}.log"
  node "$here/compare.mjs" "$work" "$pkg"
done
