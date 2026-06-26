# Escalier

Escalier is a programming language with a Go-based compiler. The compiler pipeline lives under [internal/](internal/) and the CLI entry points under [cmd/](cmd/).

## Responding in conversation

- Be concise. Lead with the direct answer, usually one or two sentences, and stop there.
- Don't pre-empt follow-up questions with extra sections, caveats, or trade-off surveys. The user will ask about anything they want expanded.
- Don't give a long version followed by a short recap — just give the short version.
- No multi-heading structure unless asked. This applies to analysis and planning questions too, not just quick lookups.

## Repo layout

- [internal/lexer_util/](internal/lexer_util/), [internal/parser/](internal/parser/) — lexing and parsing
- [internal/ast/](internal/ast/) — AST definitions and visitor
- [internal/type_system/](internal/type_system/) — type representations and visitor
- [internal/checker/](internal/checker/) — type inference and checking
- [internal/codegen/](internal/codegen/), [internal/printer/](internal/printer/) — output
- [internal/resolver/](internal/resolver/), [internal/dep_graph/](internal/dep_graph/) — module/name resolution
- [internal/set/](internal/set/) — `Set` ADT (use this instead of `map[T]struct{}`)
- [cmd/escalier/](cmd/escalier/), [cmd/lsp-server/](cmd/lsp-server/) — CLI entry points

## Build & test

- Build: `make` (or `make escalier` / `make lsp-server`)
- Run all tests: `go test ./...`
- Run fixture tests only: `go test ./cmd/...`
- Update snapshots: `UPDATE_SNAPS=true go test ./...` (uses `github.com/gkampitakis/go-snaps`)
- Update fixtures: `UPDATE_FIXTURES=true go test ./cmd/...`
- After changing checker/codegen/printer output, re-run with both vars set so snapshots and fixtures stay in sync.
- Don't invoke the CLI directly with `go run ./cmd/escalier check <file>` to try out source. Add a fixture under `fixtures/<name>/lib/index.esc` (with a `package.json`) and run `go test ./cmd/...` — the fixture harness exercises the same pipeline and keeps the test reproducible.

## Code conventions

- When traversing tree-like structures, use the existing visitor for that tree — see [internal/ast/visitor.go](internal/ast/visitor.go) for AST and [internal/type_system/visitor.go](internal/type_system/visitor.go) for types. Don't hand-roll a new traversal.
- Use the `Set` ADT from [internal/set/](internal/set/) (`set.NewSet[T]()`, `set.FromSlice(...)`) instead of `map[T]struct{}` or `map[T]bool`.
- Don't shadow Go builtins (`any`, `error`, `new`, `len`, etc.) or imported type/package aliases with local identifiers. Pick a distinct name (e.g. `anyT`, `errVal`).
- Avoid parentheticals in comments — they make comments hard to read, especially when nested or combined with em-dashes. Rewrite the aside as a plain sentence, fold it into the main clause, or drop it. Reserve parentheses for short, essential clarifications like a code reference or a concrete example. Prefer several short sentences over one sentence carrying multiple asides.
- Write comments about what the code does now, not what it did before a change. Drop phrasing like "previously", "used to", "is now", "no longer", and "the old behavior", and don't make a PR number or milestone the subject of a sentence. A comment that narrates the diff goes stale the moment the next change lands. Describe the current behavior and its rationale instead, and leave the history to git.

## Writing tests

