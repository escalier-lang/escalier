#!/usr/bin/env bash
#
# Wire the "blocked by" dependency edges for the M7.5 milestone-5 issues, plus the
# edges that cross into the ECMA-262 and Builtin-types milestones.
#
# Edges come from the dependency graph in
# planning/simple_sub/m7.5-implementation-plan.md.
#
# Usage:
#   scripts/m7.5-deps.sh            # apply the edges
#   scripts/m7.5-deps.sh --dry-run  # print what would be applied, change nothing
#
# Requires the gh CLI, authenticated with repo write access.
# Re-running is safe: an edge that already exists is reported and skipped.

set -euo pipefail

REPO="escalier-lang/escalier"
DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

# --- edges -------------------------------------------------------------------
# One "BLOCKED BLOCKER # comment" per line: BLOCKED cannot start until BLOCKER
# lands. Issue numbers, not IDs — the script resolves IDs itself.

read -r -d '' EDGES <<'EOF' || true
# --- M7.5 core (milestone 5) ---
1237 1236  # PR2 scheme-URI imports        <- PR1 cross-module core
1238 1236  # PR4 lib decl kinds            <- PR1 cross-module core
1239 1237  # PR5 real Array<T>             <- PR2 scheme-URI imports
1240 1239  # PR6 protocol set              <- PR5 real Array<T>
1241 1239  # PR7 numeric index reads       <- PR5 real Array<T>
1243 1239  # PR12 exactness + gate         <- PR5 real Array<T>
1242 1241  # PR8 index writes              <- PR7 numeric index reads

# --- M7.5 deferred tail (milestone 5) ---
1244 1237  # PR3 package cycles            <- PR2 scheme-URI imports
1245 1239  # PR9 variadic tuples           <- PR5 real Array<T>
1246 1240  # PR10 unique symbol            <- PR6 protocol set
1247 1241  # PR11 place segments           <- PR7 numeric index reads
1247 1246  # PR11 place segments           <- PR10 unique symbol
1248 1236  # PR13 npm .d.ts channel        <- PR1 cross-module core
1248 1238  # PR13 npm .d.ts channel        <- PR4 lib decl kinds
1248 1243  # PR13 npm .d.ts channel        <- PR12 exactness + gate

# --- cross-milestone: M7.5 blocked by Builtin types ---
1237 1231  # PR2 scheme-URI imports        <- Builtins §6 PR D (free the converter of type_system)
1248 1231  # PR13 npm .d.ts channel        <- Builtins §6 PR D (same interop split)

# --- cross-milestone: other milestones blocked by M7.5 ---
1234 1236  # Builtins §9 shape loading     <- PR1 (reuses the registry, memoized by URI)
1234 1237  # Builtins §9 shape loading     <- PR2 (explicit import ingestion is M7.5's job)
1210 1239  # Borrow through container methods <- PR5 (needs Array and its method surface)
EOF

# Deliberately NOT an edge: Builtins §7 (#1232) commits the .esc tree and #1232 states
# it can land well ahead of M7.5. Every M7.5 PR tests against the fixture stdlib tree
# PR2 establishes, so none of them blocks on it. The shipped-tree acceptance test in
# PR5/PR6 is where that gap surfaces, not the dependency graph.
#
# Also not an edge: #1232 item 4 defers FR5 finalization into PR2. That is one item
# inside §7, not the whole phase, so blocking §7 on PR2 would contradict its own
# sequencing note. It is recorded in #1237's body instead.

# --- helpers -----------------------------------------------------------------

# GitHub's dependency API takes an issue ID, not an issue number.
declare -A ID_CACHE
issue_id() {
  local num="$1"
  if [[ -z "${ID_CACHE[$num]:-}" ]]; then
    ID_CACHE[$num]="$(gh api "repos/$REPO/issues/$num" --jq .id)"
  fi
  printf '%s' "${ID_CACHE[$num]}"
}

# Existing blockers of an issue, one number per line.
blockers_of() {
  gh api "repos/$REPO/issues/$1/dependencies/blocked_by" --paginate --jq '.[].number' 2>/dev/null || true
}

add_edge() {
  local blocked="$1" blocker="$2" note="$3"
  local label
  label="$(printf '#%s blocked by #%s' "$blocked" "$blocker")"

  if grep -qx "$blocker" <<<"$(blockers_of "$blocked")"; then
    printf '  = %-28s already set%s\n' "$label" "$note"
    return
  fi
  if (( DRY_RUN )); then
    printf '  + %-28s would add%s\n' "$label" "$note"
    return
  fi
  if gh api --method POST "repos/$REPO/issues/$blocked/dependencies/blocked_by" \
       -f "issue_id=$(issue_id "$blocker")" >/dev/null 2>&1; then
    printf '  + %-28s added%s\n' "$label" "$note"
  else
    printf '  ! %-28s FAILED%s\n' "$label" "$note" >&2
    FAILED=$((FAILED + 1))
  fi
}

# --- apply -------------------------------------------------------------------

command -v gh >/dev/null || { echo "gh CLI not found" >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "gh is not authenticated" >&2; exit 1; }

(( DRY_RUN )) && echo "DRY RUN — no changes will be made"
echo "Wiring M7.5 dependency edges in $REPO"

FAILED=0
while IFS= read -r line; do
  [[ -z "${line// }" ]] && continue
  if [[ "$line" == \#* ]]; then
    printf '\n%s\n' "${line#\# }"
    continue
  fi
  blocked="$(awk '{print $1}' <<<"$line")"
  blocker="$(awk '{print $2}' <<<"$line")"
  note="$(sed -n 's/.*#[[:space:]]*\(.*\)/  (\1)/p' <<<"${line#*"$blocker"}")"
  add_edge "$blocked" "$blocker" "$note"
done <<<"$EDGES"

echo
if (( FAILED )); then
  echo "$FAILED edge(s) failed — see above." >&2
  exit 1
fi
(( DRY_RUN )) && echo "Dry run complete." || echo "Done."
