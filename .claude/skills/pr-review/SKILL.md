---
name: pr-review
description: Review a pull request's diff for correctness bugs, cleanup, and CLAUDE.md violations, and post each finding as an inline comment on the PR. The summary also lists functions moved with trivial changes and the high-risk changes a human should check. The `code-review` job in .github/workflows/claude.yaml runs it when k4b7 comments `/code-review [low|medium|high]` on a PR. Use it in any session that is asked to review a PR and post the findings on GitHub.
---

# pr-review

The prompt that runs this skill gives the repository, the PR number, and a level of `low`, `medium`, or `high`. Use `medium` when no level is given.

## Levels

| Level | Angles to run | Findings to post at most |
|---|---|---|
| `low` | A | 3 |
| `medium` | A, B, C, Conventions | 6 |
| `high` | All eight | 10 |

At `low`, post only findings you are confident are real. At `high`, aim for recall. Post a finding whenever you can name a concrete failure scenario, even when you are not sure it is real.

## 1. Gather the diff

1. Run `gh pr view <number> --json baseRefName,headRefOid,files` to find the base branch and head commit. The base may be another stacked PR branch, not `main`.
2. Run `gh pr diff <number>` to get the unified diff. This diff is the review scope.
3. The PR branch is checked out in the working directory. Read changed files from there to see the code around each hunk.

## 2. Find candidates

Run each angle the level calls for. Each angle produces up to 6 candidates, each with a file, a line, a one-line summary, and a concrete failure scenario.

- **A. Line-by-line scan.** Read every hunk, then the whole function that encloses it. Bugs in unchanged lines of a touched function are in scope. For each line, ask what input, state, or ordering makes it wrong: an inverted condition, an off-by-one, a nil dereference, the wrong variable, a swallowed error.
- **B. Removed behavior.** For each line the diff deletes or replaces, name the invariant it enforced. Then find where the new code enforces it. If nothing does, that is a candidate: a dropped guard, a lost error path, a narrowed check, or a deleted test that covered a real case.
- **C. Cross-file trace.** For each function the diff changes, grep for its callers. Check whether the change breaks a call site through a new precondition, a changed return shape, or an ordering dependency.
- **Reuse.** Flag new code that duplicates a helper the codebase already has, and name the helper.
- **Simplification.** Flag complexity the diff adds that a simpler form would replace: derivable state, near-copies, dead code. Name the simpler form.
- **Efficiency.** Flag wasted work the diff adds, such as repeated computation or work on a hot path that could be skipped. Name the cheaper alternative.
- **Altitude.** Flag a fix that patches a symptom with a special case where a change to the underlying mechanism would be simpler and more general. Name that change.
- **Conventions.** Read the repo-root `CLAUDE.md` and any `CLAUDE.md` in a directory above a changed file. Flag a violation only when you can quote both the rule and the line that breaks it.

For cleanup, altitude, and conventions findings, the failure scenario states the cost instead of a crash.

## 3. Dedup and rank

Merge candidates that describe the same defect at the same place. Rank correctness bugs first. When the cap forces a cut, drop cleanup findings before correctness ones.

## 4. Map moved code

Run this step at every level. A moved function is one the diff deletes from one place and adds in another, in the same file or a different one. Find candidates with `git diff --color-moved=plain --color-moved-ws=allow-indentation-change origin/<base>...HEAD`, then compare each deleted body with its added counterpart.

A move is trivial when the only differences are these:

- Parameters, locals, or the function itself are renamed.
- A method becomes a function, or a function becomes a method, with the receiver moved to or from the parameter list.
- Imports, package qualifiers, comments, or formatting change.

Record each trivial move as the old location, the new location, and the changes in one short phrase, such as "renamed `t` to `table`" or "method on `checker` became a function taking `c *checker`". A move that changes behavior is not trivial. Leave it out of this list and review its differences like any other change. Don't post findings about code a trivial move carries over unchanged, since the move did not introduce it.

## 5. Flag high-risk changes

Run this step at every level. A high-risk change is one where a mistake is costly or easy to miss, whether or not you found a bug in it. Flag a change when it does one of these:

- It loosens a check in `internal/solver/`, so the compiler accepts a program it rejected before. Removing a diagnostic, narrowing when a rule fires, or adding an early return before a check all count.
- It deletes or disables a test, or changes an expected snapshot, fixture, or error message so that an error becomes success.
- It changes shared infrastructure that many passes read, such as the visitors in `internal/ast/visitor.go` and `internal/soltype/visitor.go`, `internal/soltype/`, or caches.
- It changes `.github/workflows/`, permissions, secrets, or anything else that runs with repository credentials.
- It replaces generated data, such as files under `internal/interop/data/`, in bulk.

For each one, record the file and a one-line statement of what a human reviewer should confirm. Keep the list to the changes that most need a second look, at most 5.

## 6. Post

1. For each finding, check that its line falls inside a diff hunk on the new side. Post it with `mcp__github_inline_comment__create_inline_comment` and `confirmed: true`. The body starts with a bold one-line summary and its category, then gives the failure scenario.
2. When a finding's line is outside every hunk, attach it to the nearest changed line it concerns and name the real line in the body.
3. Last, post one summary with `gh pr comment <number> --body ...`. It has three sections, each with a sentence-case heading:
   - **Findings:** each finding on one line with its file and line. When nothing survives, say that the review found no issues.
   - **Moved code:** each trivial move from step 4 on one line. Leave the section out when there are none.
   - **Needs human review:** each high-risk change from step 5 on one line. Leave the section out when there are none.

Write every comment by the prose rules in `CLAUDE.md`. Don't send the review as a chat message, since only GitHub comments reach the PR.
