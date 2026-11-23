#!/usr/bin/env node
//
// Fails when a PR changes a published package without a changeset that
// releases it.
//
//     check-changesets.mjs <base-ref>
//
// The script lists the files the PR changes since <base-ref> and works out
// which published packages they belong to:
//
//   - a file inside a non-private workspace package belongs to that package,
//   - compiler source that builds into the escalier binary belongs to
//     @escalier/cli, although it lives outside every package.json.
//
// Each of those packages must appear in the releases that
// `changeset status --output` plans. `changeset status` on its own only fails
// when a PR has no changesets at all, so it misses a PR that changes two
// packages and releases one. An empty changeset (`pnpm changeset --empty`)
// passes, for changes that shouldn't release.

import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

// The package whose binary the compiler sources build.
export const CLI_PACKAGE = '@escalier/cli';

// Returns true when `path`, relative to the repo root, is source code that
// ends up in the escalier binary. Tests, test data and snapshots don't, and
// cmd/lsp-server ships in the VS Code extension instead.
export function isCliSource(path) {
    if (
        path.endsWith('_test.go') ||
        path.includes('/testdata/') ||
        path.includes('/__snapshots__/')
    ) {
        return false;
    }
    return (
        path === 'go.mod' ||
        path === 'go.sum' ||
        path.startsWith('cmd/escalier/') ||
        path.startsWith('internal/')
    );
}

// Returns the published package that `path` belongs to, or `undefined`.
// `packages` holds `{ name, dir }` for each non-private workspace package,
// with `dir` relative to the repo root and `/`-separated.
export function owningPackage(path, packages) {
    if (isCliSource(path)) {
        return CLI_PACKAGE;
    }
    let owner;
    for (const pkg of packages) {
        const inside = path.startsWith(`${pkg.dir}/`);
        if (
            inside &&
            (owner === undefined || pkg.dir.length > owner.dir.length)
        ) {
            owner = pkg;
        }
    }
    return owner?.name;
}

// Returns a map from each published package that `files` change to the
// files that change it, in the order the files appear.
export function changedPackages(files, packages) {
    const byPackage = new Map();
    for (const file of files) {
        const name = owningPackage(file, packages);
        if (name === undefined) {
            continue;
        }
        if (!byPackage.has(name)) {
            byPackage.set(name, []);
        }
        byPackage.get(name).push(file);
    }
    return byPackage;
}

// Returns the names in `required` that `status`, the JSON that
// `changeset status --output` writes, doesn't release. An empty changeset
// covers every package.
export function unreleasedPackages(required, status) {
    if (
        status.changesets.some((changeset) => changeset.releases.length === 0)
    ) {
        return [];
    }
    const released = new Set(
        status.releases
            .filter((release) => release.type !== 'none')
            .map((release) => release.name),
    );
    return required.filter((name) => !released.has(name));
}

// Returns the error report for the packages in `missing`, each listed with
// the files from `byPackage` that change it.
export function formatReport(missing, byPackage) {
    const lines = ['These packages changed, but no changeset releases them:'];
    for (const name of missing) {
        lines.push(`  ${name}`);
        for (const file of byPackage.get(name)) {
            lines.push(`    ${file}`);
        }
    }
    lines.push(
        "Run `pnpm changeset` and select each package, or run `pnpm changeset --empty` if the changes shouldn't release.",
    );
    return lines.join('\n');
}

function changedFiles(baseRef) {
    const output = execFileSync(
        'git',
        ['diff', '--name-only', `${baseRef}...HEAD`],
        { encoding: 'utf8' },
    );
    return output.split('\n').filter((line) => line !== '');
}

function publishedPackages() {
    const output = execFileSync(
        'pnpm',
        ['ls', '--recursive', '--depth', '-1', '--json'],
        { encoding: 'utf8' },
    );
    return JSON.parse(output)
        .filter((pkg) => !pkg.private)
        .map((pkg) => ({
            name: pkg.name,
            dir: relative(process.cwd(), pkg.path).split(sep).join('/'),
        }));
}

// `changeset status` exits before writing --output when packages changed
// and the PR has no changesets, so a missing file means no changesets.
function changesetStatus(baseRef) {
    const dir = mkdtempSync(join(tmpdir(), 'changeset-status-'));
    const outPath = join(dir, 'status.json');
    try {
        spawnSync(
            'pnpm',
            [
                'exec',
                'changeset',
                'status',
                `--since=${baseRef}`,
                `--output=${outPath}`,
            ],
            { stdio: 'ignore' },
        );
        if (!existsSync(outPath)) {
            return { changesets: [], releases: [] };
        }
        return JSON.parse(readFileSync(outPath, 'utf8'));
    } finally {
        rmSync(dir, { recursive: true, force: true });
    }
}

function main() {
    const baseRef = process.argv[2];
    if (baseRef === undefined) {
        console.error('usage: check-changesets.mjs <base-ref>');
        process.exit(2);
    }
    const byPackage = changedPackages(
        changedFiles(baseRef),
        publishedPackages(),
    );
    if (byPackage.size === 0) {
        console.log(
            'No published package changed, so no changeset is required.',
        );
        return;
    }
    const missing = unreleasedPackages(
        [...byPackage.keys()],
        changesetStatus(baseRef),
    );
    if (missing.length === 0) {
        console.log(
            `Every changed package has a changeset: ${[...byPackage.keys()].join(', ')}.`,
        );
        return;
    }
    console.error(formatReport(missing, byPackage));
    process.exit(1);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
