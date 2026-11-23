import { spawnSync } from 'node:child_process';
import { constants } from 'node:os';

import { getBinaryPath } from './index.js';

// The outcome of running the binary, in the shape `spawnSync` returns it.
export interface SpawnOutcome {
    status: number | null;
    signal: NodeJS.Signals | null;
    error?: Error;
}

export interface RunDeps {
    // Returns the path of the binary to run. Throws when there is none.
    getBinaryPath: () => string;
    // Runs the binary with the given arguments and this process's stdio.
    spawn: (binaryPath: string, args: string[]) => SpawnOutcome;
    // Sends `signal` to this process.
    kill: (signal: NodeJS.Signals) => void;
    // Writes a line to stderr.
    printError: (message: string) => void;
}

const defaultDeps: RunDeps = {
    getBinaryPath: () => getBinaryPath(),
    spawn: (binaryPath, args) =>
        spawnSync(binaryPath, args, { stdio: 'inherit' }),
    kill: (signal) => process.kill(process.pid, signal),
    printError: (message) => console.error(message),
};

// Runs the escalier binary with `args` and returns the exit status the
// `escalier` command should exit with: the binary's own status, or 1 when
// the binary can't be found or started.
//
// A binary killed by a signal has no exit status. `runEscalier` first sends
// the same signal to this process, so the shell sees the same cause of
// death. Node survives some signals, such as SIGPIPE, which it ignores, and
// SIGUSR1, which starts its inspector. For those it returns 128 plus the
// signal number, the status a shell reports for a process the signal killed.
export function runEscalier(
    args: string[],
    deps: RunDeps = defaultDeps,
): number {
    let binaryPath: string;
    try {
        binaryPath = deps.getBinaryPath();
    } catch (err) {
        deps.printError((err as Error).message);
        return 1;
    }

    const result = deps.spawn(binaryPath, args);
    if (result.error !== undefined) {
        deps.printError(`failed to run ${binaryPath}: ${result.error.message}`);
        return 1;
    }
    if (result.signal !== null) {
        deps.kill(result.signal);
        return 128 + constants.signals[result.signal];
    }
    return result.status ?? 1;
}
