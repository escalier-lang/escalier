# @escalier/cli

## 0.1.1

### Patch Changes

- 6e45d7d: License `@escalier/cli` and its binary packages under Apache-2.0 and `@escalier/runtime` under 0BSD, and ship the licence and third-party notices in each package.

## 0.1.0

### Minor Changes

- 9ac2d50: Ship the standard library's `.esc` files in `@escalier/cli`, so `escalier build` works outside the escalier repo. The `escalier` command points the binary at them through `ESCALIER_STDLIB_DIR` unless you set it yourself. `getStdlibDir()` returns their directory for tools that spawn the binary themselves. The platform packages now keep the binary's execute permission when `pnpm` packs them.
- a65ef7a: Run the native escalier binary from the matching `@escalier/cli-<platform>-<arch>` package instead of the wasm build. Set `ESCALIER_BINARY_PATH` to use a binary of your own. `getBinaryPath()` returns the path of the binary for tools that spawn it themselves.

### Patch Changes

- 5debb14: Start each compiled `bin/` script with `#!/usr/bin/env node`, so a package's `bin` commands run when npm, pnpm or yarn installs them.
- 6c92d5a: Fix `escalier build` on Windows, which compiled no files because it read source paths with backslashes.
