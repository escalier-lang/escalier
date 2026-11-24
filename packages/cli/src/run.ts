import { spawnSync } from 'node:child_process';
import { constants } from 'node:os';

import {
    BINARY_PATH_ENV,
    STDLIB_DIR_ENV,
    getBinaryPath,
    getStdlibDir,
} from './index.js';

// The outcome of running the binary, in the shape `spawnSync` returns it.
export interface SpawnOutcome {
    status: number | null;
    signal: NodeJS.Signals | null;
    error?: Error;
}

export type Env = Record<string, string | undefined>;

export interface RunDeps {
    // This process's environment variables.
    env: Env;
    // Returns the path of the binary to run. Throws when there is none.
    getBinaryPath: () => string;
    // Returns the stdlib directory this package ships.
    getStdlibDir: () => string;
    // Runs the binary with the given arguments and environment variables,
    // and this process's stdio.
    spawn: (binaryPath: string, args: string[], env: Env) => SpawnOutcome;
    // Sends `signal` to this process.
    kill: (signal: NodeJS.Signals) => void;
    // Writes a line to stderr.
    printError: (message: string) => void;
}

const defaultDeps: RunDeps = {
    env: process.env,
    getBinaryPath: () => getBinaryPath(),
    getStdlibDir,
    spawn: (binaryPath, args, env) =>
        spawnSync(binaryPath, args, { stdio: 'inherit', env }),
    kill: (signal) => process.kill(process.pid, signal),
    printError: (message) => console.error(message),
};

// Returns the environment variables to run the binary with. They are `env`
// with ESCALIER_STDLIB_DIR set to `stdlibDir`, unless `env` already sets
// ESCALIER_STDLIB_DIR or ESCALIER_BINARY_PATH to a non-empty value.
export function binaryEnv(env: Env, stdlibDir: string): Env {
    if (env[STDLIB_DIR_ENV] || env[BINARY_PATH_ENV]) {
        return env;
    }
    return { ...env, [STDLIB_DIR_ENV]: stdlibDir };
}

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

    // The binary in the platform package finds the stdlib only through
    // ESCALIER_STDLIB_DIR or `--stdlib-dir`, so the variable points it at
    // this package's copy. The variable also overrides every lookup the
    // binary does on its own. A binary from ESCALIER_BINARY_PATH keeps those
    // lookups, so one built in an escalier checkout reads that checkout's
    // stdlib.
    const env = binaryEnv(deps.env, deps.getStdlibDir());
    const result = deps.spawn(binaryPath, args, env);
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
