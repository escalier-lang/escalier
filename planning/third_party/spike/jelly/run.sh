#!/usr/bin/env bash
# run.sh sets up a work directory, generates a driver for each package, and runs
# Jelly over each driver. See README.md beside it.
#
# Usage: run.sh <work-dir>
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
work="$1"
mkdir -p "$work/drivers" "$work/runs"

if [ ! -d "$work/node_modules/@cs-au-dk/jelly" ]; then
  echo '{ "name": "jelly-proto", "private": true }' > "$work/package.json"
  (cd "$work" && npm install --ignore-scripts --no-audit --no-fund --save-exact \
    @cs-au-dk/jelly@0.13.0 typescript@5.9.3 \
    date-fns@4.4.0 zod@4.6.7 lodash-es@4.18.1 @types/lodash-es@4.17.12 \
    express@5.3.0 @types/express@5.0.6 immer@11.2.9 rxjs@7.8.2 \
    eventemitter3@5.0.4 lru-cache@11.5.3 uuid@14.0.3 mitt@3.0.1)
fi

# zod's full surface is too large for one run, so its driver covers the
# exports the hand-lowered members reach.
declare -A only=([zod]="string,optional,object,registry,number")

export NODE_OPTIONS="--max-old-space-size=8192"
packages=(mitt uuid eventemitter3 lru-cache immer express rxjs date-fns lodash-es zod)
for pkg in "${packages[@]}"; do
  base="$work/drivers/${pkg//-/}"
  driver="$(node "$here/gen-driver.mjs" "$work" "$pkg" "$base" ${only[$pkg]:-})"
  (cd "$work" && npx jelly --approx --no-callgraph-external --no-tty --no-print-progress --zeros \
    -b "$work" -j "runs/${pkg}.json" "$driver" > "runs/${pkg}.log" 2>&1) || echo "$pkg: jelly failed, see runs/${pkg}.log"
  grep -E '^Calls with|^Analysis time' "$work/runs/${pkg}.log" | sed "s/^/$pkg: /"
done
