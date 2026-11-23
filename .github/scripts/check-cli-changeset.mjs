#!/usr/bin/env node
//
// Fails when a PR changes the compiler but has no changeset for
// @escalier/cli.
//
//     check-cli-changeset.mjs <base-ref>
//
// `changeset status` only notices changes inside a package's directory.
// The Go sources that build the escalier binary live outside every
// package.json, so a compiler change alone never asks for a changeset.
// This script lists the files the PR changes since <base-ref> and, when any
// of them is compiler source, requires the planned releases to include
// @escalier/cli. An empty changeset (`pnpm changeset --empty`) also passes,
// for compiler changes that shouldn't release.

import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
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

// Returns true when `status`, the JSON that `changeset status --output`
// writes, plans a release of @escalier/cli or holds an empty changeset.
export function hasCliChangeset(status) {
    const releasesCli = status.releases.some(
        (release) => release.name === CLI_PACKAGE && release.type !== 'none',
    );
    const hasEmptyChangeset = status.changesets.some(
        (changeset) => changeset.releases.length === 0,
    );
    return releasesCli || hasEmptyChangeset;
}

// Returns the error report for a PR that changes `cliFiles` without a
// changeset for @escalier/cli.
export function formatReport(cliFiles) {
    return [
        `These changes build into the escalier binary, but no changeset releases ${CLI_PACKAGE}:`,
        ...cliFiles.map((file) => `  ${file}`),
        `Run \`pnpm changeset\` and select ${CLI_PACKAGE}, or run \`pnpm changeset --empty\` if the change shouldn't release.`,
    ].join('\n');
}

function changedFiles(baseRef) {
    const output = execFileSync(
        'git',
        ['diff', '--name-only', `${baseRef}...HEAD`],
        { encoding: 'utf8' },
    );
    return output.split('\n').filter((line) => line !== '');
}

function changesetStatus(baseRef) {
    const dir = mkdtempSync(join(tmpdir(), 'changeset-status-'));
    const outPath = join(dir, 'status.json');
    try {
        execFileSync(
            'pnpm',
            [
                'exec',
                'changeset',
                'status',
                `--since=${baseRef}`,
                `--output=${outPath}`,
            ],
            { stdio: ['ignore', 'ignore', 'inherit'] },
        );
        return JSON.parse(readFileSync(outPath, 'utf8'));
    } finally {
        rmSync(dir, { recursive: true, force: true });
    }
}

function main() {
    const baseRef = process.argv[2];
    if (baseRef === undefined) {
        console.error('usage: check-cli-changeset.mjs <base-ref>');
        process.exit(2);
    }
    const cliFiles = changedFiles(baseRef).filter(isCliSource);
    if (cliFiles.length === 0) {
        console.log('No compiler changes, so no changeset is required.');
        return;
    }
    if (hasCliChangeset(changesetStatus(baseRef))) {
        console.log(
            `The compiler changes have a changeset for ${CLI_PACKAGE}.`,
        );
        return;
    }
    console.error(formatReport(cliFiles));
    process.exit(1);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
