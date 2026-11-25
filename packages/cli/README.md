# @escalier/cli

The command-line compiler for the
[Escalier](https://github.com/escalier-lang/escalier) programming language.

```sh
npm install --save-dev @escalier/cli
npx escalier build .
```

Projects that run the compiled code also need
[`@escalier/runtime`](https://www.npmjs.com/package/@escalier/runtime).

## How the binary is installed

The compiler is a native binary. `@escalier/cli` lists one package per
supported platform in `optionalDependencies`, and npm, pnpm and yarn install
only the one that matches your machine:

| Platform | Package |
| --- | --- |
| macOS on Apple silicon | `@escalier/cli-darwin-arm64` |
| macOS on Intel | `@escalier/cli-darwin-x64` |
| Linux on x64 | `@escalier/cli-linux-x64` |
| Linux on ARM64 | `@escalier/cli-linux-arm64` |
| Linux on 32-bit ARMv7 | `@escalier/cli-linux-arm` |
| Windows on x64 | `@escalier/cli-win32-x64` |
| Windows on ARM64 | `@escalier/cli-win32-arm64` |

The Linux binaries are statically linked, so they also run on musl
distributions such as Alpine.

If the `escalier` command reports a missing platform package, reinstall
without `--no-optional` or `--omit=optional`. To use a binary you built
yourself, set `ESCALIER_BINARY_PATH` to its path.

## The standard library

The binary reads the standard library's `.esc` files from disk.
`@escalier/cli` ships them in its `data` directory, and the `escalier`
command sets `ESCALIER_STDLIB_DIR` to that directory unless you have set it
yourself. It does so for a binary from `ESCALIER_BINARY_PATH` too, so set
`ESCALIER_STDLIB_DIR` as well to run your binary with another copy of the
standard library.

## JavaScript API

```js
import { spawnSync } from 'node:child_process';
import { getBinaryPath, getStdlibDir } from '@escalier/cli';

// The path of the escalier binary for this machine, and the standard
// library directory to run it with, for tools that spawn the compiler
// themselves.
spawnSync(getBinaryPath(), ['build', '.'], {
    env: {
        ...process.env,
        ESCALIER_STDLIB_DIR: process.env.ESCALIER_STDLIB_DIR || getStdlibDir(),
    },
    stdio: 'inherit',
});
```

`getBinaryPath()` throws an `Error` that explains what is missing when the
platform has no package or its package isn't installed.
