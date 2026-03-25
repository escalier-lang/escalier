# Playground Requirements

## Current State

The playground is a browser-based IDE using Monaco Editor with a Go-based LSP server
compiled to WebAssembly. It currently has:

- A single hardcoded input file (`foo.esc`) in the left editor
- Two output tabs (`foo.js`, `foo.js.map`) in the right editor
- A virtual filesystem (`BrowserFS`) preloaded with TypeScript `.d.ts` files
- LSP features: diagnostics, completions, hover, go-to-definition
- A `compile` workspace command that returns JS and sourcemap output

## Requirements

### 1. File Explorer

- **R1.1** A file explorer panel on the left side of the playground that
  displays the project's file tree.
- **R1.2** The file tree should show directories and files with appropriate
  expand/collapse behavior for directories.
- **R1.3** Clicking a file in the explorer opens it in the left (input) editor.
- **R1.4** The explorer should reflect the virtual filesystem state (files in
  `lib/`, `bin/`, `package.json`, etc.).
- **R1.5** Users can create new files and directories via the explorer.
- **R1.6** Users can delete files and directories via the explorer.
- **R1.7** Users can rename files and directories via the explorer.
- **R1.8** The `build/` directory (and its contents) should be visible in the
  file explorer but visually distinguished as generated output (e.g. dimmed
  text). Files under `build/` are read-only — create/delete/rename operations
  should not be available for them.
- **R1.9** Opening a file from `build/` in the left editor opens it in
  read-only mode.
- **R1.10** The file explorer should use a third-party React tree component
  rather than a custom implementation. Candidates to evaluate:
  - **react-arborist** (~3.5k GitHub stars) — Built-in CRUD (create, rename,
    delete, move), virtualized rendering, fully customizable node renderer,
    drag-and-drop. Best out-of-the-box fit for an IDE-style sidebar.
  - **@headless-tree/react** (~800 GitHub stars) — Fully headless (logic only,
    you own all JSX/CSS), tiny bundle (~10 kB), built-in rename, but
    create/delete are DIY. Best if maximum styling control is needed.
  - **react-complex-tree** (~1.3k GitHub stars) — Unopinionated styling, W3C
    keyboard accessibility, drag-and-drop. Being superseded by headless-tree
    (same author). Create/delete not built-in.

### 2. Editor Tabs

#### 2.1. Input (Left) Editor Tabs
- **R2.1.1** Opening a file from the explorer creates a new tab in the left
  editor.
- **R2.1.2** Multiple files can be open simultaneously as tabs.
- **R2.1.3** Clicking a tab switches the left editor to display that file.
- **R2.1.4** Each tab should have a close button (x) to close it.
- **R2.1.5** Tab state (which tabs are open, which is active) persists while
  navigating the explorer.

#### 2.2. Output (Right) Editor Tabs
- **R2.2.1** When a `.esc` file is active in the left editor, the right editor
  shows the corresponding compiled output tabs.
- **R2.2.2** For `lib/` files (module sources): show `.js`, `.js.map`, and
  `.d.ts` tabs.
- **R2.2.3** For `bin/` files (scripts): show `.js` and `.js.map` tabs only
  (scripts don't export types, so no `.d.ts`).
- **R2.2.4** Switching the active input file updates which output files are
  shown on the right.
- **R2.2.5** If the active input file is not a `.esc` file (e.g.
  `package.json`), the right editor and its tabs should be hidden.
- **R2.2.6** If no files are open in the left editor, the right editor and its
  tabs should be hidden entirely.

#### 2.3. Tab Lifecycle on File Operations
- **R2.3.1** **Delete**: If a file is deleted, its corresponding input tab is
  closed. If it was the active tab, the next tab becomes active (or the
  previous tab if it was the last one).
- **R2.3.2** **Rename**: If a file is renamed, its corresponding input tab
  updates to reflect the new name and path.
- **R2.3.3** **Load template/example**: All open input tabs are closed before
  the new project is loaded. The project's primary source file (e.g.
  `lib/index.esc`) is automatically opened.

