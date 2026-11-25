// Installs the packed @escalier/cli tarballs into a new temporary project
// and checks that the installed command works on this machine.
//
//     ESCALIER_SMOKE_TARBALLS=<dir> node --test tools/npm-smoke.e2e.test.mjs
//
// The test skips itself when ESCALIER_SMOKE_TARBALLS is unset, so a plain
// `node --test` run over tools/ passes without tarballs.
//
// <dir> holds the tarballs that `pnpm pack` writes for packages/cli and for
// every package under packages/platforms. The project depends on the
// @escalier/cli tarball. Package manager overrides point each
// @escalier/cli-<suffix> dependency at a local copy of its package, so
// nothing comes from the registry. To build the tarballs in a checkout:
//
//     node tools/build-binaries.mjs
//     for dir in packages/cli packages/platforms/cli-*; do
//         (cd "$dir" && pnpm pack --pack-destination /tmp/escalier-tarballs)
//     done
//
// The tests share one project, which a before() hook installs and an
// after() hook deletes, and they check that:
//
// - only the platform package for the target was installed,
// - getBinaryPath() from @escalier/cli returns an executable file in that
//   package,
// - `escalier --version` prints the version in packages/cli/package.json,
// - `escalier build` compiles each package in FIXTURES: a library that
//   imports from `std:`, one that imports from `web:`, a bin-only package
//   and a package whose bin script calls its library,
// - Node imports each compiled library and prints the expected result,
// - each installed bin command prints the expected line.
//
// ESCALIER_SMOKE_PACKAGE_MANAGER is `npm`, the default, or `pnpm`.
// ESCALIER_SMOKE_TARGET names the platform to install for, such as
// `linux-arm`. It defaults to this machine's platform. A target that differs
// from this machine needs ESCALIER_SMOKE_EMULATOR, the command that runs the
// target's binary, such as `qemu-arm`. Node on this machine can't run the
// wrapper for another platform, so for such a target the tests run the
// binary that getBinaryPath() returns under the emulator instead of running
// `npx escalier`. Only npm can install for another platform, through its
// --os and --cpu flags.

import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, test } from 'node:test';

import {
    CLI_PACKAGE,
    FIXTURES,
    escalierRunner,
    execCommand,
    installProject,
    installedPaths,
    installedPlatformPackages,
    projectManifest,
    readOptions,
    run,
    runModule,
    setUpProject,
    smokeArgsFromEnv,
} from './npm-smoke.mjs';
import { platformPackageName } from './platforms.mjs';

// Defines the smoke tests for `options`, the result of readOptions(). They
// share one project in a temporary directory: a before() hook installs it,
// the tests run in order against it, and an after() hook deletes it. The
// directory comes from fs.mkdtempDisposableSync(), which needs Node 24.4.
function defineSmokeTests(options) {
    const { packageManager, target, version } = options;
    const expectedPackage = platformPackageName(target.suffix);

    describe(`${CLI_PACKAGE} on ${target.suffix} with ${packageManager}`, () => {
        let projectTmp;
        let projectDir;
        // The paths of the installed tarballs, for the second install.
        let paths;
        // The getBinaryPath() test fills this in, and the escalier runner
        // reads it for an emulated target.
        let cliPaths;
        const runEscalier = (args) =>
            escalierRunner(options, projectDir, () => cliPaths)(args);

        before(() => {
            projectTmp = fs.mkdtempDisposableSync(
                join(tmpdir(), 'escalier-smoke-'),
            );
            projectDir = projectTmp.path;
            paths = setUpProject(options, projectDir);
        });

        after(() => {
            projectTmp?.remove();
        });

        test(`only ${expectedPackage} is installed`, () => {
            assert.deepEqual(installedPlatformPackages(projectDir), [
                expectedPackage,
            ]);
        });

        test('getBinaryPath() returns an executable file', () => {
            cliPaths = installedPaths(projectDir, target);
            const { binaryPath } = cliPaths;
            assert.ok(
                binaryPath
                    .split(/[\\/]/)
                    .join('/')
                    .includes(`/${expectedPackage}/bin/`),
                `${binaryPath} is not inside ${expectedPackage}/bin/`,
            );
            assert.ok(
                fs.statSync(binaryPath).isFile(),
                `${binaryPath} is not a file`,
            );
            // Windows has no executable bit. It runs a file by its extension.
            if (process.platform !== 'win32') {
                assert.doesNotThrow(
                    () => fs.accessSync(binaryPath, fs.constants.X_OK),
                    `${binaryPath} is not executable`,
                );
            }
        });

        test(`escalier --version prints ${version}`, () => {
            const result = runEscalier(['--version']);
            assert.ok(result.ok, result.output);
            assert.equal(result.stdout, version);
        });

        for (const { name, label, output } of FIXTURES) {
            test(`escalier build compiles ${label}`, () => {
                const dir = `smoke-${name}`;
                const result = runEscalier(['build', dir]);
                assert.ok(result.ok, result.output);
                const [file, text] = output;
                const outPath = join(projectDir, dir, 'build', file);
                assert.ok(
                    fs.existsSync(outPath),
                    `${dir}/build/${file} is missing:\n${result.output}`,
                );
                const js = fs.readFileSync(outPath, 'utf8');
                assert.ok(
                    js.includes(text),
                    `${dir}/build/${file} has no ${text}:\n${js}`,
                );
            });
        }

        // The fixtures install after they build. npm links a bin command to
        // its target, and pnpm copies a `file:` directory, so both need
        // build/ to exist first.
        test('the compiled packages install', () => {
            const localPackages = Object.fromEntries(
                FIXTURES.map(({ name }) => [
                    `smoke-${name}`,
                    `./smoke-${name}`,
                ]),
            );
            installProject(
                options,
                projectDir,
                projectManifest(paths, packageManager, localPackages),
            );
        });

        for (const { name, run: code, stdout } of FIXTURES) {
            if (code === undefined) {
                continue;
            }
            test(`node imports smoke-${name}`, () => {
                const result = runModule(
                    `import * as lib from 'smoke-${name}';\n${code}`,
                    projectDir,
                );
                assert.ok(result.ok, result.output);
                assert.equal(result.stdout, stdout);
            });
        }

        for (const { name, bin } of FIXTURES) {
            if (bin === undefined) {
                continue;
            }
            test(`smoke-${name} prints ${bin}`, () => {
                const [command, args] = execCommand(
                    packageManager,
                    `smoke-${name}`,
                    [],
                );
                const result = run(command, args, projectDir);
                assert.ok(result.ok, result.output);
                assert.equal(result.stdout, bin);
            });
        }
    });
}

const argv = smokeArgsFromEnv(process.env);
if (argv === undefined) {
    test('npm smoke test', {
        skip: 'set ESCALIER_SMOKE_TARBALLS to the directory of packed tarballs',
    });
} else {
    defineSmokeTests(readOptions(argv));
}
