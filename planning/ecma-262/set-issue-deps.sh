#!/bin/sh
#
# Wire the ECMA-262 workstream issue dependencies (milestone 3) as native
# GitHub "blocked by" links, mirroring the dependency graph in
# planning/ecma-262/implementation_plan.md.
#
# Each edge means the blocker must land before the blocked issue, i.e. the
# blocked issue is "blocked by" the blocker. Requires the `gh` CLI,
# authenticated with repo write access.
#
# POSIX sh — works with macOS's default shell. Run either way:
#   sh planning/ecma-262/set-issue-deps.sh
#   ./planning/ecma-262/set-issue-deps.sh
# Preview without making changes:
#   DRY_RUN=1 sh planning/ecma-262/set-issue-deps.sh
#
set -eu

OWNER=escalier-lang
REPO=escalier

# The dependencies API keys issues by their global numeric id, not their
# number, so resolve number -> id for each blocker.
apply_edge() {
  blocker_num=$1
  blocked_num=$2
  label=$3
  echo "$label"
  if [ -n "${DRY_RUN:-}" ]; then
    return 0
  fi
  blocker_id=$(gh api "repos/$OWNER/$REPO/issues/$blocker_num" --jq .id </dev/null)
  # A duplicate link returns 422; tolerate it so the script is re-runnable.
  if ! printf '{"issue_id": %s}' "$blocker_id" | gh api \
      "repos/$OWNER/$REPO/issues/$blocked_num/dependencies/blocked_by" \
      --method POST \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      --input - >/dev/null 2>&1; then
    echo "  (already linked or failed — re-run with DRY_RUN=1 to inspect)"
  fi
}

# Columns: <blocker#> <blocked#> <human-readable label>
# The pairs are the edges of the plan's mermaid dependency graph, with the
# section labels resolved to the milestone 3 issue numbers.
while read -r blocker blocked label; do
  [ -z "$blocker" ] && continue
  apply_edge "$blocker" "$blocked" "$label"
done <<'EOF'
1192 1193 §2 (#1193) blocked by §1 (#1192)
1193 1194 §3 (#1194) blocked by §2 (#1193)
1194 1195 §4.2 (#1195) blocked by §3 (#1194)
1194 1196 §4.1 (#1196) blocked by §3 (#1194)
1195 1196 §4.1 (#1196) blocked by §4.2 (#1195)
1196 1197 §4.3 (#1197) blocked by §4.1 (#1196)
1195 1197 §4.3 (#1197) blocked by §4.2 (#1195)
1197 1198 §5 (#1198) blocked by §4.3 (#1197)
1198 1199 §6 (#1199) blocked by §5 (#1198)
1199 1200 §7 (#1200) blocked by §6 (#1199)
1196 1201 §8.1 (#1201) blocked by §4.1 (#1196)
1200 1201 §8.1 (#1201) blocked by §7 (#1200)
1197 1202 §8.2 (#1202) blocked by §4.3 (#1197)
1195 1203 §9.1 (#1203) blocked by §4.2 (#1195)
1203 1204 §9.2 (#1204) blocked by §9.1 (#1203)
1198 1204 §9.2 (#1204) blocked by §5 (#1198)
1203 1205 §9.3 (#1205) blocked by §9.1 (#1203)
1204 1206 §9.4 (#1206) blocked by §9.2 (#1204)
1205 1206 §9.4 (#1206) blocked by §9.3 (#1205)
1200 1206 §9.4 (#1206) blocked by §7 (#1200)
1200 1207 §10 (#1207) blocked by §7 (#1200)
1201 1208 §11 (#1208) blocked by §8.1 (#1201)
1202 1208 §11 (#1208) blocked by §8.2 (#1202)
1205 1208 §11 (#1208) blocked by §9.3 (#1205)
1206 1208 §11 (#1208) blocked by §9.4 (#1206)
1200 1208 §11 (#1208) blocked by §7 (#1200)
EOF

echo "Done."
