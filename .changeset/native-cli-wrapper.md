---
'@escalier/cli': minor
---

Run the native escalier binary from the matching `@escalier/cli-<platform>-<arch>` package instead of the wasm build. Set `ESCALIER_BINARY_PATH` to use a binary of your own. `getBinaryPath()` returns the path of the binary for tools that spawn it themselves.
