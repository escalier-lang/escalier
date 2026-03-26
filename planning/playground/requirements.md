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
├── escalier.toml
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
- **R3.3** Each `.esc` file under `bin/` is its own `ast.Script`. Scripts
  automatically have access to **all** symbols in the package's `lib/` module,
  including non-exported symbols. This is different from cross-package imports,
  where only exported symbols are available.
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
├── escalier.toml
├── pnpm-workspace.yaml
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
- **R4.5** A `pnpm-workspace.yaml` file at the monorepo root defines which
  directories contain packages:
  ```yaml
  packages:
    - "packages/*"
  ```
  This mirrors real pnpm monorepo conventions and is used to discover package
  locations during validation and compilation.

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

#### 5.4. File Storage and Build Integration
- **R5.4.1** Template files live in `playground/templates/` on disk, with one
  subdirectory per template (e.g. `playground/templates/single-package/`,
  `playground/templates/multi-package/`). Each subdirectory contains the actual
  project files (`escalier.toml`, `package.json`, `lib/index.esc`, etc.).
- **R5.4.2** Example files live in `playground/examples/` on disk, with one
  subdirectory per example (e.g. `playground/examples/hello-world/`,
  `playground/examples/calculator/`). Each subdirectory contains the actual
  project files.
- **R5.4.3** The `playground/scripts/copy-files.js` build script copies
  templates and examples into `public/` (or `dist/`) alongside the existing
  TypeScript `.d.ts` files.
- **R5.4.4** The manifest (`manifest.json`) produced by `copy-files.js` is
  extended to include templates and examples with their full file trees. For
  example:
  ```json
  {
    "types": ["lib.es5.d.ts", "lib.dom.d.ts", ...],
    "templates": {
      "single-package": ["escalier.toml", "package.json", "lib/index.esc", "bin/main.esc"],
      "multi-package": ["escalier.toml", "packages/core/package.json", ...]
    },
    "examples": {
      "hello-world": ["escalier.toml", "package.json", "lib/index.esc"],
      "calculator": ["escalier.toml", "package.json", "lib/index.esc", "lib/math.esc"],
      ...
    }
  }
  ```
- **R5.4.5** At runtime, the playground fetches the manifest to discover
  available templates and examples, then fetches individual files on demand
  when loading a template or example.

### 6. Permalinks

Users can generate a shareable permalink that captures the full state of their
playground project.

- **R6.1** A "Share" button generates a permalink URL for the current project.
- **R6.2** The permalink encodes the entire virtual filesystem (all source
  files and their contents) into the URL. The `build/` directory is excluded
  since it is regenerated on load. The `node_modules/` directory is excluded
  since it contains TypeScript `.d.ts` files that are already provided by the
  playground's build-time manifest.
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
- **R7.2** **Multi-package**: A `pnpm-workspace.yaml` file exists at the root
  and the directories it references contain subdirectories that each have their
  own `package.json` with `lib/` and/or `bin/` directories.
- **R7.3** **Precedence**: If a `packages/` directory exists, the project is
  treated as multi-package. A root `package.json` alongside `packages/` is
  allowed (as in real monorepos) but does not trigger single-package mode.
- **R7.4** If the filesystem doesn't match either pattern, the playground
  surfaces an error message to the user explaining what's wrong (e.g. "Missing
  package.json", "No lib/ or bin/ directory found").
- **R7.5** Validation re-runs as the user creates/deletes/renames files, so a
  project can naturally transition between modes (e.g. adding a `packages/`
  directory switches to multi-package mode).

### 8. Project Root Identification (`escalier.toml`)

The project currently uses a `go.mod` file as a marker for the LSP server's
`findRepoRoot` function. This is a workaround — real Escalier projects won't
have a `go.mod`. This should be replaced with a dedicated project manifest.

- **R8.1** A new `escalier.toml` file at the project root identifies an
  Escalier project and serves as the project root marker. The LSP server's
  `findRepoRoot` should look for `escalier.toml` instead of `go.mod`.
- **R8.2** In single-package mode, `escalier.toml` lives at the project root:
  ```
  /
  ├── escalier.toml
  ├── package.json
  ├── lib/
  └── bin/
  ```
- **R8.3** In multi-package mode, `escalier.toml` lives at the monorepo root
  alongside `pnpm-workspace.yaml`:
  ```
  /
  ├── escalier.toml
  ├── pnpm-workspace.yaml
  └── packages/
      ├── core/
      │   └── package.json
      └── app/
          └── package.json
  ```
- **R8.4** `escalier.toml` contains project-level settings. At minimum:
  ```toml
  [project]
  name = "my-project"
  ```
  Future settings (e.g. compiler options, target version) can be added here.
- **R8.5** Templates and examples should include an `escalier.toml` in their
  file sets.
- **R8.6** The LSP server (Go side) must be updated: replace `go.mod` lookup
  in `findRepoRoot` with `escalier.toml` lookup.
- **R8.7** The `createVolume` function must be updated: replace the `go.mod`
  entry with an `escalier.toml` entry.

### 9. Virtual Filesystem Updates

- **R9.1** The `BrowserFS` / volume system must support creating, deleting, and
  renaming files and directories.
- **R9.2** The LSP server must be notified of filesystem changes so diagnostics
  and completions stay current.

### 10. Compilation

- **R10.1** Compilation should be triggered on file save or after a debounce on
  edit (as it works today).
- **R10.2** The LSP server should be aware of all files in the project, even in
  multi-package mode.
- **R10.3** In single-package mode, compiling produces output for all source
  files.
- **R10.4** In multi-package mode:
  - **R10.4.1** Inter-package dependencies must be resolved into a DAG
    (directed acyclic graph) based on the `dependencies` declared in each
    package's `package.json`.
  - **R10.4.2** Packages are type-checked and compiled in topological order so
    that a package's dependencies are built before it is.
  - **R10.4.3** If cyclic dependencies are detected between packages, this is a
    **fatal error** and should be surfaced to the user immediately.
  - **R10.4.4** If a dependency declared in a package's `package.json` cannot
    be found among the workspace packages, this is a **fatal error** and should
    be surfaced to the user immediately.
- **R10.5** The LSP server is responsible for writing compiled output files
  directly to the virtual filesystem (via `FSAPI` write operations). This
  mirrors how a real `escalier` CLI would compile sources and write outputs to
  disk, enabling code sharing between the LSP server and the CLI in the future.
  The `compile` workspace command should write output files and return a
  success/failure status (with diagnostics) rather than returning file contents.
  - **R10.5.1** `lib/` sources produce `.js`, `.js.map`, and `.d.ts` files.
  - **R10.5.2** `bin/` sources produce `.js` and `.js.map` files only.
- **R10.6** The `FSAPI` interface exposed to the LSP server (Go/WASM side) must
  include write operations (`writeFile`, `mkdir`) so the compiler can write
  output files. Filesystem change events from these writes automatically update
  the file explorer and output tabs on the TypeScript side.
