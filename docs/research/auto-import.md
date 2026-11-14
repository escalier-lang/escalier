# Import suggestions in the language server and playground

This note scopes two editor features for names that a pseudo-package exports but
the current file has not imported. A pseudo-package is one of the `std:*` or
`web:*` packages the compiler ships as `.esc` files, such as `std:math`. The two
features mirror what TypeScript offers.

1. A completion item for a name that is not in scope. Accepting it writes the
   qualified name at the cursor and adds the `import "std:…"` line through the
   item's `additionalTextEdits`.
2. A quick fix on an `Unknown identifier` or `cannot find type` diagnostic that
   adds the import and qualifies the name.

No code changes come with this note. The plan at the end splits the work into
PRs.

## Where names come from

### On `main`

`import "uri"` is the only import form, since #1472. It binds the package as a
namespace under the statement's local name. `ImportStmt.LocalName` in
`internal/ast/stmt.go` returns the `as` alias when there is one and
`DeriveImportName(specifier)` otherwise, so `import "std:math"` binds `math`.
There is no named import, so an import never binds a bare member.

`bindImportedNamespace` in `internal/solver/imports.go` binds the namespace into
the file's own scope. When the package exports a value under the binding's own
name, such as `console` in `std:console` or `fetch` in `web:fetch`, that value is
bound too, so `console.log(…)` reads it. A module-level value of the same name
wins, and the import then binds the namespace alone.

Each `lib/` file has its own import scope, a child of the module scope, so an
import in one file is not visible in a sibling (`bindFileImports`). A `bin/`
script binds its `Script.Imports` the same way. The parser requires imports to
come first in both. `ParseLibFiles` parses them before any declaration, and a
script reports `An import must come before the script's first statement`
(`internal/parser/parser.go`).

A project's own module or script reaches `std:prelude` with no import and
nothing else, since #2692. `inferModuleOn` and `inferScriptOn` parent the entry
scope at the prelude scope. `console.log("hi")` with no import reports
`Unknown identifier: console`, and `Math.PI` reports `Unknown identifier: Math`.

`internal/solver/ambient.go` still fills a globals scope from the packages in
`globalsPackages`, but only an npm package's `.d.ts` reads it. `bindGlobals`
binds a value whose `@js` path is dotted, such as `@js("Math.sqrt") export
declare fn sqrt`, into a namespace named by the prefix. Those `Math` and `Intl`
namespaces are what a `.d.ts` sees. A project file never does.

`web:core` is the one exception to qualified access. `bindCoreExports` in
`imports.go` binds its exports unprefixed into the importing file.

### After #2537

#2537 is still open. It renames `web:core` to `web:prelude`, makes it ambient
beside `std:prelude`, and removes `bindCoreExports`. After it lands, `Event`
and `AbortController` need no import, and every other name outside the two
preludes does.

### What the user writes after the import

The user always writes a qualified name. `sqrt` becomes `math.sqrt`, `Collator`
in a type annotation becomes `intl.Collator`, and `Math.sqrt` becomes
`math.sqrt`. A package that exports a value under its own name is the one case
where the bare name is right. `console`, `fetch` and `performance` read as
written once the import exists.

So every suggestion has two parts.

- An edit at the name, unless it is an own-name value. For a member name such as
  `sqrt` it writes `math.sqrt`. For a dotted `@js` head such as `Math` it
  rewrites the head to the binding name `math`.
- An import line, unless the file already imports the package.

## Where the package list and exports live

`resolveStdlibPath` in `internal/solver/stdlib_import.go` maps `scheme:pkg` to
`<dir>/<scheme>/<pkg>.esc`. `stdlibdir.StdlibDir` finds `<dir>` from the
`--stdlib-dir` flag, `ESCALIER_STDLIB_DIR`, the install layout, or the repo's
`internal/interop/data`. The directory listing is therefore the authoritative
list of packages. Today it holds 25 `std` files and 24 `web` files. `node` is
reserved and empty.

`internal/dts_to_esc/partition.go` also lists the packages in `Partition`, but it
belongs to the generator. The language server should not depend on it, and it
does not cover hand-written files such as `std/prelude.esc`.

The playground mirrors the same tree. `playground/scripts/copy-files.js` copies
`internal/interop/data` to `public/escalier-builtins` and lists every file in the
manifest's `builtins`. `createVolume` in `playground/src/fs/volume.ts` mounts them
under `/node_modules/escalier-builtins/`, and the client passes that path to the
WASM build through an environment variable. The language server in the
playground can list and read the files the same way the native one does.

### Cost of reading the exports

Enumerating exports does not need a type check. Each package is a single `.esc`
file of `export declare …` declarations, and a parse yields every exported name,
its sort (value, type or both) and its `@js` decorator
(`ast.JSDecoratorName`). A throwaway program that parsed all 49 files with
`parser.ParseLibFiles` took 130 to 150 ms natively and found 2,132 declarations.
The WASM build will be slower, so the index should build once, off the request
path, and be shared by every request.

