#!/usr/bin/env node
//
// Copies the stdlib `.esc` files into the @escalier/cli package.
//
//     node tools/copy-stdlib.mjs
//
// The escalier binary reads the stdlib from disk at run time, and the
// platform packages ship only the binary. @escalier/cli publishes the copy
// in packages/cli/data/ and points the binary at it through
// ESCALIER_STDLIB_DIR. The `build` script of packages/cli runs this script,
// and the copy is gitignored.

import { cpSync, existsSync, rmSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

// The directory the compiler reads the stdlib from in a checkout, relative
// to the repo root.
export const STDLIB_SOURCE_DIR = join('internal', 'interop', 'data');

// The directory @escalier/cli publishes the stdlib from, relative to the
// repo root.
export const STDLIB_PACKAGE_DIR = join('packages', 'cli', 'data');

// Replaces `destDir` with a copy of `srcDir`. Throws when `srcDir` has no
// `std/` subdirectory, the one the binary requires of a stdlib directory.
export function copyStdlib(srcDir, destDir) {
    if (!existsSync(join(srcDir, 'std'))) {
        throw new Error(`${srcDir} has no std/ subdirectory`);
    }
    rmSync(destDir, { recursive: true, force: true });
    cpSync(srcDir, destDir, { recursive: true });
}

function main() {
    const destDir = join(ROOT, STDLIB_PACKAGE_DIR);
    try {
        copyStdlib(join(ROOT, STDLIB_SOURCE_DIR), destDir);
    } catch (err) {
        console.error(err.message);
        process.exit(1);
    }
    console.log(`copied ${STDLIB_SOURCE_DIR} to ${relative(ROOT, destDir)}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
