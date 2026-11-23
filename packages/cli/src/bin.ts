#!/usr/bin/env node
//
// The `escalier` command. Runs the escalier binary from the installed
// platform package with this process's arguments and stdio, then exits the
// way the binary did.

import { spawnSync } from 'node:child_process';
import { constants } from 'node:os';

import { getBinaryPath } from './index.js';

let binaryPath: string;
try {
    binaryPath = getBinaryPath();
} catch (err) {
    console.error((err as Error).message);
    process.exit(1);
}

const result = spawnSync(binaryPath, process.argv.slice(2), {
    stdio: 'inherit',
});
if (result.error !== undefined) {
    console.error(`failed to run ${binaryPath}: ${result.error.message}`);
    process.exit(1);
}
// A binary killed by a signal has no exit status. Sending the same signal to
// this process makes the shell see the same cause of death. Node survives
// some signals, such as SIGPIPE, which it ignores, and SIGUSR1, which starts
// its inspector. For those, exit with 128 plus the signal number, the status
// a shell reports for a process the signal killed.
if (result.signal !== null) {
    process.kill(process.pid, result.signal);
    process.exit(128 + constants.signals[result.signal]);
}
process.exit(result.status ?? 1);