The counts by scheme are 294 exports in `std` and 1,838 in `web`. `web/dom.esc`
alone has 1,195 of them. No two packages export the same name today, so the
ambiguity below comes from binding names and `@js` heads rather than exports. The `@js` prefixes that name a namespace are
`Math`, `Intl`, `JSON` and `Reflect` in `std`, `Atomics` (in `std:typed_arrays`),
and `CSS` and `WebAssembly` in `web`.

The solver already caches stdlib parses in `stdlibParses`, keyed by basename and
contents. An index built inside `internal/solver` can reuse those parses, so a
package the run later imports is not parsed twice.

## Existing language server pieces

### Completion

`textDocumentCompletion` in `cmd/lsp-server/completion.go` finds the node at the
cursor and dispatches on it. `moduleCompletion` does the same for `lib/` files.

- A bare `*ast.IdentExpr`, or a blank position, calls `completionsFromScope` or
  `completionsFromModuleScope` and filters by prefix. Auto-import items for
  values join here.
- An unqualified `*ast.TypeRefTypeAnn` calls `typeCompletionsFromScope`, and in a
  `lib/` file also `collectFileImportBindings`. Auto-import items for types join
  here.
- `*ast.MemberExpr` and a qualified `TypeRefTypeAnn` call `memberCompletions` and
  `qualifiedTypeCompletions`. #2675 made these split a namespace's members by
  value or type position through `completionsFromNamespace` and
  `namespaceTypeCompletions`. Those functions are what a resolved item reuses for
  its detail.

There is no completion inside an import string today. #2675 covers namespace
members after the dot, not specifiers.

Two parts of the current code need changes for auto-import items to show up.

- `sortAndLimit` sorts by `Label` and truncates to `maxCompletionItems` (100). Up
  to 2,000 candidates would push scope items out of the list. It needs to honour
  `SortText` so in-scope names come first, and cap the auto-import group on its
  own.
- No item sets `TextEdit` or `AdditionalTextEdits`. The server speaks
  `protocol_3_16`, which has both fields but not `labelDetails`. The package name
  therefore goes in `Detail`, as `std:math`.

Detail resolution runs through `completionItemResolve` and the
`completionResolveData` stored in `Data`. An auto-import item can carry the
package URI there, and resolve can print the declaration from the parsed `.esc`
file, so no package load is needed to show a signature.

### Code actions

`textDocumentCodeAction` in `cmd/lsp-server/text_document.go` returns three
kinds of action. It returns the `compile` action, one `borrowFixAction` per
diagnostic for `quickfix`, and the `signatureActionAt` rewrite from
`cmd/lsp-server/signature_action.go` for `refactor.rewrite`. `borrowFixAction`
in `cmd/lsp-server/borrow_fix.go` is the model to follow. `toDiagnostic` attaches `borrowFixData` to the published diagnostic, the
client echoes it back in the code action request, and the action is built from it
with no re-check.

### The diagnostics

Two errors cover an unresolved name.

- `UnknownIdentifierError` in `internal/solver/errors.go` holds the
  `*ast.IdentExpr`. `resolveIdentPath` in `infer_expr.go` and
  `resolveAnnKeyPath` in `type_ann.go` report it. Its message is `Unknown identifier: <name>`. It fires
  for the head of a member chain, so `Math.sqrt(x)` reports `Math`.
- `UnknownTypeError` holds the `*ast.TypeRefTypeAnn` and its name as written,
  qualified when the reference is. Its message is ``cannot find type `<name>` ``.

Both carry the name and the span, which is enough to build the fix. Neither
records the scope the name was looked up in, so the fix cannot tell on its own
whether the binding name, such as `math`, is free in that scope. The handler can
answer that from the file's imports and the module's top-level declarations,
which `topLevelNames` already reads off the AST.

## Where the import goes

The new line goes after the file's last import, or at the top of the file when
there is none. When the file opens with a comment block, it goes after the block.
`Script.Comments` and `Module.Comments` hold the spans for that check.

The edit is skipped when the file already imports the package. The match is on
`ImportStmt.PackageName`, and the qualified name uses that import's `LocalName`,
so `import "std:math" as m` gives `m.sqrt`.

In a `lib/` file the import belongs to that file alone. A sibling file's import
of the same package does not count, since `bindFileImports` gives each file its
own scope. In a `bin/` script the import goes above the first statement, which
the parser requires anyway.

## Playground

`playground/src/language.ts` passes neither feature through to Monaco.

- `provideCompletionItems` in `playground/src/completions.ts` maps `textEdit`,
  `insertText` and `insertTextFormat`, but drops `additionalTextEdits`. It needs
  to convert each edit's range with `lspRangeToMonacoRange` and set Monaco's
  `additionalTextEdits`. `resolveCompletionItem` copies only `detail` back, which
  is fine as long as the server computes the edits up front.
