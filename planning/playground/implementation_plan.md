# Playground Implementation Plan

This plan describes how to implement the requirements in
[requirements.md](requirements.md), broken into incremental phases. Each phase
builds on the previous one and results in a working (if incomplete) playground.

## Phase 1: Virtual Filesystem CRUD and LSP Notifications

**Goal**: Extend `BrowserFS` to support write operations and notify the LSP
server of changes. This is the foundation that all other phases depend on.

**Requirements**: R8.1, R8.2

### 1.1 Extend FSAPI and BrowserFS with write operations

**Files to modify**:
- `playground/src/fs/fs-api.ts` — Add `writeFile`, `mkdir`, `unlink`, `rmdir`,
  `rename` to the `FSAPI` interface.
- `playground/src/fs/browser-fs.ts` — Implement the new methods, mutating both
  the in-memory `rootDir` tree and the `volume` map. Also add a `clear()`
  method that removes all entries except those under `node_modules/` (needed
  later for project loading).
- `playground/src/fs/browser-fs.test.ts` — Add tests for each new operation.

### 1.2 Add filesystem change events

**Files to create**:
- `playground/src/fs/fs-events.ts` — Define a `FSEvent` type
  (`create | delete | rename`) with path and kind (file/dir), and an
  `FSEventEmitter` that `BrowserFS` emits on every mutation.

**Files to modify**:
- `playground/src/fs/browser-fs.ts` — Emit events from `writeFile`, `mkdir`,
  `unlink`, `rmdir`, `rename`.

### 1.3 LSP notifications on filesystem changes

**Files to modify**:
- `playground/src/lsp-client/client.ts` — Add methods for
  `workspace/didChangeWatchedFiles` and `textDocument/didOpen` /
  `textDocument/didClose` notifications.
- `playground/src/main.tsx` — Subscribe to `BrowserFS` events and forward them
  to the LSP client as appropriate notifications.

### 1.4 Refactor volume initialization

**Files to modify**:
- `playground/src/fs/volume.ts` — Refactor `createVolume` to only handle
  TypeScript `.d.ts` file setup (the `node_modules/` entries). Remove the
  hardcoded `package.json` and `go.mod` — project files will be populated by
  the project loader (Phase 6). Address the existing `go.mod` TODO by finding
  an alternative approach for repo root detection.

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

### 2.2 Wire validation to filesystem events

**Files to modify**:
- `playground/src/main.tsx` (or wherever FS events are subscribed) — Re-run
  validation on every filesystem change event. Store the result so the UI can
  read it once Phase 3 adds the state layer.

---

## Phase 3: State Management and Multi-Tab Editor

**Goal**: Replace the hardcoded single-file playground with a state-driven
multi-tab editor that can open, close, and switch between files.

**Requirements**: R2.1.1–R2.1.5, R2.2.1–R2.2.6, R2.3.1–R2.3.3

### 3.1 Introduce playground state

**Files to create**:
- `playground/src/state.ts` — Define the playground state:
  ```ts
  type PlaygroundState = {
    openTabs: Array<{ path: string; scrollPos?: number }>;
    activeTabIndex: number | null;
    activeOutputTab: 'js' | 'map' | 'dts';
    validationResult: ValidationResult;
    notification: { message: string; type: 'info' | 'warning' | 'error' } | null;
  };
  ```
  Export a React context and reducer (or zustand store) for state management.
  Include actions: `openFile`, `closeTab`, `setActiveTab`,
  `setActiveOutputTab`, `renameFile`, `deleteFile`, `resetTabs`,
  `setValidationResult`, `showNotification`, `dismissNotification`.

### 3.2 Build shared UI components

**Files to create**:
- `playground/src/components/toast.tsx` — A toast/notification component for
  transient messages (warnings, errors, confirmations). Used by:
  - R5.3.3: unknown example fallback warning
  - R6.6: malformed permalink error
  - R6.7: "Link copied!" confirmation
  - R7.4: invalid project structure errors
