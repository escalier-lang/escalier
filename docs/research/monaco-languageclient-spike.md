# Spike: monaco-languageclient in the playground

This spike asks whether the playground should replace its hand-written Monaco ↔ LSP
adapters with the `monaco-languageclient` npm package. The adapters are
`playground/src/language.ts`, `completions.ts`, `signature-help.ts`,
`code-actions.ts`, `ranges.ts` and the provider half of `semantic-tokens.ts`.

The spike code is on the throwaway branch
[`claude/monaco-languageclient-spike`](https://github.com/escalier-lang/escalier/tree/claude/monaco-languageclient-spike).
It isn't meant to merge.

## Recommendation

Keep the current adapters. The swap works, but it costs more than it saves:

- The page downloads 2.4 times as much JavaScript at load: 2,237 KiB gzipped against
  933 KiB. The build emits 31 JavaScript chunks instead of 2, and pulls in 43
  `@codingame` packages.
- Startup and completion latency don't improve. The spike was 70 to 290 ms slower to
  first diagnostics, which is within run-to-run noise at the low end.
- The two behaviours specific to Escalier still need custom code. Holding semantic
  tokens back during a pending change needs middleware that reaches into
  vscode-languageclient's feature objects. Hover verbosity (#2715) needs a Monaco hover
  provider written by hand, because vscode-languageclient's hover feature drops
  Monaco's `verbosityRequest`.
- The 930 source lines it removes are small, tested adapters over a stable Monaco API.
  The 276 lines it adds have no tests yet and depend on `getFeature(...)` and
  `onDidChangeSemanticTokensEmitter`, which aren't part of vscode-languageclient's
  documented API.
- `@codingame/monaco-vscode-api` follows VS Code's monthly releases, and
  monaco-languageclient pins one major of it. Each upgrade moves the 43 `@codingame`
  packages in lockstep.

Revisit the decision if the playground needs a VS Code feature that Monaco alone lacks,
such as TextMate grammars, VS Code themes or a workbench layout, or if the server gains
many more LSP features than the eight the playground uses today.

## Bug found on `main`

The stdin shim in `playground/src/lsp-client/client.ts` copies each message chunk into
the Go server's read buffer whole. The server reads stdin through a 4,096-byte
`bufio.Reader`, so a chunk longer than that panics the server with
`panic: JavaScript error: offset is out of bounds`. On `main`, appending 120 lines of
`export val vN = N // padding padding` to the hello-world file reproduces the panic.
The `didChange` that sends the full text is then over 4 KiB.

The spike hit the same panic at startup, since vscode-languageclient's `initialize`
request is over 4 KiB. The spike fixes it by having `SimpleStream.read(maxLength)`
return at most the bytes the server asked for and keep the rest queued. That fix is
independent of this decision and should land on its own.

## Measurements

All measurements were taken on `main` at 8978f0c and on the spike branch. Each page was
served by `vite preview` from a `pnpm build:prod` build and driven by Playwright with
Chromium at `/opt/pw-browsers/chromium`. Every run used a new browser context with an
empty cache.

| Measurement | `main` | Spike | Change |
| --- | --- | --- | --- |
| JS emitted, raw | 3,810 KiB (2 files) | 14,145 KiB (31 files) | +271% |
| JS emitted, gzipped | 998 KiB | 3,951 KiB | +296% |
| CSS emitted, raw | 143 KiB (1 file) | 524 KiB (6 files) | +267% |
| CSS emitted, gzipped | 23 KiB | 66 KiB | +187% |
| Extra WebAssembly emitted (`onig.wasm`, `diff_wasm_bg.wasm`) | none | 572 KiB raw, 206 KiB gzipped | not fetched at load |
| JS fetched at load (gzipped transfer) | 933 KiB | 2,237 KiB | +1,304 KiB |
| CSS fetched at load (gzipped transfer) | 23 KiB | 37 KiB | +14 KiB |
| Fonts, HTML and JSON fetched at load | 82 KiB | 153 KiB | +71 KiB |
| First-load download on the loading screen | 23.87 MiB | 23.87 MiB | none |
| Page load to first diagnostics, median of 3 (batch 1) | 3,260 ms | 3,332 ms | +72 ms |
| Page load to first completion list, median of 3 (batch 1) | 3,994 ms | 4,013 ms | +19 ms |
| Page load to first diagnostics, median of 3 (batch 2) | 2,989 ms | 3,275 ms | +286 ms |
| Page load to first completion list, median of 3 (batch 2) | 3,658 ms | 3,958 ms | +300 ms |
| Lines removed from `playground/src` | | 930 source, 1,272 test | |
| Lines added to `playground/src` | | 276 source, 3 test | |
| Playwright e2e suite | 29 pass (CI) | 29 pass | |
| Vitest unit project | passes | 239 pass after one new mock | |

Notes on the table:

- "Emitted" counts every `.js` and `.css` file `vite build` writes to
  `dist/assets`, including workers and chunks the page never fetches. The spike emits
  the VS Code extension-host worker (1.9 MiB raw) and `iconv-lite`, among others.
- "Fetched at load" sums the response body sizes of everything the page requested
  before the first completion list appeared. `vite preview` serves JS and CSS gzipped.
- The loading screen (`playground/src/startup.tsx`) counts only `lsp-server.wasm`
  (23,061,496 bytes) and the 54 standard library files (1,966,050 bytes). The spike
  doesn't change either, so the figure is the same. The extra JavaScript arrives before
  the loading screen renders and isn't counted there.
- "First diagnostics" is the moment the server first writes a
  `textDocument/publishDiagnostics` message to stdout. A Playwright init script wraps
  the `fs.write` shim to record it, so the same probe runs unchanged on both builds.
- "First completion list" is the moment the suggest widget shows its first row. The
  probe clicks the editor after first diagnostics, types `export val zz = gre` on a new
  line and presses Ctrl+Space. The list shows `greet` first on both builds.
- Batch 1 ran on an earlier spike commit and batch 2 on the final one. The two `main`
  batches differ by 270 ms, so differences under about 300 ms are noise.

## What monaco-languageclient needs from the playground

- `monaco-editor-core` becomes an npm alias for `@codingame/monaco-vscode-editor-api`
  37.6.1, so every existing `import * as monaco from 'monaco-editor-core'` still
  resolves. `vscode-languageclient` 10.1.2 and the `vscode` alias for
  `@codingame/monaco-vscode-extension-api` become direct dependencies. The lockfile
  grows by 518 lines.
- `vite.config.ts` needs `worker: { format: 'es' }`. Without it the build fails with
  `Invalid value "iife" for option "worker.format" - UMD and IIFE output formats are not supported for code-splitting builds.`
  The dev server works without an esbuild plugin.
- `MonacoVscodeApiWrapper` runs in `classic` mode with the `EditorService` views type.
  Classic mode keeps the Monarch tokenizers and the standalone theme service. Its
  `start()` must finish before the first model or editor exists, so `main.tsx` starts
  it at the top of `start()`, alongside the downloads.
- `Client` exposes its `ReadableStreamMessageReader` and `WriteableStreamMessageWriter`
  through a new `messageTransports()` method. It creates its own `MessageConnection`
  only on first use, so the language client is the one reader of stdout.
  `client.test.ts` keeps using the typed request methods to test the server.
- `MonacoLanguageClient` takes those transports directly. It sends `initialize` with
  `workspaceFolder` set to `file:///`, which the server reads as the root URI.

## Carry-over of custom behaviours

| Behaviour | Result | What it takes |
| --- | --- | --- |
| Diagnostics, completion, completion resolve, hover, signature help, go-to-definition, code actions | Works as-is | vscode-languageclient's built-in features. Code actions also get each diagnostic's `data` back, so `markerDiagnostics` isn't needed. |
| Recompiling an open `.esc` file when its diagnostics arrive | Needs middleware | `handleDiagnostics` calls `next`, then sends `workspace/executeCommand` with `compile` for an open document. |
| Theme pair `escalier-dark` / `escalier-light` and the light/dark/system switch (#2662) | Works as-is | Classic mode keeps `monaco.editor.defineTheme` and `setTheme`. The `theme.spec.ts` e2e test passes. Extended mode would need VS Code theme contributions instead. |
| Font styles for the `mutable` and `reassignable` modifiers (`modifierTokenRules`) | Works as-is | The same theme rules apply. `enforceSemanticHighlighting` turns semantic highlighting on. Both `semantic-tokens.spec.ts` tests pass. |
| Monarch tokenizer (`monarch-language.ts`) and the bracket config | Works as-is | Classic mode installs the Monarch service override. Extended mode would need a TextMate grammar contributed as an extension. |
| JS/TS highlighting of compiled output | Works as-is | `basic-languages.ts` and `monacoCorePlugin` resolve to the aliased editor API. The compiled-JavaScript test in `syntax-highlighting.spec.ts` passes. |
| Flushing the debounced `didChange` before completion and hover | Works as-is, with a different delay | With full sync, vscode-languageclient holds each change back and sends it 250 ms after the last edit, or before any request. The delay is hard-coded, against 500 ms today. |
| Holding semantic tokens back while a change is pending | Needs middleware | The built-in flush also runs before each semantic-tokens request, which starts a check per request. `didChange` middleware records the pending document, the didChange feature's `onNotificationSent` clears it, and `provideDocumentSemanticTokens` middleware throws the `busy` error meanwhile. The refresh after diagnostics fires `onDidChangeSemanticTokensEmitter` on the semantic-tokens feature's provider. |
| Hover verbosity levels (#2715) | Not possible through vscode-languageclient | Its hover feature calls `provideHover(document, position, token)` and drops Monaco's `context`. The spike sets `provideHover` middleware to return `null` and registers a Monaco hover provider that sends `textDocument/hover` through `languageClient.sendRequest`. That provider reads `verbosityRequest` and sets `canIncreaseVerbosity`. Clicking + in the spike moves the hover from level 0 to level 1. |

## Test impact

- The Monaco mock (`playground/src/__mocks__/monaco-editor-core.ts`) stays as it is.
  The alias in the root `vitest.config.ts` still replaces `monaco-editor-core` by name.
- `vscode-languageclient/browser` resolves only under the `browser` export condition,
  so Node can't load it. `playground.test.ts` failed with
  `No known conditions for "./browser" specifier in "vscode-languageclient" package`
  until it mocked `./language-client`, as `editor.test.tsx` already mocks the language
  module. A test that loads the real client would need `resolve.conditions` set to
  `browser` in the unit project.
- `monaco-core-plugin.ts` needs no change. It resolves to the aliased package.
- The unit tests for the removed adapters go with them: 1,272 lines across
  `language.test.ts`, `completions.test.ts`, `signature-help.test.ts`,
  `code-actions.test.ts`, `ranges.test.ts` and the provider half of
  `semantic-tokens.test.ts`. The new middleware and the hover provider would need their
  own tests. A comparable suite is likely 150 to 250 lines, which the line counts above
  leave out.
- The Playwright suite passes unchanged on the spike, through `pnpm dev`. No e2e test
  covers completion or hover, so the spike checked both with one-off probes.
