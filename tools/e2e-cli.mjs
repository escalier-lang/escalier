#!/usr/bin/env node
//
// Installs @escalier/cli from packed tarballs into a project outside the
// checkout and checks that `escalier build` compiles it.
//
//     node tools/e2e-cli.mjs
//
// Inside the checkout the binary finds the stdlib by walking up to
// internal/interop/data, so only an install elsewhere shows whether the
// published packages carry everything the binary reads. The script:
//
//  1. builds the binary for this machine with tools/build-binaries.mjs,
//  2. packs @escalier/cli and the platform package with `pnpm pack`,
//  3. copies fixtures/npm_e2e into a new temporary directory and installs
//     both tarballs there with npm, using `overrides` to point the platform
//     package at its tarball,
//  4. runs `escalier --version` and `escalier build .` with
//     ESCALIER_STDLIB_DIR and ESCALIER_BINARY_PATH unset, and compares the
//     build/ directory with the one fixtures/npm_e2e commits,
//  5. builds the source of fixtures/npm_e2e_type_error and compares its
//     stderr with the fixture's error.txt.
//
// The fixture tests in cmd/escalier keep both fixtures' expected output in
// step with the compiler. The script needs Go, pnpm and npm on PATH and the
// workspace's dependencies installed.

import { spawnSync } from 'node:child_process';
import {
    cpSync,
    mkdirSync,
    mkdtempSync,
    readdirSync,
    readFileSync,
    rmSync,
    writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import { TARGETS, platformPackageName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
const CLI_DIR = join(ROOT, 'packages', 'cli');
const PROJECT_FIXTURE = join(ROOT, 'fixtures', 'npm_e2e');
const TYPE_ERROR_FIXTURE = join(ROOT, 'fixtures', 'npm_e2e_type_error');

// Runs `command` with `args` and returns its stdout, stderr and exit
// status. Throws when the command can't be started.
function run(command, args, options) {
    const result = spawnSync(command, args, { encoding: 'utf8', ...options });
    if (result.error !== undefined) {
        throw new Error(`failed to run ${command}: ${result.error.message}`);
    }
    return result;
}

// Runs `command` with `args`, and throws with the command's output when it
// exits with a status other than `expected`.
function runExpecting(expected, command, args, options) {
    const result = run(command, args, options);
    if (result.status !== expected) {
        throw new Error(
            [
                `${command} ${args.join(' ')} exited with status ${result.status}, expected ${expected}`,
                result.stdout,
                result.stderr,
            ].join('\n'),
        );
    }
    return result;
}

// Returns the paths of the files under `dir`, relative to `dir`, with `/`
// separators, in sorted order.
function listFiles(dir) {
    return readdirSync(dir, { recursive: true, withFileTypes: true })
        .filter((entry) => entry.isFile())
        .map((entry) =>
            relative(dir, join(entry.parentPath, entry.name))
                .split(sep)
                .join('/'),
        )
        .sort();
}

// Packs the workspace package in `packageDir` into `destDir` and returns
// the tarball's path.
function pack(packageDir, destDir) {
    mkdirSync(destDir, { recursive: true });
    runExpecting(0, 'pnpm', ['pack', '--pack-destination', destDir], {
        cwd: packageDir,
    });
    const tarballs = readdirSync(destDir).filter((name) =>
        name.endsWith('.tgz'),
    );
    if (tarballs.length !== 1) {
        throw new Error(
            `expected one tarball in ${destDir}, found ${tarballs.length}`,
        );
    }
    return join(destDir, tarballs[0]);
}

// Throws when `actual` differs from `expected`, naming `what` and showing
// both texts.
function assertSame(what, expected, actual) {
    if (actual !== expected) {
        throw new Error(
            `${what} differs from the expected text.\n--- expected\n${expected}\n--- actual\n${actual}`,
        );
    }
}

// Throws when the files under `actualDir` differ from those under
// `expectedDir`, in name or in content.
function assertSameTree(expectedDir, actualDir) {
    const expected = listFiles(expectedDir);
    assertSame(
        `the list of files in ${actualDir}`,
        expected.join('\n'),
        listFiles(actualDir).join('\n'),
    );
    for (const file of expected) {
        assertSame(
            join(actualDir, file),
            readFileSync(join(expectedDir, file), 'utf8'),
            readFileSync(join(actualDir, file), 'utf8'),
        );
    }
}

// Runs the checks in the temporary directory `tmp` with the binary for
// `target`, an entry of TARGETS. Throws on the first check that fails.
function runChecks(target, tmp) {
    console.log(`building the ${target.suffix} binary`);
    runExpecting(
        0,
        process.execPath,
        [join(ROOT, 'tools', 'build-binaries.mjs'), '--only', target.suffix],
        { cwd: ROOT, stdio: 'inherit' },
    );

    console.log('packing @escalier/cli and its platform package');
    const cliTarball = pack(CLI_DIR, join(tmp, 'packs', 'cli'));
    const platformTarball = pack(
        join(ROOT, 'packages', 'platforms', `cli-${target.suffix}`),
        join(tmp, 'packs', 'platform'),
    );

    // The copy leaves out build/, so the comparison below sees only what
    // the installed binary writes.
    const projectDir = join(tmp, 'project');
    cpSync(PROJECT_FIXTURE, projectDir, {
        recursive: true,
        filter: (src) => src !== join(PROJECT_FIXTURE, 'build'),
    });
    const manifestPath = join(projectDir, 'package.json');
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
    manifest.devDependencies = { '@escalier/cli': `file:${cliTarball}` };
    manifest.overrides = {
        [platformPackageName(target.suffix)]: `file:${platformTarball}`,
    };
    writeFileSync(manifestPath, JSON.stringify(manifest, null, 4) + '\n');

    console.log('installing the tarballs with npm');
    runExpecting(0, 'npm', ['install', '--no-audit', '--no-fund'], {
        cwd: projectDir,
        stdio: 'inherit',
    });

    // The binary has to find the stdlib through what the packages install,
    // so neither variable may point it elsewhere.
    const env = { ...process.env };
    delete env.ESCALIER_STDLIB_DIR;
    delete env.ESCALIER_BINARY_PATH;
    const npxOptions = { cwd: projectDir, env };

    const { version } = JSON.parse(
        readFileSync(join(CLI_DIR, 'package.json'), 'utf8'),
    );
    const versionResult = runExpecting(
        0,
        'npx',
        ['escalier', '--version'],
        npxOptions,
    );
    assertSame('escalier --version', `${version}\n`, versionResult.stdout);
    console.log(`escalier --version printed ${version}`);

    runExpecting(0, 'npx', ['escalier', 'build', '.'], npxOptions);
    assertSameTree(join(PROJECT_FIXTURE, 'build'), join(projectDir, 'build'));
    console.log('escalier build . wrote the expected output');

    // `escalier build broken` compiles only broken/lib and prints paths
    // relative to broken/, the same paths the fixture's error.txt holds.
    // Running it from the project directory lets npx find the installed
    // `escalier` command.
    cpSync(join(TYPE_ERROR_FIXTURE, 'lib'), join(projectDir, 'broken', 'lib'), {
        recursive: true,
    });
    const broken = runExpecting(
        1,
        'npx',
        ['escalier', 'build', 'broken'],
        npxOptions,
    );
    assertSame(
        'the stderr of escalier build broken',
        readFileSync(join(TYPE_ERROR_FIXTURE, 'error.txt'), 'utf8'),
        broken.stderr,
    );
    console.log('escalier build broken reported the expected type error');
}

function main() {
    const target = TARGETS.find(
        (t) => t.os === process.platform && t.cpu === process.arch,
    );
    if (target === undefined) {
        throw new Error(
            `no target in tools/platforms.mjs for ${process.platform}-${process.arch}`,
        );
    }

    const tmp = mkdtempSync(join(tmpdir(), 'escalier-e2e-'));
    const fromRoot = relative(ROOT, tmp);
    if (!fromRoot.startsWith('..') && !isAbsolute(fromRoot)) {
        throw new Error(`${tmp} is inside the checkout at ${ROOT}`);
    }
    console.log(`working in ${tmp}`);

    try {
        runChecks(target, tmp);
    } finally {
        rmSync(tmp, { recursive: true, force: true });
    }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    try {
        main();
    } catch (err) {
        console.error(err.message);
        process.exit(1);
    }
}