- `playground/src/components/toast.module.css` — Toast styling.
- `playground/src/components/confirm-dialog.tsx` — A confirmation dialog
  component for destructive actions. Used by:
  - R5.1.3: replacing project with template
  - R5.2.3: replacing project with example
- `playground/src/components/confirm-dialog.module.css` — Dialog styling.

### 3.3 Refactor Playground component for multi-tab

**Files to modify**:
- `playground/src/playground.tsx` — Replace the single hardcoded `inputModel`
  with a model-per-tab approach. Use `monaco.editor.getModel()` /
  `createModel()` keyed by file URI. Switch the editor's model when the active
  tab changes. Create a `TabBar` sub-component with close buttons.
- `playground/src/playground.module.css` — Add styles for close button, tab
  overflow scrolling. Design the grid layout to be forward-compatible with the
  toolbar (Phase 6) and file explorer (Phase 4) by using named grid areas:
  ```css
  grid-template-areas:
    "toolbar  toolbar  toolbar"
    "explorer input-tabs output-tabs"
    "explorer input    output";
  ```
  Initially the `toolbar` row has `height: 0` and `explorer` column has
  `width: 0` — they expand when those features are added.

### 3.4 Dynamic output tabs based on active input

**Files to modify**:
- `playground/src/playground.tsx` — When the active tab changes:
  - If it's a `.esc` file under `lib/`, show `.js`, `.js.map`, `.d.ts` output
    tabs.
  - If it's a `.esc` file under `bin/`, show `.js`, `.js.map` only.
  - If it's not a `.esc` file or no tabs are open, hide the right editor
    entirely.
- `playground/src/language.ts` — Update the compile result handler to write
  output for the correct file (not just hardcoded `foo.esc`). Store compiled
  output in a map keyed by source path.

### 3.5 Tab lifecycle on file operations

**Files to modify**:
- `playground/src/state.ts` — The `deleteFile` action closes the tab and
  activates the adjacent one. The `renameFile` action updates the tab's path.
  The `resetTabs` action (for template/example loading) closes all tabs and
  opens the primary source file.

### 3.6 Show validation errors

**Files to modify**:
- `playground/src/playground.tsx` — If `validationResult.mode === 'invalid'`,
  show an error banner above the editors. Wire validation result from Phase 2
  into the state.

---

## Phase 4: File Explorer

**Goal**: Add a file explorer panel that displays the virtual filesystem tree
and supports CRUD operations.

**Requirements**: R1.1–R1.10

### 4.1 Evaluate and install tree component

Evaluate the candidates listed in R1.10. Recommendation: start with
**react-arborist** since it has built-in CRUD, virtualization, and custom node
rendering.

**Commands**:
- `pnpm add react-arborist` (in `playground/`)

### 4.2 Build the FileExplorer component

**Files to create**:
- `playground/src/file-explorer.tsx` — A React component that:
  - Reads the `BrowserFS` directory tree and converts it to the tree data
    format expected by react-arborist.
  - Subscribes to `FSEvent`s to re-render when files change.
  - On file click: dispatches `openFile` to the playground state.
  - On create/delete/rename: calls the corresponding `BrowserFS` method (which
    triggers events and LSP notifications).
  - Custom node renderer that dims `build/` entries and hides CRUD controls for
    them.
- `playground/src/file-explorer.module.css` — Styles for the tree, icons,
  dimmed build entries.

### 4.3 Integrate into layout

**Files to modify**:
- `playground/src/playground.tsx` — Expand the explorer grid column from
  `width: 0` to `220px`. Render `<FileExplorer />` in the explorer grid area.
- `playground/src/playground.module.css` — Update the explorer column width.

### 4.4 Read-only mode for build files

**Files to modify**:
- `playground/src/playground.tsx` — When opening a file from `build/`, set the
  Monaco editor to `readOnly: true` for that model. When switching to a
  non-build file, ensure `readOnly: false`.

---

## Phase 5: Single-Package Mode Compilation

**Goal**: Full single-package compilation producing `build/` output in the
virtual filesystem.

