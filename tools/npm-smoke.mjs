// Helpers for the npm smoke test in tools/npm-smoke.e2e.test.mjs, which
// installs the packed @escalier/cli tarballs into a temporary project and
// checks that the installed command works. tools/npm-smoke.test.mjs tests
// these helpers.

import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs as parseArgv } from 'node:util';

import { TARGETS, platformPackageName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

export const CLI_PACKAGE = '@escalier/cli';

// The packages that the build checks compile. Each is `smoke-<name>` in the
// directory of that name, with the `.esc` sources in `files`.
//
// - `label` names the package in the check names.
// - `output` is `[path, text]`: the compiled file under build/ and text it
//   must contain.
// - `run` is a module that imports the package as `lib` and prints one
//   line, and `stdout` is that line.
// - `bin` is the line the package's bin command, also named
//   `smoke-<name>`, prints.
//
// A package with lib/index.esc gets `main`, and one with bin/main.esc gets
// `bin`. The std and web libraries import from those stdlib directories, so
// they only compile when the installed @escalier/cli carries them.
export const FIXTURES = [
    {
        name: 'std',
        label: 'a std: import',
        files: {
            'lib/index.esc':
                'import "std:math"\n\nexport fn hypot(a: number, b: number) -> number {\n    return math.sqrt(a * a + b * b)\n}\n',
        },
        output: ['lib/index.js', 'Math.sqrt('],
        run: 'console.log(lib.hypot(3, 4));',
        stdout: '5',
    },
    {
        name: 'web',
        label: 'a web: import',
        files: {
            'lib/index.esc':
                'import "web:crypto"\n\nexport fn newID() -> string {\n    return crypto.randomUUID()\n}\n',
        },
        output: ['lib/index.js', 'crypto.randomUUID()'],
        run: 'console.log(/^[0-9a-f-]{36}$/.test(lib.newID()));',
        stdout: 'true',
    },
    {
        name: 'bin',
        label: 'a bin-only package',
        files: {
            'bin/main.esc':
                'import "std:console"\n\nconsole.log("hello from smoke-bin")\n',
        },
        output: ['bin/main.js', 'console.log('],
        bin: 'hello from smoke-bin',
    },
    {
        name: 'bin-lib',
        label: 'a bin+lib package',
        files: {
            'lib/index.esc':
                'export fn greet(name: string) -> string {\n    return "hello, " ++ name\n}\n',
            'bin/main.esc':
                'import "std:console"\n\nconsole.log(greet("smoke"))\n',
        },
        output: ['bin/main.js', '"../lib/index.js"'],
        bin: 'hello, smoke',
    },
];

// Returns the package.json of the FIXTURES entry `fixture`.
export function fixtureManifest(fixture) {
    const name = `smoke-${fixture.name}`;
    const manifest = { name, private: true, version: '0.0.0', type: 'module' };
    if ('lib/index.esc' in fixture.files) {
        manifest.main = 'build/lib/index.js';
    }
    if ('bin/main.esc' in fixture.files) {
        manifest.bin = { [name]: 'build/bin/main.js' };
    }
    return manifest;
}

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
// pnpm reads `pnpm.overrides`. `localPackages` maps the name of each
// package in a subdirectory of the project to that directory, and the
// project depends on each one through `file:`.
export function projectManifest(paths, packageManager, localPackages = {}) {
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
    for (const [name, dir] of Object.entries(localPackages)) {
        manifest.dependencies[name] = `file:${dir}`;
    }
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

// Returns the command and arguments that run the installed command
// `command` with `args` through `packageManager`.
export function execCommand(packageManager, command, args) {
    if (packageManager === 'pnpm') {
        return ['pnpm', ['exec', command, ...args]];
    }
    // --no stops npx from fetching the command from the registry when the
    // install didn't link it. npx reads flags that follow the command name
    // as its own, so without `--` it would print npm's version for
    // `escalier --version`.
    return ['npx', ['--no', '--', command, ...args]];
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
export function run(command, args, cwd, extraEnv = {}) {
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
export function installedPlatformPackages(projectDir) {
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
export function installedPaths(projectDir, target) {
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

// Returns the arguments for parseArgs() that the smoke test's environment
// variables in `env` select, or `undefined` when ESCALIER_SMOKE_TARBALLS
// is unset or empty:
//
// - ESCALIER_SMOKE_TARBALLS gives --tarballs,
// - ESCALIER_SMOKE_PACKAGE_MANAGER gives --package-manager,
// - ESCALIER_SMOKE_TARGET gives --target,
// - ESCALIER_SMOKE_EMULATOR gives --emulator.
//
// An unset or empty variable leaves its flag out.
export function smokeArgsFromEnv(env) {
    if (!env.ESCALIER_SMOKE_TARBALLS) {
        return undefined;
    }
    const flags = [
        ['--tarballs', env.ESCALIER_SMOKE_TARBALLS],
        ['--package-manager', env.ESCALIER_SMOKE_PACKAGE_MANAGER],
        ['--target', env.ESCALIER_SMOKE_TARGET],
        ['--emulator', env.ESCALIER_SMOKE_EMULATOR],
    ];
    return flags.flatMap(([flag, value]) => (value ? [flag, value] : []));
}

// Returns the options that `argv` selects, with the version that `escalier
// --version` should print and the tarballs to install. Throws on a bad
// argument or tarball directory.
export function readOptions(argv) {
    const hostTarget = `${process.platform}-${process.arch}`;
    const options = parseArgs(argv, hostTarget);
    const tarballDir = fs.realpathSync(options.tarballs);
    const tarballs = findTarballs(fs.readdirSync(tarballDir));
    const { version } = JSON.parse(
        fs.readFileSync(join(ROOT, 'packages', 'cli', 'package.json'), 'utf8'),
    );
    return {
        ...options,
        crossInstall: options.target.suffix !== hostTarget,
        version,
        tarballDir,
        tarballs,
    };
}

// Copies the tarballs into `projectDir`/tarballs and returns the path of
// each, relative to the project, keyed by package name. Relative paths keep
// a Windows drive letter out of the `file:` specifiers.
function copyTarballs(tarballDir, tarballs, projectDir) {
    fs.mkdirSync(join(projectDir, 'tarballs'));
    const paths = {};
    for (const [name, file] of Object.entries(tarballs)) {
        fs.copyFileSync(
            join(tarballDir, file),
            join(projectDir, 'tarballs', file),
        );
        paths[name] = `./tarballs/${file}`;
    }
    return paths;
}

// Extracts each tarball in `projectDir`/tarballs into `projectDir`/packages
// and returns the path of each extracted package, relative to the project,
// keyed by package name. tar keeps each file's mode. Throws when tar fails.
function extractTarballs(tarballs, projectDir) {
    fs.mkdirSync(join(projectDir, 'packages'));
    const paths = {};
    for (const [name, file] of Object.entries(tarballs)) {
        const dir = `packages/${file.slice(0, -'.tgz'.length)}`;
        fs.mkdirSync(join(projectDir, dir));
        const result = run(
            'tar',
            ['-xzf', `tarballs/${file}`, '-C', dir, '--strip-components=1'],
            projectDir,
        );
        if (!result.ok) {
            throw new Error(`extracting ${file} failed:\n${result.output}`);
        }
        paths[name] = `./${dir}`;
    }
    return paths;
}

// Writes each entry of FIXTURES into its own directory under `projectDir`.
function writeFixtures(projectDir) {
    for (const fixture of FIXTURES) {
        const fixtureDir = join(projectDir, `smoke-${fixture.name}`);
        fs.mkdirSync(fixtureDir);
        fs.writeFileSync(
            join(fixtureDir, 'package.json'),
            `${JSON.stringify(fixtureManifest(fixture), null, 2)}\n`,
        );
        for (const [path, source] of Object.entries(fixture.files)) {
            fs.mkdirSync(join(fixtureDir, dirname(path)), { recursive: true });
            fs.writeFileSync(join(fixtureDir, path), source);
        }
    }
}

// Writes `manifest` as the project's package.json and installs it with the
// options' package manager. Throws when the install fails.
export function installProject(options, projectDir, manifest) {
    fs.writeFileSync(
        join(projectDir, 'package.json'),
        `${JSON.stringify(manifest, null, 2)}\n`,
    );
    const [installer, installArgs] = installCommand(
        options.packageManager,
        options.target,
        options.crossInstall,
    );
    const install = run(installer, installArgs, projectDir);
    if (!install.ok) {
        throw new Error(`install failed:\n${install.output}`);
    }
}

// Writes the project that installs the tarballs into `projectDir` and
// installs it. Returns the paths of the installed packages, keyed by
// package name, for projectManifest(). Throws when extracting a tarball or
// the install fails.
export function setUpProject(options, projectDir) {
    const { packageManager, tarballDir, tarballs } = options;
    let paths = copyTarballs(tarballDir, tarballs, projectDir);
    // pnpm installs every platform package when the overrides name
    // tarballs, whatever their `os` and `cpu`, so for pnpm the overrides
    // name the extracted packages instead. A tarball that lost the binary's
    // executable bit still fails, because tar keeps file modes.
    if (packageManager === 'pnpm') {
        paths = extractTarballs(tarballs, projectDir);
    }
    writeFixtures(projectDir);
    installProject(options, projectDir, projectManifest(paths, packageManager));
    return paths;
}

// Runs `code` as an ES module with this machine's Node, from `cwd`, and
// returns its exit status and output the way run() does. The compiled
// JavaScript doesn't depend on the target, so the host's Node runs it for an
// emulated target too.
export function runModule(code, cwd) {
    const result = spawnSync(
        process.execPath,
        ['--input-type=module', '--eval', code],
        { cwd, env: childEnv(), encoding: 'utf8' },
    );
    const output = `${result.stdout}${result.stderr}`.trim();
    return { ok: result.status === 0, output, stdout: result.stdout.trim() };
}

// Returns a function that runs the escalier binary with its arguments in
// `projectDir` and returns the result the way run() does. For the host's
// own target it goes through the installed `escalier` command. For another
// target it runs the binary that `getPaths()` names under the emulator.
export function escalierRunner(options, projectDir, getPaths) {
    const { packageManager, crossInstall, emulator } = options;
    return (args) => {
        if (!crossInstall) {
            const [command, commandArgs] = execCommand(
                packageManager,
                'escalier',
                args,
            );
            return run(command, commandArgs, projectDir);
        }
        const paths = getPaths();
        if (paths === undefined) {
            return { ok: false, output: 'getBinaryPath() returned no path' };
        }
        // The `escalier` command would point the binary at the stdlib in
        // the installed @escalier/cli. Running the binary directly skips
        // the command, so the call sets the same directory itself.
        return run(emulator, [paths.binaryPath, ...args], projectDir, {
            ESCALIER_STDLIB_DIR: paths.stdlibDir,
        });
    };
}
