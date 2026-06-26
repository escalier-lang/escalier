---
name: dev-task
description: End-to-end development workflow for the Escalier compiler. Use this whenever the user hands off a coding task to carry through to completion — working a GitHub issue, fixing a bug, implementing a feature, or building out a PR from an implementation plan. It implements the change, runs /code-review and fixes every valid finding in severity order, then audits the comments in the diff against the prose guidance in CLAUDE.md. Reach for it even when the user just says "work on this" or "implement this" without naming the workflow.
---

# dev-task

A development ask is not done when the code compiles. It's done when the change is correct, has survived review, and reads the way the rest of the codebase reads. This skill runs that full arc so nothing is left half-finished: implement, review-and-fix, then clean the prose.

Run the three phases in order. Each one feeds the next — review findings often touch code you just wrote, and the comment audit covers comments review may have added or moved.

## Phase 1 — Implement

Start from the user's ask, whatever form it takes: a GitHub issue, a bug report, a feature request, or an implementation plan for a PR.

1. **Understand before changing.** Read the relevant code so you know how the existing pipeline works. The compiler stages and their directories are listed in CLAUDE.md under "Repo layout" — lexer/parser, AST, type system, checker, codegen/printer, resolver/dep_graph. Find where your change belongs before you start editing.
2. **Match the surrounding code.** Use the existing AST visitor and type-system visitor instead of hand-rolling traversals, use the `Set` ADT instead of raw maps, and follow the other conventions in CLAUDE.md's "Code conventions". Your code should be indistinguishable in style from the code next to it.
3. **Verify the change.** Build with `make` and run the tests. Use `go test ./...` for the whole suite or `go test ./cmd/...` for the fixture tests. If you changed checker, codegen, or printer output, refresh snapshots and fixtures together so they stay in sync: `UPDATE_SNAPS=true go test ./...` and `UPDATE_FIXTURES=true go test ./cmd/...`. Don't try out source by running the CLI directly — add a fixture under `fixtures/<name>/lib/index.esc` and exercise it through the harness, as CLAUDE.md describes.
4. **Add tests for new behavior.** Follow the testing conventions in CLAUDE.md: assert the full error message, write inputs as Escalier source, prefer table-driven tests with `testify`'s `require.*`, and use inline snapshots for assertions that would otherwise need many drill-down checks.

Land the implementation as a working, tested change before moving on. The next two phases assume the diff compiles and passes.

## Phase 2 — Review and fix

Run the `/code-review` skill on your working diff. It surfaces correctness bugs and reuse/simplification/efficiency cleanups.

Then triage every finding before touching anything:

- **Decide validity.** Not every finding is real. A finding is valid when it identifies a genuine bug, a correctness gap, or a cleanup that makes the code clearly better. If a finding is wrong, or rests on a misreading of the code, or its "fix" would make the code worse, don't apply it — note briefly why you're skipping it so the user can see your reasoning.
- **Fix in severity order.** Address valid findings from most to least severe — correctness bugs and crashes first, then logic gaps, then cleanups and style. Working highest-severity-first means the most important fixes land even if something later forces you to stop.
- **Re-verify after fixing.** A fix can break a test or introduce a new problem. Rebuild and re-run the relevant tests after applying fixes, and refresh snapshots and fixtures if output changed.

If your fixes were substantial, it's reasonable to run `/code-review` once more to confirm the new code didn't introduce fresh issues. Use judgment — a second pass over a small, clean fix is wasted effort.

## Phase 3 — Audit comments against CLAUDE.md

Review every comment in your diff — both comments you wrote and any that review added or relocated — and rewrite the ones that don't conform to CLAUDE.md's prose guidance. This phase exists because comments are the part of a change most likely to drift from house style, and they're what the next reader leans on.

Read your diff's comments against the "Code conventions" and the two "Writing Prose" sections in CLAUDE.md. The recurring offenders to watch for:

- **Parentheticals.** CLAUDE.md is emphatic that asides in parentheses make comments hard to parse. Cut the aside if it isn't load-bearing, promote it to its own sentence if it matters, or fold it in with a comma if it's short and essential. Never nest or stack them. Parentheses are fine only for a short essential clarification like a code reference or a concrete example.
- **Misused colons.** Use a colon only to introduce a list, a definition, or a direct elaboration that completes the clause before it. Don't use one mid-sentence to tack on a second thought — split it into two sentences or join with a conjunction.
- **Overlong sentences.** Prefer short, complete sentences over one long sentence held together by dashes and semicolons. One idea per sentence. For a multi-step process, lay the steps out as a list rather than stringing them through a paragraph.
- **Undefined or vague terms.** Define a coined or technical term the first time it appears, in plain language, before you rely on it. Replace vague verbs like "handles", "manages", "drives", or "supplies" with the precise action — produces, reads, returns, mutates, consults. Name the value the code passes around, not the technique that produced it.
- **Unanchored claims.** When a comment cites specific output such as a rendered type or an inferred value, include the source snippet that produces it so the reader can trace it.
- Write comments about what the code does now, not what it did before a change. Drop phrasing like "previously", "used to", "is now", "no longer", and "the old behavior", and don't make a PR number or milestone the subject of a sentence. A comment that narrates the diff goes stale the moment the next change lands. Describe the current behavior and its rationale instead, and leave the history to git.

Treat each non-conforming comment as draft-then-revise: rewrite it, then reread it as someone with no prior context and fix anything that still needs a second pass.

## Wrapping up

When all three phases are done, give the user a short summary: what you implemented, which review findings you fixed and which you skipped and why, and which comments you rewrote. Don't open a pull request unless the user asked for one.