**Requirements**: R3.1–R3.4, R9.1, R9.3, R9.5.1, R9.5.2

### 5.1 LSP server: ast.Module vs ast.Script distinction

The LSP server needs to distinguish between `lib/` files (compiled as
`ast.Module`) and `bin/` files (compiled as `ast.Script`). This distinction
determines:
- Whether symbols can be exported/imported between files (modules only).
- Whether `.d.ts` output is generated (modules only).

**Files to modify (Go)**:
- The LSP server's compile command handler — use the file path to determine
  whether a file is under `lib/` (module) or `bin/` (script). Pass this
  context to the compiler so it produces the correct AST type and output set.

### 5.2 Update compile command to handle multiple files

**Files to modify**:
- `playground/src/language.ts` — Update the compile trigger to send all `.esc`
  source file paths (not just the active file) to the LSP `workspace/
  executeCommand`. Handle the response which now returns output for multiple
  files.

### 5.3 Write compilation output to BrowserFS

**Files to create/modify**:
- `playground/src/compiler.ts` — Create a compilation orchestrator that:
  - After receiving compile results, writes each output file to the virtual
    filesystem under `build/`, mirroring the source structure.
  - For `lib/` sources: writes `.js`, `.js.map`, and `.d.ts` files.
  - For `bin/` sources: writes `.js` and `.js.map` files only.
  - This triggers filesystem events which update the file explorer.

### 5.4 Wire output tabs to build files

**Files to modify**:
- `playground/src/playground.tsx` — Output tabs now read content from
  `BrowserFS` at the corresponding `build/` path rather than from inline
  compile results. When the active `.esc` file changes, resolve the output
  paths and load their content into the output editor models.

---

## Phase 6: Templates, Examples, and Deep Linking

**Goal**: Provide project templates and curated examples with deep linking.
This phase depends on Phases 1 (FS CRUD) and 3 (state management / multi-tab)
but does not require compilation (Phase 5) — templates just populate the
filesystem with source files.

**Requirements**: R5.1.1–R5.1.3, R5.2.1–R5.2.4, R5.3.1–R5.3.4

### 6.1 Define template and example data

**Files to create**:
- `playground/src/templates/single-package.ts` — Export a `Volume` object with
  the single-package template files (`package.json`, `lib/index.esc`,
  `bin/main.esc`).
- `playground/src/templates/multi-package.ts` — Export a `Volume` object with
  the multi-package template files.
- `playground/src/examples/index.ts` — Export a registry mapping example slugs
  to `Volume` objects:
  ```ts
  export const examples: Record<string, { name: string; volume: Volume }> = {
    'hello-world': { name: 'Hello World', volume: ... },
    'calculator': { name: 'Calculator', volume: ... },
    ...
  };
  ```

**Files to delete**:
- `playground/src/examples.ts` — Remove the old hardcoded `initialCode`.
  References in `playground/src/playground.tsx` should be removed in Phase 3
  when the single-file setup is replaced.

### 6.2 Load project function

**Files to create**:
- `playground/src/project-loader.ts` — Export a `loadProject(volume, fs,
  dispatch)` function that:
  1. Calls `fs.clear()` to remove all entries except `node_modules/`.
  2. Populates the filesystem from the provided `Volume`.
  3. Dispatches `resetTabs` to close all tabs and open `lib/index.esc`.
  4. Notifies the LSP server of the new files.

### 6.3 Toolbar and selector UI

**Files to create**:
- `playground/src/toolbar.tsx` — A toolbar component rendered in the `toolbar`
  grid area (set up in Phase 3.3 with `height: 0`). Contains:
  - A "New Project" dropdown listing templates.
  - An "Examples" dropdown listing example projects.
  - Uses `<ConfirmDialog>` (from Phase 3.2) before replacing the current
    project.

**Files to modify**:
- `playground/src/playground.tsx` — Expand the toolbar grid row from
  `height: 0` to its natural height. Render `<Toolbar />`.
