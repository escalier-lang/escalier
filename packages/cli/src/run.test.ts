import { describe, expect, test } from 'vitest';

import { type RunDeps, type SpawnOutcome, runEscalier } from './run.js';

// Returns fake dependencies that record what runEscalier does with them.
function fakeDeps(outcome: SpawnOutcome, binaryPath = '/bin/escalier') {
    const calls = {
        spawned: [] as [string, string[]][],
        killed: [] as NodeJS.Signals[],
        errors: [] as string[],
    };
    const deps: RunDeps = {
        getBinaryPath: () => binaryPath,
        spawn: (path, args) => {
            calls.spawned.push([path, args]);
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
            ['/bin/escalier', ['build', '--watch']],
        ]);
        expect(calls.killed).toEqual([]);
        expect(calls.errors).toEqual([]);
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
