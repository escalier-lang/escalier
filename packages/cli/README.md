# @escalier/cli

The command-line compiler for the
[Escalier](https://github.com/escalier-lang/escalier) programming language.

```sh
npm install --save-dev @escalier/cli
npx escalier build
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

## JavaScript API

```js
import { getBinaryPath } from '@escalier/cli';

// The path of the escalier binary for this machine, for tools that spawn
// the compiler themselves.
const escalier = getBinaryPath();
```

`getBinaryPath()` throws an `Error` that explains what is missing when the
platform has no package or its package isn't installed.