- Assert the full error message, not a substring.
- Write test inputs as Escalier source. Assert inferred types using Escalier type-annotation syntax in strings. See [internal/checker/tests/class_test.go](internal/checker/tests/class_test.go) for the canonical pattern.
- Prefer table-driven tests.
- Use `github.com/stretchr/testify` for assertions instead of hand-rolled `if x != y { t.Fatalf(...) }` blocks. Prefer `require.*` (stops on first failure, matching the `t.Fatalf` pattern) over `assert.*`. Common conversions: `require.Equal(t, expected, actual)`, `require.Same(t, expected, actual)` for pointer identity, `require.NotNil(t, x)`, `require.Contains(t, m, k)`, `require.NotContains(t, m, k)`, `require.Empty(t, s)`, `require.Error(t, err)`, `require.NoError(t, err)`. Split compound conditions (`x == nil || x.Type != fn`) into separate `require` calls so failures point at the actual problem.
- When a test would otherwise need many drill-down assertions about an AST or type tree (walking children, checking field after field, type-asserting through wrappers), render the subtree to a string and use `snaps.MatchInlineSnapshot` instead. Use `printer.Print(node, printer.DefaultOptions())` for AST nodes that the printer supports (Escalier-source form, best for review) or `snapshot.String(node)` from [internal/snapshot/](internal/snapshot/) for raw struct dumps. First-run flow: pass `nil` as the expected value and run with `UPDATE_SNAPS=true` — go-snaps writes the literal back into the test file. Reserve targeted `require.*` checks for the one or two facts that aren't visible in the printed form.
- Disable a test that asserts behavior a later milestone will flip rather than deleting it or letting it pin under-checked behavior. Update the assertions in place to the expected future error, wrap the function body in `/* */`, and add a `DISABLED until <milestone>` comment above it that names the milestone and what changes. Re-enable by removing the wrapper when the milestone lands. Use this for two cases: an assertion that locks in behavior a planned PR will reject, and an assertion that succeeds today only because the current checker under-checks a case the planned PRs will catch.

## Ephemeral scripts

When a one-off task needs more logic than a short shell pipeline (parsing JSON, multi-step text processing, scanning many files, ad-hoc analysis), write a Node script in `/tmp/<name>.mjs` and run it with `node`. Prefer this over inline bash with awk/sed/jq chains or a Python script — Node scripts are easier for the user to read and audit.

- Use ES modules (`.mjs`), top-level `await`, and the Node standard library (`node:fs`, `node:path`, `node:os`, etc.).
- Don't add npm dependencies for ephemeral work.
- Reach for bash only for genuinely shell-shaped tasks (one-line pipelines, file globs, process control).

## GitHub issues

- When creating issues with `gh`, do not escape strings or backticks in the Markdown body — `gh` passes the body through as-is, and escaping produces literal backslashes in the rendered issue.

# Writing Prose: Punctuation and sentence structure

- Use colons only for their standard jobs: introducing a list, a definition, or
  a direct elaboration that completes the clause before it. Do not use a colon
  mid-sentence to tack on an aside or a second thought. If you're tempted to
  write "X does Y: which means Z", rewrite it as two sentences or join with a
  conjunction.
- Don't use a colon where a comma, dash, or period would read more naturally.
  A colon makes the reader stop and expect a list or payoff; if none follows,
  it's jarring.
- Avoid parentheticals. They interrupt the sentence and force the reader to
  hold the main clause in mind while processing the aside. In almost every case
  one of these is better:
    - Cut the parenthetical entirely if it's not load-bearing.
    - Promote it to its own sentence if it matters.
    - Fold it into the sentence with a comma if it's short and essential.
- Never nest a parenthetical inside another, and never stack two parentheticals
  in one sentence.
- Don't use parentheses to define or gloss a term inline. Define it in the
  preceding or following sentence instead.
- Prefer short, complete sentences over long ones held together by dashes,
  semicolons, and asides. One idea per sentence.
- When explaining a process with multiple steps, lay the steps out as a list
  rather than stringing them through a paragraph. Use a numbered list when the
  order matters and a bulleted list when it doesn't. One step per item reads far
  more easily than steps joined by semicolons or "then ... then ...".
- Read each sentence as if a human has to parse it left to right with no
  rereading. If understanding it requires jumping back to an earlier clause,
  restructure it.

# Writing Prose: Word choice and explaining code

- Define a technical or coined term the first time you use it. Terms like
  "co-occurrence", "representative", or "closure" mean nothing to a reader who
  lacks your context. Give a one-line plain-language definition before you rely
  on the term, not after.
- Ground an abstract claim in a concrete example. When a comment cites specific
  output such as a rendered type or an inferred value, include the source
  snippet that produces it. The reader can then trace where the output comes
  from instead of taking it on faith.
- Use precise verbs. Replace vague verbs like "supplies", "handles", "drives",
  and "manages" with the actual action: produces, reads, returns, mutates,
  consults. A vague verb hides what the code does.
- Name the value, not the technique that produced it. Don't write "union-find"
  when you mean the merge classes it computed, or "the visitor" when you mean
  the walk's result. Refer to the thing the code hands around.
- Treat a comment as draft-then-revise, not one-shot. After writing any comment
  longer than a sentence or two, reread it as someone with no prior context.
  Fix every unexplained term and every sentence that needs a second pass.
