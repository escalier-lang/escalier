#!/usr/bin/env node
//
// Cross-compiles the escalier binary for every target in tools/platforms.mjs.
//
//     node tools/build-binaries.mjs [--only <suffix>]...
//
// Each binary goes into packages/platforms/cli-<suffix>/bin/, where the
// @escalier/cli-<suffix> package publishes it. That path is gitignored.
//
// Every build sets CGO_ENABLED=0, so a single Linux machine can build all
// targets and the Linux binaries are statically linked. -ldflags sets
// buildinfo.Version to the version in packages/cli/package.json, which is
// what `escalier --version` prints.
//
// --only limits the build to the named target suffixes, such as
// `linux-x64`. It can be repeated.

import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs as parseArgv } from 'node:util';

import { TARGETS, binaryName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

// The Go package that builds the escalier binary.
export const ESCALIER_PKG = './cmd/escalier';

// The Go import path of the variable that `--version` prints.
export const VERSION_VARIABLE =
    'github.com/escalier-lang/escalier/internal/buildinfo.Version';

// Returns the directory, relative to the repo root, that receives the
// binary for `target`.
export function binaryDir(target) {
    return join('packages', 'platforms', `cli-${target.suffix}`, 'bin');
}

// Returns the target suffixes that `argv` selects with --only. An empty
// list means every target. Throws on an unknown flag, a missing value, a
// positional argument, or a suffix that isn't in TARGETS.
export function parseArgs(argv) {
    const { values } = parseArgv({
        args: argv,
        options: { only: { type: 'string', multiple: true } },
        strict: true,
        allowPositionals: false,
    });
    const only = values.only ?? [];
    const suffixes = TARGETS.map((target) => target.suffix);
    for (const value of only) {
        if (!suffixes.includes(value)) {
            throw new Error(
                `unknown target ${value}, expected one of: ${suffixes.join(', ')}`,
            );
        }
    }
    return only;
}

// Returns the targets in TARGETS order that `only`, the result of parseArgs,
// selects.
export function selectTargets(only) {
    return TARGETS.filter(
        (target) => only.length === 0 || only.includes(target.suffix),
    );
}

// Returns the environment variables that point `go build` at `target`.
// They override the caller's environment.
export function goEnv(target) {
    const env = {
        GOOS: target.goos,
        GOARCH: target.goarch,
        CGO_ENABLED: '0',
        // GOAMD64 and GOARM64 set the oldest CPU the binary runs on. `v1` is
        // the original x86-64 instruction set and `v8.0` is baseline
        // ARMv8-A, so every x64 and arm64 CPU can run the binary. Higher
        // levels let the compiler emit newer instructions: GOAMD64=v3 uses
        // AVX2, and its binary crashes with "illegal instruction" on CPUs
        // that lack it. Go defaults to these values, but `go build` also
        // reads both from the shell and from `go env -w`, so a builder with
        // GOAMD64=v3 set would publish binaries that fail on older machines.
        GOAMD64: 'v1',
        GOARM64: 'v8.0',
        // Replaces any GOFLAGS from the caller, so a flag such as -race
        // can't leak into a release build.
        GOFLAGS: '-mod=readonly',
    };
    if (target.goarm !== undefined) {
        env.GOARM = target.goarm;
    }
    return env;
}

// Returns the `go` arguments that build the escalier binary into `outPath`
// with its version set to `version`.
export function goBuildArgs(outPath, version) {
    return [
        'build',
        '-trimpath',
        '-ldflags',
        `-s -w -X ${VERSION_VARIABLE}=${version}`,
        '-o',
        outPath,
        ESCALIER_PKG,
    ];
}

function main() {
    let only;
    try {
        only = parseArgs(process.argv.slice(2));
    } catch (err) {
        console.error(err.message);
        process.exit(2);
    }
    const cli = JSON.parse(
        readFileSync(join(ROOT, 'packages', 'cli', 'package.json'), 'utf8'),
    );

    for (const target of selectTargets(only)) {
        const outDir = join(ROOT, binaryDir(target));
        const outPath = join(outDir, binaryName(target));
        mkdirSync(outDir, { recursive: true });
        console.log(`building ${relative(ROOT, outPath)}`);
        const result = spawnSync('go', goBuildArgs(outPath, cli.version), {
            cwd: ROOT,
            env: { ...process.env, ...goEnv(target) },
            stdio: 'inherit',
        });
        if (result.error !== undefined) {
            console.error(`failed to run go: ${result.error.message}`);
            process.exit(1);
        }
        if (result.status !== 0) {
            const cause =
                result.signal === null
                    ? `exit status ${result.status}`
                    : `signal ${result.signal}`;
            console.error(
                `go build failed for escalier on ${target.suffix} (${cause})`,
            );
            process.exit(result.status ?? 1);
        }
        chmodSync(outPath, 0o755);
    }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