### 3. Single-Package Mode

Simulates a single npm package project. The virtual filesystem layout:

```
/
├── package.json
├── lib/
│   ├── index.esc        # part of the module
│   └── utils/
│       └── helpers.esc  # also part of the module
└── bin/
    └── main.esc         # a standalone script
```

- **R3.1** `package.json` defines the package name, version, and entry points.
- **R3.2** All `.esc` files under `lib/` (recursively) constitute a single
  `ast.Module`. Symbols exported from one file in `lib/` are importable by
  other files in `lib/`.
- **R3.3** Each `.esc` file under `bin/` is its own `ast.Script`. Scripts can
  import from the package's own `lib/` module.
- **R3.4** Compilation output goes to a `build/` directory mirroring the source
  structure:
  ```
  build/
  ├── lib/
  │   ├── index.js
  │   ├── index.js.map
  │   ├── index.d.ts
  │   └── utils/
  │       ├── helpers.js
  │       ├── helpers.js.map
  │       └── helpers.d.ts
  └── bin/
      ├── main.js
      └── main.js.map
  ```

### 4. Multi-Package Mode (Monorepo)

Simulates a monorepo with multiple packages that can depend on each other. The
virtual filesystem layout (source files only — `build/` is generated output):

```
/
├── packages/
│   ├── core/
│   │   ├── package.json
│   │   └── lib/
│   │       └── index.esc
│   └── app/
│       ├── package.json
│       ├── lib/
│       │   └── index.esc
│       └── bin/
│           └── main.esc
```

- **R4.1** Each package has its own `package.json` specifying:
  - `"name"`: the package name used for imports (e.g. `"core"`)
  - `"main"`: path to the compiled JS entry point (e.g. `"build/lib/index.js"`)
  - `"types"`: path to the type declarations (e.g. `"build/lib/index.d.ts"`)
  - `"dependencies"`: a map of package names this package depends on (e.g.
    `{ "core": "*" }`)
- **R4.2** Compilation output goes to a `build/` directory within each package,
  mirroring the source structure:
  ```
  packages/core/build/
  ├── lib/
  │   ├── index.js
  │   ├── index.js.map
  │   └── index.d.ts
  ```
- **R4.3** Code in one package (e.g. `app`) can import exported symbols from
  another package (e.g. `core`) using the package name. The implementation is
  resolved from the dependency's `build/lib/index.js` and types from
  `build/lib/index.d.ts` as specified in that package's `package.json`.
- **R4.4** Cross-package imports use bare specifiers matching the dependency's
  package name (e.g. `import { foo } from "core"`).

### 5. Templates and Examples

The playground provides a way to start new projects from templates and to load
example projects. These are accessible from a toolbar or welcome screen.

#### 5.1. Project Templates
- **R5.1.1** **Single-package template**: Scaffolds a minimal single-package
  project with a `package.json`, a `lib/index.esc`, and a `bin/main.esc`
  containing skeleton starter code.
- **R5.1.2** **Multi-package template**: Scaffolds a minimal monorepo with two
  packages (e.g. `core` and `app`) where `app` depends on `core`, each with a
  `package.json` and a `lib/index.esc`.
- **R5.1.3** Creating a new project from a template replaces the current
  virtual filesystem contents. The user should be prompted with a confirmation
  dialog before replacing their current work.

#### 5.2. Example Projects
- **R5.2.1** A dropdown or menu allows users to select from a curated set of
  example projects.
- **R5.2.2** Examples should include both single-package and multi-package
  projects:
  - **Single-package examples**: e.g. "Hello World", "Calculator", "Todo List"
  - **Multi-package examples**: e.g. "Shared Utils + App", "Plugin System"
- **R5.2.3** Loading an example replaces the current virtual filesystem
  contents with the example's files. The user should be prompted with a
  confirmation dialog before replacing their current work.
- **R5.2.4** Examples should demonstrate key language features and project
  patterns (imports/exports, types, bin scripts, cross-package deps, etc.).