- `playground/src/playground.module.css` — Update the toolbar row height.

### 6.4 Deep linking via URL

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
- `playground/src/main.tsx` — Update the startup sequence (from Phase 6.4) to
  check for `#project=` first. If present, decode and load the project via
  `loadProject()`. If malformed, fall back to "Hello World" and show an error
  toast (Phase 3.2).

---

## Phase 8: Multi-Package Mode

**Goal**: Support monorepo projects with inter-package dependencies. Depends on
Phase 5 (single-package compilation) as the foundation.

**Requirements**: R4.1–R4.4, R9.2, R9.4.1–R9.4.3

### 8.1 Dependency DAG resolution

**Files to create**:
- `playground/src/dependency-graph.ts` — Export a function that:
  - Reads all `package.json` files from the virtual filesystem.
  - Builds a dependency graph from their `dependencies` fields.
  - Returns a topological sort of package names.
  - Throws a descriptive error if a cycle is detected.
- `playground/src/dependency-graph.test.ts` — Tests for topological sort,
  cycle detection, missing dependencies.

### 8.2 Multi-package compilation

**Files to modify**:
- `playground/src/compiler.ts` — When validation detects multi-package mode:
  - Resolve the dependency DAG.
  - Compile packages in topological order, writing each package's output to
    `packages/<name>/build/` (not the project root `build/`).
  - For each package, `lib/` sources produce `.js`, `.js.map`, `.d.ts`;
    `bin/` sources produce `.js`, `.js.map` only.
  - Surface cycle detection errors via the toast component.

### 8.3 Cross-package import resolution

This requires LSP server changes (Go side) to resolve bare specifiers
against the `packages/` directory structure using `package.json` `main`/`types`
fields.

**Files to modify (Go)**:
- The LSP server's module resolution logic — when encountering a bare import
  like `"core"`, look up the corresponding package's `package.json` to find
  its `main` and `types` paths.

**Files to modify (TypeScript)**:
- `playground/src/fs/volume.ts` — The `createVolume` function needs to be
  updated to set up the appropriate `node_modules` symlink-like structure or
  path mappings so the LSP can resolve cross-package imports.

---

## Phase Summary

| Phase | Description                          | Requirements Covered                  |
|-------|--------------------------------------|---------------------------------------|
| 1     | Virtual FS CRUD + LSP notifications  | R8.1, R8.2                            |
| 2     | Project validation                   | R7.1–R7.5                             |
| 3     | Multi-tab editor + state management  | R2.1.x, R2.2.x, R2.3.x              |
| 4     | File explorer                        | R1.1–R1.10                            |
| 5     | Single-package compilation           | R3.1–R3.4, R9.1, R9.3, R9.5.x       |
| 6     | Templates and examples               | R5.1.x, R5.2.x, R5.3.x              |
| 7     | Permalinks                           | R6.1–R6.7                             |
| 8     | Multi-package mode                   | R4.1–R4.4, R9.2, R9.4.x             |

## Dependencies Between Phases

```
Phase 1 (FS CRUD)
  ├── Phase 2 (Validation) ─────────────────────────┐
  ├── Phase 3 (Multi-tab editor + state + UI components)
  │     ├── Phase 4 (File explorer)                  │
  │     └── Phase 6 (Templates & examples) ──────────┤
  │           └── Phase 7 (Permalinks)               │
  └── Phase 5 (Single-package compilation) ──────────┘
        └── Phase 8 (Multi-package mode)
```

Key observations:
- **Phase 2** (validation) has no UI dependencies — it only needs Phase 1 (FS).
- **Phase 6** (templates) only needs Phases 1 + 3 — it populates the
  filesystem with source files and doesn't require compilation to work.
- **Phase 7** (permalinks) only needs Phases 1 + 6 — it encodes/decodes the
  filesystem using the project loader.
- **Phases 4, 5, 6** can proceed in parallel after Phase 3.
- **Phase 8** requires Phase 5 (compilation must work for single packages
  before extending to multi-package).
