#!/usr/bin/env bash
#
# Fails when a generated tree does not match what its generator writes.
#
#     check-generated-tree.sh <tree-dir> -- <generator> [args...]
#
# The check runs the generator, stages <tree-dir>, and diffs the index.
# Staging is what makes it total. `git diff --exit-code` compares tracked
# files only, so a run that emits a package the tree does not carry
# leaves that package untracked and the check passes. Diffing the index
# reports that case alongside a package whose contents changed and a
# package the run no longer emits.
#
# The staging is forced. An ignore rule matching a generated file would
# otherwise keep it out of the index, and the check would report a match
# it never made.
#
# The check reads the index against HEAD, so it assumes nothing under
# <tree-dir> was staged before it ran. It leaves what the run wrote
# staged. CI runs it on a fresh checkout, where both hold.

set -euo pipefail

usage='usage: check-generated-tree.sh <tree-dir> -- <generator> [args...]'

if [ "$#" -lt 3 ] || [ "$2" != '--' ]; then
    printf '%s\n' "$usage" >&2
    exit 2
fi

tree_dir=$1
shift 2

"$@"

git add --all --force -- "$tree_dir"

if git diff --cached --quiet -- "$tree_dir"; then
    printf 'check-generated-tree: %s matches its inputs\n' "$tree_dir"
    exit 0
fi

{
    printf 'check-generated-tree: %s does not match its inputs.\n' "$tree_dir"
    printf 'Re-run the generator and commit what it writes.\n'
    git --no-pager diff --cached -- "$tree_dir"
} >&2
exit 1