- No code action provider is registered. `LspClient.textDocumentCodeAction`
  exists in `playground/src/lsp-client/client.ts`, so the borrow quick fix is
  also unreachable in the playground today. A provider needs the original LSP
  diagnostics, but `onTextDocumentPublishDiagnostics` turns each one into an
  `IMarkerData` that has no `data` field. The client has to keep the published
  diagnostics per model and match them to `context.markers` by range and message.
  Each returned `WorkspaceEdit` then becomes a Monaco `IWorkspaceTextEdit`.

## Performance

- Build the index once per stdlib directory and content digest, the same key
  `ambientKey` in `internal/solver/stdlib_run_cache.go` uses. Start it in the
  background after `initialize` so the first completion does not wait 150 ms, or
  much longer in WASM. A request that arrives first gets no auto-import items and
  sets `IsIncomplete`.
- Store the names sorted, so a prefix search is a binary search followed by a
  scan.
- Offer auto-import items only once at least one character is typed, as
  TypeScript does. Cap them at about 50 and set `IsIncomplete` so the client asks
  again as the prefix grows.
- Accepting an item changes the module's package closure, and
  `BuildPackageClosure` roots the run at its imports. The next check misses the
  run cache and loads the new package, so the first diagnostics after acceptance
  arrive more slowly. `web:dom` is the costly case.

## Edge cases

- **A name several packages export.** None does today, but the index should
  allow it. Offer one item per package, each with its package in `Detail`, and
  one quick fix per package, ranking `std` above `web`. Binding names do collide
  already. `std:url` and `web:url` both derive the binding `url`, so importing the second
  one while the first is present needs an alias such as `import "web:url" as
  web_url`.
- **A binding that takes the package's name.** When the module declares a value
  `math`, `import "std:math"` binds only the namespace, and `math.sqrt` reads the
  value. The edit must import under an alias and write `<alias>.sqrt`.
- **A local binding of the member's own name.** A local `sqrt` does not conflict,
  since the inserted text is `math.sqrt`. The completion still shows both, with
  the local first by `SortText`.
- **Type position against value position.** Use the node the completion code
  already dispatches on. A type reference offers type exports and namespaces. An
  expression offers values, and a class counts as both. `std:intl` exports
  `Collator` as a class and `CollatorOptions` as a type only, and only the class
  belongs in `val c = Col|`.
- **A dotted `@js` head.** `Math`, `Intl` and the others are not exports. They
  map to the package that holds every `@js` path under that prefix, and the edit
  rewrites the head to the binding, so `Math.sqrt` becomes `math.sqrt`. The
  index has to check that one package owns each prefix, since `Atomics` lives in
  `std:typed_arrays` beside unrelated exports.
- **A name already imported.** When the file imports the package, offer the
  qualified name with no import edit. A name reached through the prelude is
  already in scope and needs nothing.
- **The prelude packages.** Leave `std:prelude` out of the index, and
  `web:prelude` once #2537 lands. Until then, offer `web:core` names as an
  import alone with no qualifier, since its import binds its exports unprefixed.
- **npm packages.** Their exports come from `.d.ts` files under `node_modules`.
  They are out of scope here, and the index's interface should leave room to add
  them.

## Plan

1. **Export index in the solver.** Add `StdlibExportIndex(dir)` in
   `internal/solver`. It lists `std/` and `web/`, parses each file through
   `stdlibParses`, and records each export's name, sort and `@js` head. It caches
   the result by directory and digest. Tests cover a value, a type, a class, an
   own-name value, a dotted head and the exclusions. About 250 lines. The risk is
   low, and the main question is whether the digest matches what
   `stdlib_run_cache.go` hashes.
2. **Import edit helper.** A function in `cmd/lsp-server` that takes a file's
   imports, comments and top-level names and returns the binding to write plus
   the `TextEdit` for the import, or no edit when the import exists. Table-driven
   tests over `lib/` and `bin/` files, aliases and name collisions. About 200
   lines. The risk is low.
3. **Quick fix.** Attach fix data to `UnknownIdentifierError` and
   `UnknownTypeError` in `toDiagnostic`, and add an `importFixAction` beside
   `borrowFixAction` that returns one action per candidate package. About 250
   lines with tests. The risk is moderate.
4. **Completion items.** Add auto-import items at the two dispatch points, make
   `sortAndLimit` honour `SortText` with a separate cap, and print details in
   `completionItemResolve`. About 400 lines with tests. The risk is moderate,
   since the sort change affects every existing completion test.
5. **Playground.** Map `additionalTextEdits` in `completions.ts`, keep the
   published diagnostics per model, and register a code action provider in
   `language.ts`. The borrow quick fix starts working in the playground too.
   About 200 lines with Vitest coverage and a Playwright test that accepts a fix.
   The risk is moderate, mostly from matching markers to diagnostics.

Step 1 has no dependencies. Steps 3 and 4 need steps 1 and 2 and can land in
either order, and step 5 needs whichever of them it exposes. Landing step 1
after #2537 lets its exclusion list name `web:prelude` directly instead of
special-casing `web:core`.
