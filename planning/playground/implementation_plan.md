# Playground Implementation Plan

This plan describes how to implement the requirements in
[requirements.md](requirements.md), broken into incremental phases. Each phase
builds on the previous one and results in a working (if incomplete) playground.

## Phase 1: Virtual Filesystem CRUD and LSP Notifications ✅

**Goal**: Extend `BrowserFS` to support write operations and notify the LSP
server of changes. This is the foundation that all other phases depend on.

**Requirements**: R8.1–R8.7, R9.1, R9.2, R10.6

### 1.1 Extend FSAPI and BrowserFS with write operations ✅

**Files modified**:
- `playground/src/fs/fs-node.ts` — Added `FSSymlink` type with `type`,
  `name`, and `target` fields. `FSNode` is now `FSFile | FSDir | FSSymlink`.
- `playground/src/fs/fs-api.ts` — Added `write`, `writeFile`, `mkdir`,
  `unlink`, `rmdir`, `rename`, and `symlink` to the `FSAPI` interface. The
  `write` method operates on open file descriptors (matching the `node:fs`
  signature) so that `FSAPI` serves as a common interface for both `BrowserFS`
  and `node:fs` (used in tests).
- `playground/src/fs/simple-stats.ts` — Added `_isSymbolicLink` field so
  `isSymbolicLink()` returns the correct value for symlink nodes.