#### 5.3. Initial State and Deep Linking
- **R5.3.1** On first load (no URL parameters), the playground loads the
  "Hello World" example.
- **R5.3.2** A URL query parameter (e.g. `?example=calculator`) allows
  specifying which example to load on page load. This enables sharing links to
  specific examples.
- **R5.3.3** If the query parameter references an unknown example, the
  playground falls back to the "Hello World" example and shows a brief warning.
- **R5.3.4** Selecting an example from the menu updates the URL query parameter
  so the current example is reflected in the URL (and can be
  bookmarked/shared).

### 6. Permalinks

Users can generate a shareable permalink that captures the full state of their
playground project.

- **R6.1** A "Share" button generates a permalink URL for the current project.
- **R6.2** The permalink encodes the entire virtual filesystem (all source
  files and their contents) into the URL. The `build/` directory is excluded
  since it is regenerated on load.
- **R6.3** The project data should be compressed (e.g. using deflate/gzip) and
  base64url-encoded into a URL hash fragment (e.g.
  `#project=<encoded-data>`). Using a hash fragment avoids server-side storage
  and keeps permalinks self-contained.
- **R6.4** When the playground loads with a permalink hash, it decodes and
  decompresses the project data and populates the virtual filesystem
  accordingly.
- **R6.5** If a permalink hash is present, it takes precedence over the
  `?example=` query parameter.
- **R6.6** If the permalink data is malformed or cannot be decoded, the
  playground falls back to the "Hello World" example and shows a brief error
  message.
- **R6.7** After generating a permalink, the URL should be copied to the
  clipboard (or presented in a dialog for easy copying).

### 7. Project Validation

There is no mode toggle. Instead, the playground automatically detects which
mode the project is in based on the virtual filesystem contents. Validation
runs whenever the filesystem changes (file create/delete/rename).

- **R7.1** **Single-package**: A root `package.json` exists with `lib/` and/or
  `bin/` directories containing at least one `.esc` file.
- **R7.2** **Multi-package**: A `packages/` directory exists containing
  subdirectories that each have their own `package.json` with `lib/` and/or
  `bin/` directories.
- **R7.3** **Precedence**: If a `packages/` directory exists, the project is
  treated as multi-package. A root `package.json` alongside `packages/` is
  allowed (as in real monorepos) but does not trigger single-package mode.
- **R7.4** If the filesystem doesn't match either pattern, the playground
  surfaces an error message to the user explaining what's wrong (e.g. "Missing
  package.json", "No lib/ or bin/ directory found").
- **R7.5** Validation re-runs as the user creates/deletes/renames files, so a
  project can naturally transition between modes (e.g. adding a `packages/`
  directory switches to multi-package mode).

### 8. Virtual Filesystem Updates

- **R8.1** The `BrowserFS` / volume system must support creating, deleting, and
  renaming files and directories.
- **R8.2** The LSP server must be notified of filesystem changes so diagnostics
  and completions stay current.

### 9. Compilation

- **R9.1** Compilation should be triggered on file save or after a debounce on
  edit (as it works today).
- **R9.2** The LSP server should be aware of all files in the project, even in
  multi-package mode.
- **R9.3** In single-package mode, compiling produces output for all source
  files.
- **R9.4** In multi-package mode:
  - **R9.4.1** Inter-package dependencies must be resolved into a DAG (directed
    acyclic graph) based on the `dependencies` declared in each package's
    `package.json`.
  - **R9.4.2** Packages are type-checked and compiled in topological order so
    that a package's dependencies are built before it is.
  - **R9.4.3** If cyclic dependencies are detected between packages, this is a
    **fatal error** and should be surfaced to the user immediately.
- **R9.5** Output files are written to the virtual filesystem under `build/`
  and viewable in the right editor:
  - **R9.5.1** `lib/` sources produce `.js`, `.js.map`, and `.d.ts` files.
  - **R9.5.2** `bin/` sources produce `.js` and `.js.map` files only.
