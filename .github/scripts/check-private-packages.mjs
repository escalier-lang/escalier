#!/usr/bin/env node
//
// Fails when a workspace package that isn't meant for npm lacks
// `"private": true`.
//
//     check-private-packages.mjs
//
// `changeset publish` publishes every workspace package that isn't private
// and whose version isn't on npm yet. A fixture added under fixtures/
// without `"private": true` would therefore be published by the next
// release. The script lists the workspace with `pnpm ls` and reports each
// package that is neither private nor matched by `isPublished`.

import { execFileSync } from 'node:child_process';
import { relative } from 'node:path';
import { fileURLToPath } from 'node:url';

import { TARGETS, platformPackageName } from '../../tools/platforms.mjs';

// Packages released to npm. The platform packages, such as
// `@escalier/cli-linux-x64`, come from the target list in
// tools/platforms.mjs.
export const PUBLISHED_NAMES = [
    '@escalier/runtime',
    '@escalier/cli',
    ...TARGETS.map((target) => platformPackageName(target.suffix)),
];

// Returns true when a package with this name is released to npm.
export function isPublished(name) {
    return PUBLISHED_NAMES.includes(name);
}

// Returns the packages in `packages` that are neither private nor
// published. Each entry has the `name`, `path` and `private` fields that
// `pnpm ls --json` reports.
export function findUnmarkedPackages(packages) {
    return packages.filter((pkg) => !pkg.private && !isPublished(pkg.name));
}

// Returns the error report for `unmarked`, a non-empty result of
// findUnmarkedPackages. Paths are shown relative to `cwd`.
export function formatReport(unmarked, cwd) {
    const lines = [
        'These workspace packages are not published to npm but lack "private": true:',
    ];
    for (const pkg of unmarked) {
        const dir = relative(cwd, pkg.path) || '.';
        lines.push(`  ${pkg.name} (${dir}/package.json)`);
    }
    lines.push(
        'Add "private": true to each one, or add the package to PUBLISHED_NAMES ' +
            'in .github/scripts/check-private-packages.mjs if it should be published.',
    );
    return lines.join('\n');
}

function main() {
    const output = execFileSync(
        'pnpm',
        ['ls', '--recursive', '--depth', '-1', '--json'],
        { encoding: 'utf8' },
    );
    const unmarked = findUnmarkedPackages(JSON.parse(output));
    if (unmarked.length === 0) {
        console.log('Every unpublished workspace package is private.');
        return;
    }
    console.error(formatReport(unmarked, process.cwd()));
    process.exit(1);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
