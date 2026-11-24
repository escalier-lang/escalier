---
'@escalier/cli': minor
---

Ship the standard library's `.esc` files in `@escalier/cli`, so `escalier build` works outside the escalier repo. The `escalier` command points the binary at them through `ESCALIER_STDLIB_DIR` unless you set it yourself or set `ESCALIER_BINARY_PATH`. `getStdlibDir()` returns their directory for tools that spawn the binary themselves. The platform packages now keep the binary's execute permission when `pnpm` packs them.