- `playground/src/fs/browser-fs.ts` — Implemented all new methods plus
  `clear()`. Key implementation details:
  - `findNodeInRootDir` accepts a `followLastSymlink` parameter (default
    `true`). `stat` passes `true`, `lstat` passes `false`.
  - Symlink resolution is recursive via `_findNode` with a 40-hop depth limit
    (matching Linux's `ELOOP` limit). Relative targets are resolved against
    the symlink's parent directory using `resolvePath` (handles `.` and `..`).
  - Added `findParent` helper used by all write methods to locate the parent
    directory and base name for a given path (needed because `writeFile` must
    both check for existing nodes and create new ones in the parent).
  - Added `resolveSymlinkPath` helper to resolve a path through symlinks to
    its canonical absolute path (used to update the volume when writing
    through symlinks).
  - All `switch` statements on `FSNode` now handle the `symlink` case.
  - `writeFile` mutates existing `FSFile` nodes in place (preserving open
    file descriptor references), emits `'create'` for new files and
    `'change'` for overwrites. Writing through symlinks resolves to the
    target; dangling symlinks return `ENOENT`.
  - `write` (fd-based) respects `offset` and `position`, grows the content
    buffer as needed, and tracks the write position for sequential writes.
  - `rename` validates the destination before mutating: rejects type
    mismatches (file→dir, dir→file) and non-empty directory targets. Rekeys
    all volume entries under the old path prefix so lazy-loaded children move
    with a renamed directory.
  - `symlink` emits a `'create'` event.
  - `clear()` removes all entries except those under `node_modules/`, cleaning
    both the in-memory tree and the `volume` map.
- `playground/src/fs/browser-fs.test.ts` — Added 44 new tests covering all
  write operations, symlink resolution (absolute, relative, chained, with
  `..`), dangling symlink behavior, writing through symlinks, `stat` vs
  `lstat` on symlinks, `readdir` with symlinks, filesystem events, node
  identity preservation on overwrite, and `clear()`.

### 1.2 Add filesystem change events ✅

**Files created**:
- `playground/src/fs/fs-events.ts` — Defines `FSEvent` type
  (`create | change | delete | rename`) with `path`, `kind` (file/dir), and
  optional `oldPath` (for renames). `FSEventEmitter` class with `on`, `off`,
  `emit`.

**Files modified**:
- `playground/src/fs/browser-fs.ts` — `BrowserFS` exposes a public `events`
  field (instance of `FSEventEmitter`). Events are emitted from `writeFile`
  (`create` for new files, `change` for overwrites), `mkdir` (create),
  `unlink` (delete), `rmdir` (delete), `rename` (rename with `oldPath`), and
  `symlink` (create).

### 1.3 Expose write operations to the WASM/LSP side ✅

**Files modified**:
- `playground/src/lsp-client/client.ts` — The `Client` constructor takes
  `FSAPI` (unchanged from before — both `BrowserFS` and `node:fs` conform to
  this interface, so the test can pass `node:fs` directly). In the
  `globalThis.fs` object:
  - `write` (fd > 2): delegates to `FSAPI.write()`, which handles offset,
    position, and buffer growth properly for multi-chunk writes.
  - `close` (fd > 2): delegates to `FSAPI.close()`.
  - `mkdir`: delegates to `FSAPI.mkdir()` (Go passes a `perm` argument which
    is ignored).
  - `rename`, `rmdir`, `symlink`, `unlink`: delegate to corresponding
    `FSAPI` methods.

### 1.4 LSP notifications on filesystem changes ✅

**Files modified**:
- `playground/src/lsp-client/client.ts` — Added
  `workspaceDidChangeWatchedFiles()` method that sends
  `workspace/didChangeWatchedFiles` notifications via `fireAndForget`.
- `playground/src/main.tsx` — Subscribes to `BrowserFS.events` and forwards
  them to the LSP client via an exhaustive switch on event type. LSP's
  `didChangeWatchedFiles` has no "rename" type — only Created, Changed, and
  Deleted — so rename events are translated into a `Deleted` event for the
  old path plus a `Created` event for the new path. The `change` event type
  maps to `FileChangeType.Changed`.

### 1.5 Replace `go.mod` with `escalier.toml` ✅

**Files created**:
- `escalier.toml` — Added at the repo root so `findRepoRoot()` works
  consistently in both the playground and during Go development/testing.

**Files modified (Go)**:
- `internal/checker/prelude.go` — `findRepoRoot()` now looks for
  `escalier.toml` instead of `go.mod`.
- `cmd/escalier/fixture_test.go` — Updated to symlink `escalier.toml`
  (instead of `go.mod`) into the temp directory so the production
  `findRepoRoot()` resolves back to the repo root where
  `node_modules/typescript/lib/` lives.

**Note**: The 3 copy-pasted `findRepoRoot` functions in test files
(`cmd/escalier/fixture_test.go`, `internal/dts_parser/integration_test.go`,
`internal/interop/module_test.go`) still look for `go.mod` — they're locating
the Go/Git repo root to find test fixtures, not an Escalier project root.

**Files modified (TypeScript)**:
- `playground/src/fs/volume.ts` — Replaced the hardcoded `go.mod` with a
  minimal `escalier.toml` (`[project]\nname = "my-project"`). The basic
  `package.json` is kept so the playground boots with a valid project root.

### 1.6 Define `escalier.toml` format ✅

The minimal schema is:
```toml
[project]
name = "my-project"
```

`findRepoRoot()` only checks for the file's existence via `os.Lstat` — it
does not parse the TOML contents. No TOML parser changes were needed for
Phase 1. Templates and examples (Phase 6) will each include an
`escalier.toml`.

---

## Phase 2: Project Validation

**Goal**: Automatically detect single-package vs multi-package mode and surface
errors for invalid project structures. This is pure logic against the
filesystem with no UI dependencies.

**Requirements**: R7.1–R7.5

### 2.1 Implement validation logic

**Files to create**:
- `playground/src/validation.ts` — Export a `validateProject(fs: BrowserFS)`
  function that inspects the filesystem and returns:
  ```ts
  type ValidationResult =
    | { mode: 'single-package'; packageJson: object }
    | { mode: 'multi-package'; packages: Array<{ name: string; path: string; packageJson: object }> }
    | { mode: 'invalid'; errors: string[] };
  ```
  Rules:
  - If `/packages/` dir exists with subdirs containing `package.json` →
    multi-package (R7.3 precedence).
  - Else if root `/package.json` exists with `lib/` and/or `bin/` containing
    at least one `.esc` file → single-package.
  - Otherwise → invalid with descriptive errors.
- `playground/src/validation.test.ts` — Tests for each validation scenario.

**Note**: The `ValidationResult` type is already defined in
`playground/src/playground-state.ts`, and the validation error banner UI is
already implemented in `playground/src/playground.tsx` (Phase 3). This phase
only needs the validation logic itself and wiring it to FS events.

### 2.2 Wire validation to filesystem events

**Files to modify**:
- `playground/src/main.tsx` (or wherever FS events are subscribed) — Re-run
  validation on every filesystem change event. Store the result so the UI can
  read it once Phase 3 adds the state layer.

---

## Phase 3: State Management and Multi-Tab Editor ✅

**Goal**: Replace the hardcoded single-file playground with a state-driven
multi-tab editor that can open, close, and switch between files.

**Requirements**: R2.1.1–R2.1.5, R2.2.1–R2.2.6, R2.3.1–R2.3.3

### 3.1 Introduce playground state ✅

State was split into two Zustand stores instead of a single monolithic state:

**Files created**:
- `playground/src/playground-state.ts` — Defines `PlaygroundState` with
  `initialCompileDone` and `validationResult` fields. Actions:
  `setInitialCompileDone`, `setValidationResult`. The `ValidationResult` type
  is defined here.
- `playground/src/playground-store.ts` — Zustand store wrapping
  `playgroundReducer` with `usePlaygroundStore()` hook.
- `playground/src/editor-state.ts` — Defines `EditorState` with split-pane
  tab management (`leftTabs`, `rightTabs`, `activeLeftTabIndex`,
  `activeRightTabIndex`, `focusedSide`, `notification`). Actions: `openFile`,
  `closeTab`, `setActiveTab`, `setFocusedSide`, `moveTab`, `renameFile`,
  `deleteFile`, `resetTabs`, `showNotification`, `dismissNotification`.
- `playground/src/editor-store.ts` — Zustand store wrapping `editorReducer`
  with `useEditorStore()` hook.

### 3.2 Build shared UI components ✅

**Files created**:
- `playground/src/components/toast.tsx` — Toast notification component with
  4-second auto-dismiss. Supports `info`, `warning`, and `error` types.
- `playground/src/components/toast.module.css` — Slide-in animation, type-based
  colors (blue/amber/red), dismiss button.
- `playground/src/components/confirm-dialog.tsx` — Modal confirmation dialog
  using the native `<dialog>` element. Supports `destructive` mode (red
  confirm button). Focuses the cancel button on open, handles backdrop clicks
  and Escape key.
- `playground/src/components/confirm-dialog.module.css` — Dark theme dialog
  styling with cancel, confirm, and destructive button variants.
- Storybook stories added for both components.

### 3.3 Refactor Playground component for multi-tab ✅

**Files created/modified**:
- `playground/src/editor.tsx` — New component extracted from the original
  `playground.tsx`. Manages two Monaco editors (input/output) with independent
  tab bars. Features:
  - Model-per-tab via `monaco.editor.getModel()` / `createModel()` keyed by
    file URI (`file:///home/user/project{path}`).
  - `TabItem` sub-component with close buttons and right-click context menus
    (Move to Left/Right, Close).
  - Scroll position preservation across tab switches.
  - Language detection from file extension (`.esc` → Escalier, `.js` →
    JavaScript, `.d.ts` → TypeScript, `.json`/`.js.map` → JSON).
  - `isReadOnly` callback prop for build file read-only mode.
  - `rightPaneVisible` and `rightPaneOverlay` props for controlling the output
    pane.
  - `banner` prop for validation error display.
  - Empty state message when no tabs are open.
- `playground/src/editor.module.css` — Grid layout with named areas:
  ```css
  grid-template-columns: 220px 1fr 1fr;
  grid-template-rows: 0px 40px 1fr;
  grid-template-areas:
    "toolbar    toolbar     toolbar"
    "explorer   input-tabs  output-tabs"
    "explorer   input       output";
  ```
  The `toolbar` row has `height: 0` (expanded in Phase 6).
- `playground/src/playground.tsx` — Wrapper component that:
  - Monitors initial compilation state (compile spinner overlay).
  - Subscribes to `fs.events` to auto-open build output files (`.js`,
    `.js.map`, `.d.ts`) in the right pane when compilation finishes.
  - Displays validation error banner when `validationResult.mode === 'invalid'`.
  - Passes `isReadOnly` callback that returns `true` for `/build/` paths.
- `playground/src/playground.module.css` — Compile spinner animation and error
  banner styling.

### 3.4 Dynamic output tabs based on active input ✅

**Files modified**:
- `playground/src/playground.tsx` — FS event listener detects when compilation
  writes to `build/` and auto-opens the corresponding output files (`.js`,
  `.js.map`, `.d.ts` for `lib/`; `.js`, `.js.map` for `bin/`) in the right
  pane.
- `playground/src/language.ts` — Sends the active file URI to the LSP
  `workspace/executeCommand` for compilation.

### 3.5 Tab lifecycle on file operations ✅

**Files modified**:
- `playground/src/editor-state.ts` — The `deleteFile` action finds and closes
  tabs on both sides, adjusting active indices. The `renameFile` action updates
  tab paths on both sides. The `resetTabs` action closes all tabs and opens
  the primary source file (defaults to `/lib/index.esc`).

### 3.6 Show validation errors ✅

**Files modified**:
- `playground/src/playground.tsx` — If `validationResult.mode === 'invalid'`,
  renders an error banner via the `banner` prop on `<Editor>`. The banner spans
  the full grid width.

---

## Phase 4: File Explorer ✅

**Goal**: Add a file explorer panel that displays the virtual filesystem tree
and supports CRUD operations.

**Requirements**: R1.1–R1.10

### 4.1 Custom tree component (no third-party library) ✅

A custom tree component was built instead of using react-arborist. The existing
`FSDir`/`FSNode` types map directly to the tree structure, making a third-party
library unnecessary overhead.

### 4.2 Build the FileExplorer component ✅

**Files created**:
- `playground/src/components/file-explorer.tsx` — A React component with:
  - Recursive `DirChildren` / `TreeNode` sub-components that render the
    `BrowserFS.rootDir` tree directly (no data transformation needed).
  - Subscribes to `FSEventEmitter` to re-render on filesystem changes.
  - On file click: calls `onFileOpen` callback (wired to editor's `openFile`).
  - Context menu on right-click: "New File", "New Folder" (directories only),
    "Rename", "Delete" (non-protected paths only).
  - `InlineNameInput` sub-component for inline rename/create with smart
    selection (selects name without extension for files), Enter/Escape/blur
    handling.
  - Delete confirmation via `ConfirmDialog` component.
  - `isProtected()` helper hides CRUD controls for `/build/`, `/node_modules/`,
    `/packages/*/build/`, `/packages/*/node_modules/`.
  - `isHidden()` helper hides `.pnpm` directory.
  - Sorting: directories first, then files, alphabetical within each group.
  - Dimmed styling for `build/` and `node_modules/` entries.
  - `build` and `node_modules` directories start collapsed; others start
    expanded.
  - Header with "New File" (+) and "New Folder" action buttons for creating
    items at the project root.
  - `expandOverrides` map tracks explicit expand/collapse overrides; auto-
    expands parent directory when creating a new item inside it.
- `playground/src/components/file-explorer.module.css` — Styles for tree,
  context menu (with separator and destructive item styling), inline input,
  header action buttons (visible on hover), and dimmed entries.
- `playground/src/components/file-explorer.stories.tsx` — Storybook stories:
  `SimpleProject`, `ClickFile`, `CollapseDirectory`, `WithBuildAndNodeModules`,
  `EmptyProject`, `ContextMenuOnFile`, `ContextMenuOnDirectory`,
  `NoContextMenuOnProtectedPaths`, `HeaderNewButtons`.

### 4.3 Integrate into layout ✅

**Files modified**:
- `playground/src/editor.tsx` — Renders `<FileExplorer>` in the explorer grid
  area. Passes `onFileOpen`, `onFileDelete`, and `onFileRename` callbacks that
  dispatch to the editor store (`openFile`, `deleteFile`, `renameFile` actions).
- `playground/src/editor.module.css` — Explorer column set to `220px` in the
  grid layout.

### 4.4 Read-only mode for build files ✅

**Files modified**:
- `playground/src/playground.tsx` — Passes an `isReadOnly` callback to
  `<Editor>` that returns `true` for paths starting with `/build/`.
- `playground/src/editor.tsx` — Applies `readOnly` option to the Monaco editor
  via `editor.updateOptions()` when switching models, based on the `isReadOnly`
  callback.

---

## Phase 5: Single-Package Mode Compilation

**Goal**: Full single-package compilation producing `build/` output in the
virtual filesystem.

**Requirements**: R3.1–R3.4, R10.1, R10.3, R10.5–R10.6

### 5.1 LSP server: ast.Module vs ast.Script distinction

The LSP server needs to distinguish between `lib/` files (compiled as
`ast.Module`) and `bin/` files (compiled as `ast.Script`). This distinction
determines:
- Whether symbols can be exported/imported between files (modules only).
- Whether `.d.ts` output is generated (modules only).

Key difference: all `.esc` files under `lib/` are compiled together as a single
`ast.Module` (they share exports/imports). In contrast, each `.esc` file under
`bin/` is compiled as its own independent `ast.Script` — if `bin/` contains
`main.esc`, `migrate.esc`, and `seed.esc`, those are three separate scripts,
each compiled in isolation.

Scripts have access to **all** symbols in the package's `lib/` module, including
non-exported symbols — the compiler injects the `lib/` namespace into the
script's scope (see `compiler.go:226-228`). However, the generated JS only
imports the symbols the script actually references: `collectUsedLibSymbols`
walks the script's AST, finds identifiers that match `libNS` entries, and
`codegen.NewImportDecl` emits an explicit `import { ... } from "../lib/index.js"`
for just those symbols. Currently the symbol collector does not filter by export
status, so non-exported lib symbols can be imported — this is intentional, as
the export visibility boundary applies between packages, not between a package's
own scripts and its module.

**Files to modify (Go)**:
- The LSP server's compile command handler — use the file path to determine
  whether a file is under `lib/` (module) or `bin/` (script). Pass this
  context to the compiler so it produces the correct AST type and output set.
  For `bin/` files, compile each `.esc` file as a separate `ast.Script`.

### 5.2 LSP server writes compilation output to the filesystem

The `compile` workspace command is updated so the LSP server writes output
files directly to the virtual filesystem via `FSAPI` write operations (set up
in Phase 1.3). This mirrors how a real `escalier` CLI would work and enables
future code sharing between the LSP server and the CLI.

**Files to modify (Go)**:
- The LSP server's `compile` command handler — after compiling, use
  `fs.MkdirAll()` to create the `build/` directory structure, then
  `fs.WriteFile()` to write each output file:
  - For `lib/` sources: write `.js`, `.js.map`, and `.d.ts`.
  - For `bin/` sources: write `.js` and `.js.map` only.
  - Return a success/failure status with diagnostics rather than returning
    file contents.

These writes go through `BrowserFS`, which emits filesystem change events.
Those events automatically update the file explorer and trigger the output
tabs to refresh — no TypeScript-side orchestrator (`compiler.ts`) is needed.

### 5.3 Update compile trigger on the TypeScript side

**Files to modify**:
- `playground/src/language.ts` — Update the compile trigger to:
  - Send a `compile` workspace command to the LSP server (the server handles
    all file writing).
  - Handle the response as a success/failure status rather than receiving file
    contents.
  - On failure, surface diagnostics via the toast component.

### 5.4 Wire output tabs to build files

**Files to modify**:
- `playground/src/playground.tsx` — Output tabs read content from `BrowserFS`
  at the corresponding `build/` path. Subscribe to filesystem change events
  on `build/` paths so the output editor refreshes when the LSP server writes
  new compilation output. When the active `.esc` file changes, resolve the
  output paths and load their content into the output editor models.

---

## Phase 6: Templates, Examples, and Deep Linking

**Goal**: Provide project templates and curated examples with deep linking.
This phase depends on Phases 1 (FS CRUD) and 3 (state management / multi-tab)
but does not require compilation (Phase 5) — templates just populate the
filesystem with source files.

**Requirements**: R5.1.1–R5.1.3, R5.2.1–R5.2.4, R5.3.1–R5.3.4, R5.4.1–R5.4.5

### 6.1 Create template and example files on disk

Templates and examples are real Escalier project files stored in the repo,
not inline TypeScript objects. This makes them easy to author, test, and
version-control.

**Directories to create**:
- `playground/templates/single-package/` — Contains `escalier.toml`,
  `package.json`, `lib/index.esc`, `bin/main.esc` with skeleton starter code.
- `playground/templates/multi-package/` — Contains `escalier.toml`,
  `pnpm-workspace.yaml`, `packages/core/package.json`,
  `packages/core/lib/index.esc`, `packages/app/package.json`,
  `packages/app/lib/index.esc`, `packages/app/bin/main.esc`.
- `playground/examples/hello-world/` — Minimal single-package example.
- `playground/examples/calculator/` — Single-package example demonstrating
  types and multiple lib files.
- Additional example directories as needed (e.g. `shared-utils-app/`,
  `plugin-system/`).

**Files to delete**:
- `playground/src/examples.ts` — Remove the old hardcoded `initialCode`.
  References in `playground/src/playground.tsx` should be removed in Phase 3
  when the single-file setup is replaced.

### 6.2 Update copy-files.js to include templates and examples

**Files to modify**:
- `playground/scripts/copy-files.js` — Extend the build script to:
  1. Recursively copy `playground/templates/*` to `public/templates/`.
  2. Recursively copy `playground/examples/*` to `public/examples/`.
  3. Walk each template/example directory to build file lists.
  4. Extend the manifest from a flat array to a structured object:
     ```json
     {
       "types": ["lib.es5.d.ts", "lib.dom.d.ts", ...],
       "templates": {
         "single-package": ["escalier.toml", "package.json", "lib/index.esc", "bin/main.esc"],
         "multi-package": ["escalier.toml", "packages/core/package.json", ...]
       },
       "examples": {
         "hello-world": ["escalier.toml", "package.json", "lib/index.esc"],
         "calculator": ["escalier.toml", "package.json", "lib/index.esc", "lib/math.esc"]
       }
     }
     ```

**Files to modify**:
- `playground/src/fs/volume.ts` — Update `createVolume` to handle the new
  manifest format (the `types` key) since the manifest is no longer a flat
  array.

### 6.3 Load project function

**Files to create**:
- `playground/src/project-loader.ts` — Export a `loadProject(slug, kind, fs,
  dispatch)` function that:
  1. Fetches the manifest to get the file list for the given template/example.
  2. Fetches each file from `public/templates/<slug>/` or
     `public/examples/<slug>/`.
  3. Calls `fs.clear()` to remove all entries except `node_modules/`.
  4. Populates the filesystem from the fetched files.
  5. Dispatches `resetTabs` to close all tabs and open `lib/index.esc`.
  6. Notifies the LSP server of the new files.

### 6.4 Toolbar and selector UI

**Files to create**:
- `playground/src/toolbar.tsx` — A toolbar component rendered in the `toolbar`
  grid area (set up in Phase 3.3 with `height: 0`). Contains:
  - A "New Project" dropdown listing templates (read from manifest).
  - An "Examples" dropdown listing example projects (read from manifest).
  - Uses `<ConfirmDialog>` (from Phase 3.2) before replacing the current
    project.

**Files to modify**:
- `playground/src/playground.tsx` — Expand the toolbar grid row from
  `height: 0` to its natural height. Render `<Toolbar />`.
- `playground/src/playground.module.css` — Update the toolbar row height.

### 6.5 Deep linking via URL

**Files to modify**:
- `playground/src/main.tsx` — On startup:
  1. Check for `#project=` hash (Phase 7) — skip if not present.
  2. Check for `?example=` query param → load that example.
  3. Otherwise → load "Hello World".
  If the query param references an unknown example, fall back to "Hello World"
  and show a warning via the toast component (Phase 3.2).
- `playground/src/toolbar.tsx` — When an example is selected, update the URL
  query parameter via `history.replaceState`.

---

## Phase 7: Permalinks

**Goal**: Allow users to generate a shareable URL that encodes the full project
state. Depends on Phase 1 (FS) and Phase 6 (project loader for decoding).

**Requirements**: R6.1–R6.7

### 7.1 Encode/decode project data

**Files to create**:
- `playground/src/permalink.ts` — Export `encodeProject(fs: BrowserFS): string`
  and `decodeProject(hash: string): Volume`:
  - Serialize all source files (excluding `build/` and `node_modules/`) as
    JSON: `{ [path]: contentString }`.
  - Compress with `CompressionStream('deflate')` (or pako for sync).
  - Base64url-encode into a hash fragment.
  - Decoding is the reverse.
- `playground/src/permalink.test.ts` — Round-trip tests for encode/decode.

### 7.2 Share button

**Files to modify**:
- `playground/src/toolbar.tsx` — Add a "Share" button that:
  1. Calls `encodeProject()`.
  2. Sets `window.location.hash = 'project=' + encoded`.
  3. Copies the full URL to the clipboard.
  4. Shows a "Link copied!" toast via the toast component (Phase 3.2).

### 7.3 Load from permalink on startup

**Files to modify**:
- `playground/src/main.tsx` — Update the startup sequence (from Phase 6.5) to
  check for `#project=` first. If present, decode and load the project via
  `loadProject()`. If malformed, fall back to "Hello World" and show an error
  toast (Phase 3.2).

---

## Phase 8: Multi-Package Mode

**Goal**: Support monorepo projects with inter-package dependencies. Depends on
Phase 5 (single-package compilation) as the foundation.

**Requirements**: R4.1–R4.5, R10.2, R10.4.1–R10.4.3

### 8.1 Dependency DAG resolution

The LSP server reads `pnpm-workspace.yaml` to discover package locations, then
reads each package's `package.json` to build the dependency graph.

**Files to create**:
- `playground/src/dependency-graph.ts` — Export a function that:
  - Reads `pnpm-workspace.yaml` to determine which directories contain
    packages.
  - Reads each discovered package's `package.json` for its `dependencies`.
  - Builds a dependency graph from the `dependencies` fields.
  - Returns a topological sort of package names.
  - Throws a descriptive error if a cycle is detected.
  - Throws a descriptive error if a dependency listed in a package's
    `package.json` cannot be found among the workspace packages.
- `playground/src/dependency-graph.test.ts` — Tests for topological sort,
  cycle detection, missing dependencies.

### 8.2 Multi-package compilation

**Files to modify (Go)**:
- The LSP server's `compile` command handler — when the project is in
  multi-package mode:
  - Resolve the dependency DAG (using logic from 8.1, implemented in Go).
  - Compile packages in topological order, writing each package's output to
    `packages/<name>/build/` (not the project root `build/`) via `FSAPI`
    write operations.
  - For each package, `lib/` sources produce `.js`, `.js.map`, `.d.ts`;
    `bin/` sources produce `.js`, `.js.map` only.
  - Return cycle detection errors as part of the compile response so the
    TypeScript side can surface them via the toast component.

**Note**: The dependency DAG resolution (8.1) should also be implemented on
the Go side so the LSP server can use it directly. The TypeScript-side
`dependency-graph.ts` from 8.1 is useful for the validation UI (Phase 2) to
detect and display cycle errors before compilation, but the authoritative
build-order logic lives in Go.

### 8.3 Cross-package import resolution

This requires setting up pnpm-style symlink structure in the virtual filesystem
so the LSP server's standard Node module resolution finds cross-package imports.
`BrowserFS` symlink support (added in Phase 1.1) makes this straightforward.

pnpm uses an isolated `node_modules` layout:
- Each package gets a `node_modules/` with symlinks to its declared
  dependencies only.
- Packages are stored in a top-level `.pnpm/` virtual store, and each
  package's `node_modules/<dep>` symlinks into that store.

For the playground's virtual filesystem, we replicate this using real symlinks
in `BrowserFS`. For example, if `app` depends on `core`:
```
packages/app/node_modules/
  core -> ../../../.pnpm/core/node_modules/core
.pnpm/core/node_modules/
  core -> ../../../packages/core
```

This means a bare import like `"core"` in `app` resolves through
`packages/app/node_modules/core/` which ultimately points to
`packages/core/`, where the `package.json` `main` and `types` fields direct
to the compiled output in `build/`.

**Files to create (TypeScript)**:
- `playground/src/linker.ts` — Exports a `link(fs: BrowserFS)` function that
  reads each package's `dependencies` and creates the pnpm-style symlink
  structure using `BrowserFS.symlink()`. Also exports a `setupLinkListener(fs)`
  function that subscribes to `BrowserFS` filesystem change events (Phase 1.2)
  and re-runs the linker when dependency-defining files change:
  - Filter events for paths matching `**/package.json` or
    `pnpm-workspace.yaml`.
  - Debounce re-linking (e.g. 300ms) since the user may be mid-edit.
  - After the debounce fires, attempt to parse the changed `package.json`
    as JSON. If parsing fails (user mid-edit), silently skip re-linking —
    do not show a toast for transient syntax errors.
  - Before re-linking, remove the existing `.pnpm/` directory and all
    `node_modules/` directories within `packages/`, then recreate them from
    the updated dependency graph.
  - If the new dependency graph has semantic errors (cycles, missing deps),
    skip re-linking and surface the error via the toast component.

**Files to modify (Go)**:
- The LSP server's module resolution logic should work with standard Node
  resolution (walking up `node_modules/` directories). Since `BrowserFS.stat()`
  follows symlinks transparently, the resolver needs no special-casing — it
  sees regular files and directories through the symlink chain.

**Tests to add**:
- `playground/src/linker.test.ts` (or in `browser-fs.test.ts`) — Tests that
  exercise Node module resolution across the pnpm-style symlink layout:
  - Verify that `stat('packages/app/node_modules/core/package.json')` resolves
    through the symlink chain to `packages/core/package.json`.
  - Verify that `lstat('packages/app/node_modules/core')` returns a symlink
    (does not follow).
  - Verify that after re-running the linker with updated dependencies, the
    symlink structure reflects the new dependency graph.
  - Verify that if a target package is deleted (dangling symlink), `stat`
    through the symlink returns `ENOENT` while `lstat` still shows a symlink.

---

## Phase Summary

| Phase | Description                          | Requirements Covered                   | Status |
|-------|--------------------------------------|----------------------------------------|--------|
| 1     | Virtual FS CRUD + LSP + escalier.toml| R8.1–R8.7, R9.1, R9.2, R10.6          | ✅     |
| 2     | Project validation                   | R7.1–R7.5                              |        |
| 3     | Multi-tab editor + state management  | R2.1.x, R2.2.x, R2.3.x               | ✅     |
| 4     | File explorer                        | R1.1–R1.10                             | ✅     |
| 5     | Single-package compilation           | R3.1–R3.4, R10.1, R10.3, R10.5–R10.6  |        |
| 6     | Templates and examples               | R5.1.x, R5.2.x, R5.3.x, R5.4.x       |        |
| 7     | Permalinks                           | R6.1–R6.7                              |        |
| 8     | Multi-package mode                   | R4.1–R4.5, R10.2, R10.4.x             |        |

## Dependencies Between Phases

```
Phase 1 (FS CRUD)
  ├── Phase 2 (Validation)
  └── Phase 3 (Multi-tab editor + state + UI components)
        ├── Phase 4 (File explorer)
        ├── Phase 5 (Single-package compilation)
        │     └── Phase 8 (Multi-package mode)
        └── Phase 6 (Templates & examples)
              └── Phase 7 (Permalinks)
```

Key observations:
- **Phase 2** (validation) has no UI dependencies — it only needs Phase 1 (FS).
- **Phase 5** depends on Phase 3 because it uses the toast component (3.2) for
  surfacing compile errors and the output tab infrastructure (3.4).
- **Phase 6** (templates) only needs Phases 1 + 3 — it populates the
  filesystem with source files and doesn't require compilation to work.
- **Phase 7** (permalinks) only needs Phases 1 + 6 — it encodes/decodes the
  filesystem using the project loader.
- **Phases 4, 5, 6** can proceed in parallel after Phase 3.
- **Phase 8** requires Phase 5 (compilation must work for single packages
  before extending to multi-package).
