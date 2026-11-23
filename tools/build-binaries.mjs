#!/usr/bin/env node
//
// Cross-compiles the escalier and lsp-server binaries for every target in
// tools/platforms.mjs.
//
//     node tools/build-binaries.mjs [--only <suffix>]... [--only-binary <name>]...
//
// The escalier binary goes into packages/platforms/cli-<suffix>/bin/, where
// the @escalier/cli-<suffix> package publishes it. The lsp-server binary
// goes into bin/<suffix>/, where the VS Code packaging script picks it up.
// Both paths are gitignored.
//
// Every build sets CGO_ENABLED=0, so a single Linux machine can build all
// targets and the Linux binaries are statically linked. -ldflags sets
// buildinfo.Version to the version in packages/cli/package.json, which is
// what `--version` prints.
//
// --only limits the build to the named target suffixes, such as
// `linux-x64`, and --only-binary to `escalier` or `lsp-server`. Each flag can
// be repeated.

import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { TARGETS, executableName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

// The Go import path of the variable that `--version` prints.
export const VERSION_VARIABLE =
    'github.com/escalier-lang/escalier/internal/buildinfo.Version';

// The binaries to build. `outDir` returns the directory, relative to the
// repo root, that receives the binary for `target`.
export const BINARIES = [
    {
        name: 'escalier',
        pkg: './cmd/escalier',
        outDir: (target) =>
            join('packages', 'platforms', `cli-${target.suffix}`, 'bin'),
    },
    {
        name: 'lsp-server',
        pkg: './cmd/lsp-server',
        outDir: (target) => join('bin', target.suffix),
    },
];

// Returns `{ only, onlyBinary }`, the target suffixes and binary names that
// `argv` selects. An empty list means every target or every binary. Throws
// on an unknown flag, a missing value, or a name that isn't in TARGETS or
// BINARIES.
export function parseArgs(argv) {
    const only = [];
    const onlyBinary = [];
    for (let i = 0; i < argv.length; i++) {
        const flag = argv[i];
        if (flag !== '--only' && flag !== '--only-binary') {
            throw new Error(`unknown argument: ${flag}`);
        }
        const value = argv[i + 1];
        if (value === undefined || value.startsWith('--')) {
            throw new Error(`${flag} needs a value`);
        }
        i++;
        if (flag === '--only') {
            if (!TARGETS.some((target) => target.suffix === value)) {
                const suffixes = TARGETS.map((target) => target.suffix);
                throw new Error(
                    `unknown target ${value}, expected one of: ${suffixes.join(', ')}`,
                );
            }
            only.push(value);
        } else {
            if (!BINARIES.some((binary) => binary.name === value)) {
                const names = BINARIES.map((binary) => binary.name);
                throw new Error(
                    `unknown binary ${value}, expected one of: ${names.join(', ')}`,
                );
            }
            onlyBinary.push(value);
        }
    }
    return { only, onlyBinary };
}

// Returns one `{ target, binary }` pair for each build that `selection`, the
// result of parseArgs, asks for, in TARGETS order.
export function selectBuilds(selection) {
    const targets = TARGETS.filter(
        (target) =>
            selection.only.length === 0 ||
            selection.only.includes(target.suffix),
    );
    const binaries = BINARIES.filter(
        (binary) =>
            selection.onlyBinary.length === 0 ||
            selection.onlyBinary.includes(binary.name),
    );
    return targets.flatMap((target) =>
        binaries.map((binary) => ({ target, binary })),
    );
}

// Returns the environment variables that point `go build` at `target`.
export function goEnv(target) {
    const env = {
        GOOS: target.goos,
        GOARCH: target.goarch,
        CGO_ENABLED: '0',
    };
    if (target.goarm !== undefined) {
        env.GOARM = target.goarm;
    }
    return env;
}

// Returns the `go` arguments that build `binary` into `outPath` with its
// version set to `version`.
export function goBuildArgs(binary, outPath, version) {
    return [
        'build',
        '-trimpath',
        '-ldflags',
        `-s -w -X ${VERSION_VARIABLE}=${version}`,
        '-o',
        outPath,
        binary.pkg,
    ];
}

function main() {
    let selection;
    try {
        selection = parseArgs(process.argv.slice(2));
    } catch (err) {
        console.error(err.message);
        process.exit(2);
    }
    const cli = JSON.parse(
        readFileSync(join(ROOT, 'packages', 'cli', 'package.json'), 'utf8'),
    );

    for (const { target, binary } of selectBuilds(selection)) {
        const outDir = join(ROOT, binary.outDir(target));
        const outPath = join(outDir, executableName(binary.name, target));
        mkdirSync(outDir, { recursive: true });
        console.log(`building ${relative(ROOT, outPath)}`);
        const result = spawnSync(
            'go',
            goBuildArgs(binary, outPath, cli.version),
            {
                cwd: ROOT,
                env: { ...process.env, ...goEnv(target) },
                stdio: 'inherit',
            },
        );
        if (result.status !== 0) {
            console.error(
                `go build failed for ${binary.name} on ${target.suffix}`,
            );
            process.exit(result.status ?? 1);
        }
        chmodSync(outPath, 0o755);
    }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
