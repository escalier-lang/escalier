#!/usr/bin/env bash
#
# Wire the ECMA-262 workstream issue dependencies (milestone 3) as native
# GitHub "blocked by" links, mirroring the dependency graph in
# planning/ecma-262/implementation_plan.md.
#
# Each edge "A B" means A must land before B, i.e. B is blocked by A.
# Requires the `gh` CLI, authenticated with repo write access.
#
# Usage:
#   ./planning/ecma-262/set-issue-deps.sh           # apply the dependencies
#   DRY_RUN=1 ./planning/ecma-262/set-issue-deps.sh # print what would be done
#
set -euo pipefail

OWNER=escalier-lang
REPO=escalier

# Section label -> issue number (created in milestone 3).
declare -A NUM=(
  [1]=1192 [2]=1193 [3]=1194
  [4.2]=1195 [4.1]=1196 [4.3]=1197
  [5]=1198 [6]=1199 [7]=1200
  [8.1]=1201 [8.2]=1202
  [9.1]=1203 [9.2]=1204 [9.3]=1205 [9.4]=1206
  [10]=1207 [11]=1208
)

# Dependency edges "<blocker> <blocked>" from the plan's mermaid graph.
EDGES=(
  "1 2"
  "2 3"
  "3 4.2"
  "3 4.1"
  "4.2 4.1"
  "4.1 4.3"
  "4.2 4.3"
  "4.3 5"
  "5 6"
  "6 7"
  "4.1 8.1"
  "7 8.1"
  "4.3 8.2"
  "4.2 9.1"
  "9.1 9.2"
  "5 9.2"
  "9.1 9.3"
  "9.2 9.4"
  "9.3 9.4"
  "7 9.4"
  "7 10"
  "8.1 11"
  "8.2 11"
  "9.3 11"
  "9.4 11"
  "7 11"
)

# The dependencies API keys issues by their global numeric id, not their
# number. Resolve number -> id once and cache it.
declare -A ID_CACHE=()
resolve_id() {
  local number="$1"
  if [[ -z "${ID_CACHE[$number]:-}" ]]; then
    ID_CACHE[$number]=$(gh api "repos/$OWNER/$REPO/issues/$number" --jq .id)
  fi
  printf '%s' "${ID_CACHE[$number]}"
}

for edge in "${EDGES[@]}"; do
  read -r blocker blocked <<<"$edge"
  blocker_num=${NUM[$blocker]}
  blocked_num=${NUM[$blocked]}

  echo "§$blocked (#$blocked_num) blocked by §$blocker (#$blocker_num)"
  if [[ -n "${DRY_RUN:-}" ]]; then
    continue
  fi

  blocker_id=$(resolve_id "$blocker_num")
  # POST is idempotent enough: a duplicate returns 422, which we tolerate.
  if ! printf '{"issue_id": %s}' "$blocker_id" | gh api \
      "repos/$OWNER/$REPO/issues/$blocked_num/dependencies/blocked_by" \
      --method POST \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      --input - >/dev/null 2>&1; then
    echo "  (already linked or failed — re-run with DRY_RUN=1 to inspect)"
  fi
done

echo "Done."
