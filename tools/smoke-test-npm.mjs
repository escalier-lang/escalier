#!/usr/bin/env node
//
// Installs the packed @escalier/cli tarballs into a new temporary project
// and checks that the installed command works on this machine.
//
//     node tools/smoke-test-npm.mjs --tarballs <dir>
//         [--package-manager npm|pnpm] [--target <suffix>]
//         [--emulator <command>]
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
// The script checks that:
//
// - only the platform package for the target was installed,
// - getBinaryPath() from @escalier/cli returns an executable file in that
//   package,
// - `escalier --version` prints the version in packages/cli/package.json,
// - `escalier build` compiles a one-file project that imports from `std:`,
//   and another that imports from `web:`.
//
// --target names the platform to install for, such as `linux-arm`. It
// defaults to this machine's platform. A target that differs from this
// machine needs --emulator, the command that runs the target's binary,
// such as `qemu-arm`. Node on this machine can't run the wrapper for
// another platform, so for such a target the script runs the binary that
// getBinaryPath() returns under the emulator instead of running
// `npx escalier`. Only npm can install for another platform, through its
// --os and --cpu flags.

import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs as parseArgv } from 'node:util';

import { TARGETS, platformPackageName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

const CLI_PACKAGE = '@escalier/cli';

// The projects that the build checks compile, one per stdlib directory.
// Each `source` imports from that directory, so the build only succeeds
// when the installed @escalier/cli carries it. `output` is a call that the
// compiled lib/index.js must contain.
const FIXTURES = [
    {
        name: 'std',
        source: [
            'import "std:math"',
            '',
            'export fn hypot(a: number, b: number) -> number {',
            '    return math.sqrt(a * a + b * b)',
            '}',
            '',
        ].join('\n'),
        output: 'Math.sqrt(',
    },
    {
        name: 'web',
        source: [
            'import "web:crypto"',
            '',
            'export fn newID() -> string {',
            '    return crypto.randomUUID()',
            '}',
            '',
        ].join('\n'),
        output: 'crypto.randomUUID()',
    },
];

// Returns the options that `argv` selects. `hostTarget` is the suffix of
// this machine's platform, which --target defaults to. Throws on an
// unknown flag, a missing value, a positional argument, an unknown target
// or package manager, or a target other than `hostTarget` without an
// emulator or with pnpm.
export function parseArgs(argv, hostTarget) {
    const { values } = parseArgv({
        args: argv,
        options: {
            tarballs: { type: 'string' },
            'package-manager': { type: 'string', default: 'npm' },
            target: { type: 'string', default: hostTarget },
            emulator: { type: 'string' },
        },
        strict: true,
        allowPositionals: false,
    });
    if (values.tarballs === undefined) {
        throw new Error('--tarballs <dir> is required');
    }
    const packageManager = values['package-manager'];
    if (packageManager !== 'npm' && packageManager !== 'pnpm') {
        throw new Error(
            `unknown package manager ${packageManager}, expected npm or pnpm`,
        );
    }
    const target = TARGETS.find((t) => t.suffix === values.target);
    if (target === undefined) {
        const suffixes = TARGETS.map((t) => t.suffix).join(', ');
        throw new Error(
            `unknown target ${values.target}, expected one of: ${suffixes}`,
        );
    }
    if (target.suffix !== hostTarget) {
        if (values.emulator === undefined) {
            throw new Error(
                `--target ${target.suffix} differs from this machine's ${hostTarget} and needs --emulator`,
            );
        }
        if (packageManager !== 'npm') {
            throw new Error(
                `--target ${target.suffix} differs from this machine's ${hostTarget}, which only npm can install for`,
            );
        }
    }
    return {
        tarballs: values.tarballs,
        packageManager,
        target,
        emulator: values.emulator,
    };
}

// Returns the tarball file names in `files` keyed by package name. pnpm
// names a tarball after its package and version, so `@escalier/cli-linux-x64`
// at 0.0.1 packs to `escalier-cli-linux-x64-0.0.1.tgz`. Throws when a
// package in TARGETS or @escalier/cli has no tarball.
export function findTarballs(files) {
    const names = [
        CLI_PACKAGE,
        ...TARGETS.map((t) => platformPackageName(t.suffix)),
    ];
    const tarballs = {};
    for (const name of names) {
        // The version follows the name and starts with a digit, which tells
        // `escalier-cli-0.0.1.tgz` apart from the platform packages.
        const prefix = `${name.slice(1).replace('/', '-')}-`;
        const file = files.find(
            (f) =>
                f.startsWith(prefix) &&
                /^\d/.test(f.slice(prefix.length)) &&
                f.endsWith('.tgz'),
        );
        if (file === undefined) {
            throw new Error(`no tarball for ${name}, expected ${prefix}*.tgz`);
        }
        tarballs[name] = file;
    }
    return tarballs;
}

// Returns the package.json of the project that installs @escalier/cli.
// `paths` maps each package name to the path of its tarball or extracted
// package, relative to the project. npm reads the top-level `overrides` and
// pnpm reads `pnpm.overrides`.
export function projectManifest(paths, packageManager) {
    const overrides = {};
    for (const [name, path] of Object.entries(paths)) {
        if (name !== CLI_PACKAGE) {
            overrides[name] = `file:${path}`;
        }
    }
    const manifest = {
        name: 'escalier-smoke-test',
        private: true,
        version: '0.0.0',
        dependencies: { [CLI_PACKAGE]: `file:${paths[CLI_PACKAGE]}` },
    };
    if (packageManager === 'pnpm') {
        manifest.pnpm = { overrides };
    } else {
        manifest.overrides = overrides;
    }
    return manifest;
}

// Returns the command and arguments that install the project's
// dependencies for `target`. `crossInstall` is true when `target` differs
// from this machine.
export function installCommand(packageManager, target, crossInstall) {
    if (packageManager === 'pnpm') {
        // The project has no lockfile. pnpm requires one by default when
        // the CI variable is set, as it is on GitHub Actions.
        return ['pnpm', ['install', '--no-frozen-lockfile']];
    }
    const args = ['install', '--no-audit', '--no-fund'];
    if (crossInstall) {
        args.push(`--os=${target.os}`, `--cpu=${target.cpu}`);
    }
    return ['npm', args];
}

// Returns the command and arguments that run the installed `escalier`
// command with `args`.
export function execCommand(packageManager, args) {
    if (packageManager === 'pnpm') {
        return ['pnpm', ['exec', 'escalier', ...args]];
    }
    // --no stops npx from fetching escalier from the registry when the
    // install didn't link the command. npx reads flags that follow the
    // command name as its own, so without `--` it would print npm's
    // version for `escalier --version`.
    return ['npx', ['--no', '--', 'escalier', ...args]];
}

// The environment for every child process. ESCALIER_BINARY_PATH would make
// getBinaryPath() return its value instead of the installed binary, and
// ESCALIER_STDLIB_DIR would stop the `escalier` command from pointing the
// binary at the stdlib that @escalier/cli ships, so the checks run without
// either.
function childEnv() {
    const env = { ...process.env };
    delete env.ESCALIER_BINARY_PATH;
    delete env.ESCALIER_STDLIB_DIR;
    return env;
}

// Runs `command` with `args` in `cwd`, with `extraEnv` added to
// childEnv(), and returns its exit status and output. On Windows none of
// `args` may contain a space.
function run(command, args, cwd, extraEnv = {}) {
    const options = {
        cwd,
        env: { ...childEnv(), ...extraEnv },
        encoding: 'utf8',
    };
    // npm, npx and pnpm are `.cmd` scripts on Windows, which Node only runs
    // through a shell. Node deprecates passing an argument array along with
    // `shell`, so on Windows the call joins them into one unquoted command
    // line.
    const result =
        process.platform === 'win32'
            ? spawnSync([command, ...args].join(' '), {
                  ...options,
                  shell: true,
              })
            : spawnSync(command, args, options);
    if (result.error !== undefined) {
        return {
            ok: false,
            output: `failed to run ${command}: ${result.error.message}`,
        };
    }
    const output = `${result.stdout}${result.stderr}`.trim();
    const ok = result.status === 0;
    return { ok, output, stdout: result.stdout.trim() };
}

// Returns the platform packages that resolve from the installed
// @escalier/cli, which is where its getBinaryPath() looks for them.
function installedPlatformPackages(projectDir) {
    const cliManifest = fs.realpathSync(
        join(projectDir, 'node_modules', '@escalier', 'cli', 'package.json'),
    );
    const requireFromCli = createRequire(cliManifest);
    return TARGETS.map((t) => platformPackageName(t.suffix)).filter((name) => {
        try {
            requireFromCli.resolve(`${name}/package.json`);
            return true;
        } catch {
            return false;
        }
    });
}

// Returns `{ binaryPath, stdlibDir }`, the paths that getBinaryPath() for
// `target` and getStdlibDir() from the installed @escalier/cli return. The
// script imports the package in a child process from the project directory,
// so the import resolves the way it does for a project that depends on
// @escalier/cli.
function installedPaths(projectDir, target) {
    const options = JSON.stringify({ platform: target.os, arch: target.cpu });
    // The child prints only the error's message, not its stack trace.
    const code = [
        `import { getBinaryPath, getStdlibDir } from '${CLI_PACKAGE}';`,
        'try {',
        `    const binaryPath = getBinaryPath(${options});`,
        '    console.log(JSON.stringify({ binaryPath, stdlibDir: getStdlibDir() }));',
        '} catch (err) {',
        '    console.error(err.message);',
        '    process.exitCode = 1;',
        '}',
    ].join('\n');
    const result = spawnSync(
        process.execPath,
        ['--input-type=module', '--eval', code],
        { cwd: projectDir, env: childEnv(), encoding: 'utf8' },
    );
    if (result.status !== 0) {
        throw new Error(`getBinaryPath() failed:\n${result.stderr.trim()}`);
    }
    return JSON.parse(result.stdout);
}

// Throws unless `path` is an executable file inside `packageName`.
function checkBinary(path, packageName) {
    if (!path.split(/[\\/]/).join('/').includes(`/${packageName}/bin/`)) {
        throw new Error(`${path} is not inside ${packageName}/bin/`);
    }
    if (!fs.statSync(path).isFile()) {
        throw new Error(`${path} is not a file`);
    }
    // Windows has no executable bit. It runs a file by its extension.
    if (process.platform !== 'win32') {
        try {
            fs.accessSync(path, fs.constants.X_OK);
        } catch {
            throw new Error(`${path} is not executable`);
        }
    }
}

function main() {
    const hostTarget = `${process.platform}-${process.arch}`;
    let options;
    try {
        options = parseArgs(process.argv.slice(2), hostTarget);
    } catch (err) {
        console.error(err.message);
        process.exit(2);
    }
    const { packageManager, target, emulator } = options;
    const crossInstall = target.suffix !== hostTarget;
    const expectedPackage = platformPackageName(target.suffix);
    const version = JSON.parse(
        fs.readFileSync(join(ROOT, 'packages', 'cli', 'package.json'), 'utf8'),
    ).version;

    let tarballDir;
    let tarballs;
    try {
        tarballDir = fs.realpathSync(options.tarballs);
        tarballs = findTarballs(fs.readdirSync(tarballDir));
    } catch (err) {
        console.error(err.message);
        process.exit(2);
    }
    const projectDir = fs.mkdtempSync(join(tmpdir(), 'escalier-smoke-'));
    console.log(
        `installing ${CLI_PACKAGE} for ${target.suffix} with ${packageManager} in ${projectDir}`,
    );
    // Prints `message` and exits with status 1. The project directory is
    // left in place so the failed install can be inspected. A passing run
    // deletes it.
    const fail = (message) => {
        console.error(`${message}\nkept ${projectDir} for inspection`);
        process.exit(1);
    };

    // The tarballs are copied into the project so the manifest can name
    // them by relative paths, which avoids writing a Windows drive letter
    // into a `file:` specifier.
    fs.mkdirSync(join(projectDir, 'tarballs'));
    const paths = {};
    for (const [name, file] of Object.entries(tarballs)) {
        fs.copyFileSync(
            join(tarballDir, file),
            join(projectDir, 'tarballs', file),
        );
        paths[name] = `./tarballs/${file}`;
    }
    // pnpm installs every platform package when the overrides name
    // tarballs, whatever their `os` and `cpu`, so for pnpm the overrides
    // name the extracted packages instead. tar keeps each file's mode, so
    // a tarball that lost the binary's executable bit still fails.
    if (packageManager === 'pnpm') {
        fs.mkdirSync(join(projectDir, 'packages'));
        for (const [name, file] of Object.entries(tarballs)) {
            const dir = `packages/${file.slice(0, -'.tgz'.length)}`;
            fs.mkdirSync(join(projectDir, dir));
            const result = run(
                'tar',
                ['-xzf', `tarballs/${file}`, '-C', dir, '--strip-components=1'],
                projectDir,
            );
            if (!result.ok) {
                fail(`not ok - extracting ${file} failed:\n${result.output}`);
            }
            paths[name] = `./${dir}`;
        }
    }
    fs.writeFileSync(
        join(projectDir, 'package.json'),
        `${JSON.stringify(projectManifest(paths, packageManager), null, 2)}\n`,
    );
    for (const { name, source } of FIXTURES) {
        const fixtureDir = join(projectDir, name);
        fs.mkdirSync(join(fixtureDir, 'lib'), { recursive: true });
        fs.writeFileSync(
            join(fixtureDir, 'package.json'),
            `${JSON.stringify({ name, private: true, version: '0.0.0', type: 'module' }, null, 2)}\n`,
        );
        fs.writeFileSync(join(fixtureDir, 'lib', 'index.esc'), source);
    }

    const [installer, installArgs] = installCommand(
        packageManager,
        target,
        crossInstall,
    );
    const install = run(installer, installArgs, projectDir);
    if (!install.ok) {
        fail(`not ok - install failed:\n${install.output}`);
    }

    // Runs the escalier binary with `args`. For the host's own target it
    // goes through the installed `escalier` command. For another target it
    // runs the binary under the emulator.
    let cliPaths;
    const runEscalier = (args) => {
        if (!crossInstall) {
            const [command, commandArgs] = execCommand(packageManager, args);
            return run(command, commandArgs, projectDir);
        }
        if (cliPaths === undefined) {
            return { ok: false, output: 'getBinaryPath() returned no path' };
        }
        // The `escalier` command would point the binary at the stdlib in
        // the installed @escalier/cli. Running the binary directly skips
        // the command, so the call sets the same directory itself.
        return run(emulator, [cliPaths.binaryPath, ...args], projectDir, {
            ESCALIER_STDLIB_DIR: cliPaths.stdlibDir,
        });
    };

    const checks = [
        {
            name: `only ${expectedPackage} is installed`,
            check: () => {
                const installed = installedPlatformPackages(projectDir);
                if (
                    installed.length !== 1 ||
                    installed[0] !== expectedPackage
                ) {
                    throw new Error(
                        `installed platform packages: ${installed.join(', ') || 'none'}`,
                    );
                }
            },
        },
        {
            name: 'getBinaryPath() returns an executable file',
            check: () => {
                cliPaths = installedPaths(projectDir, target);
                checkBinary(cliPaths.binaryPath, expectedPackage);
            },
        },
        {
            name: `escalier --version prints ${version}`,
            check: () => {
                const result = runEscalier(['--version']);
                if (!result.ok || result.stdout !== version) {
                    throw new Error(result.output);
                }
            },
        },
        ...FIXTURES.map(({ name, output }) => ({
            name: `escalier build compiles a ${name}: import`,
            check: () => {
                const result = runEscalier(['build', name]);
                if (!result.ok) {
                    throw new Error(result.output);
                }
                const outPath = join(
                    projectDir,
                    name,
                    'build',
                    'lib',
                    'index.js',
                );
                if (!fs.existsSync(outPath)) {
                    throw new Error(
                        `${name}/build/lib/index.js is missing:\n${result.output}`,
                    );
                }
                const js = fs.readFileSync(outPath, 'utf8');
                if (!js.includes(output)) {
                    throw new Error(
                        `${name}/build/lib/index.js has no ${output}:\n${js}`,
                    );
                }
            },
        })),
    ];

    let failed = 0;
    for (const { name, check } of checks) {
        try {
            check();
            console.log(`ok - ${name}`);
        } catch (err) {
            failed++;
            console.error(`not ok - ${name}\n${err.message}`);
        }
    }
    if (failed > 0) {
        fail(`${failed} of ${checks.length} checks failed`);
    }
    fs.rmSync(projectDir, { recursive: true, force: true });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
