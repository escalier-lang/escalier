#!/usr/bin/env node
//
// The `escalier` command. Runs the escalier binary from the installed
// platform package with this process's arguments and stdio, then exits the
// way the binary did.

import { runEscalier } from './run.js';

process.exit(runEscalier(process.argv.slice(2)));
