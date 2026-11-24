import { describe, expect, test } from 'vitest';

import {
    type Env,
    type RunDeps,
    type SpawnOutcome,
    binaryEnv,
    runEscalier,
} from './run.js';

// Returns fake dependencies that record what runEscalier does with them.
function fakeDeps(
    outcome: SpawnOutcome,
    binaryPath = '/bin/escalier',
    env: Env = {},
) {
    const calls = {
        spawned: [] as [string, string[], Env][],
        killed: [] as NodeJS.Signals[],
        errors: [] as string[],
    };
    const deps: RunDeps = {
        env,
        getBinaryPath: () => binaryPath,
        getStdlibDir: () => '/cli/data',
        spawn: (path, args, spawnEnv) => {
            calls.spawned.push([path, args, spawnEnv]);
            return outcome;
        },
        kill: (signal) => calls.killed.push(signal),
        printError: (message) => calls.errors.push(message),
    };
    return { deps, calls };
}

describe('runEscalier', () => {
    test('passes the arguments through and returns the exit status', () => {
        const { deps, calls } = fakeDeps({ status: 7, signal: null });
        expect(runEscalier(['build', '--watch'], deps)).toBe(7);
        expect(calls.spawned).toEqual([
            [
                '/bin/escalier',
                ['build', '--watch'],
                { ESCALIER_STDLIB_DIR: '/cli/data' },
            ],
        ]);
        expect(calls.killed).toEqual([]);
        expect(calls.errors).toEqual([]);
    });

    test('runs the binary with the environment from binaryEnv', () => {
        const { deps, calls } = fakeDeps(
            { status: 0, signal: null },
            '/bin/escalier',
            { HOME: '/home/me', ESCALIER_STDLIB_DIR: '/opt/stdlib' },
        );
        expect(runEscalier([], deps)).toBe(0);
        expect(calls.spawned).toEqual([
            [
                '/bin/escalier',
                [],
                { HOME: '/home/me', ESCALIER_STDLIB_DIR: '/opt/stdlib' },
            ],
        ]);
    });

    test.each([
        ['SIGTERM', 143],
        ['SIGPIPE', 141],
        ['SIGINT', 130],
    ] as const)(
        're-raises %s and returns %i when the binary dies from it',
        (signal, expected) => {
            const { deps, calls } = fakeDeps({ status: null, signal });
            expect(runEscalier([], deps)).toBe(expected);
            expect(calls.killed).toEqual([signal]);
        },
    );

    test('reports a binary that fails to start', () => {
        const { deps, calls } = fakeDeps({
            status: null,
            signal: null,
            error: new Error('spawnSync /bin/escalier ENOENT'),
        });
        expect(runEscalier([], deps)).toBe(1);
        expect(calls.errors).toEqual([
            'failed to run /bin/escalier: spawnSync /bin/escalier ENOENT',
        ]);
    });

    test('reports why there is no binary to run', () => {
        const { deps, calls } = fakeDeps({ status: 0, signal: null });
        deps.getBinaryPath = () => {
            throw new Error(
                '@escalier/cli has no escalier binary for freebsd-x64.',
            );
        };
        expect(runEscalier([], deps)).toBe(1);
        expect(calls.spawned).toEqual([]);
        expect(calls.errors).toEqual([
            '@escalier/cli has no escalier binary for freebsd-x64.',
        ]);
    });
});

describe('binaryEnv', () => {
    test.each([
        [
            'adds ESCALIER_STDLIB_DIR when it is unset',
            { HOME: '/home/me' },
            { HOME: '/home/me', ESCALIER_STDLIB_DIR: '/cli/data' },
        ],
        [
            'replaces an empty ESCALIER_STDLIB_DIR',
            { ESCALIER_STDLIB_DIR: '' },
            { ESCALIER_STDLIB_DIR: '/cli/data' },
        ],
        [
            'keeps the ESCALIER_STDLIB_DIR the user set',
            { ESCALIER_STDLIB_DIR: '/opt/stdlib' },
            { ESCALIER_STDLIB_DIR: '/opt/stdlib' },
        ],
        [
            'leaves the stdlib lookup to a binary from ESCALIER_BINARY_PATH',
            { ESCALIER_BINARY_PATH: '/src/escalier/bin/escalier' },
            { ESCALIER_BINARY_PATH: '/src/escalier/bin/escalier' },
        ],
        [
            'adds ESCALIER_STDLIB_DIR when ESCALIER_BINARY_PATH is empty',
            { ESCALIER_BINARY_PATH: '' },
            { ESCALIER_BINARY_PATH: '', ESCALIER_STDLIB_DIR: '/cli/data' },
        ],
    ])('%s', (_name, env: Env, expected: Env) => {
        expect(binaryEnv(env, '/cli/data')).toEqual(expected);
    });
});
