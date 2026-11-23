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

// The kinds of file under cmd/escalier/ and internal/ that end up in the
// escalier binary or ship with it: Go source, the stdlib `.esc` files, and
// the `.json` files that internal/ecma262 embeds with go:embed.
export const CLI_SOURCE_EXTENSIONS = ['.go', '.esc', '.json'];

// Returns true when `path`, relative to the repo root, is source code that
// ends up in the escalier binary. Docs, tests, test data and snapshots
// don't, and cmd/lsp-server ships in the VS Code extension instead.
export function isCliSource(path) {
    if (path === 'go.mod' || path === 'go.sum') {
        return true;
    }
    if (!path.startsWith('cmd/escalier/') && !path.startsWith('internal/')) {
        return false;
    }
    if (
        path.endsWith('_test.go') ||
        path.includes('/testdata/') ||
        path.includes('/__snapshots__/')
    ) {
        return false;
    }
    return CLI_SOURCE_EXTENSIONS.some((extension) => path.endsWith(extension));
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

// Throws an Error that carries git's stderr when the diff fails, such as
// for a base ref that wasn't fetched.
function changedFiles(baseRef) {
    const run = spawnSync('git', ['diff', '--name-only', `${baseRef}...HEAD`], {
        encoding: 'utf8',
    });
    if (run.status !== 0) {
        throw new Error(
            `git diff against ${baseRef} failed:\n${(run.stderr ?? '').trimEnd()}`,
        );
    }
    return run.stdout.split('\n').filter((line) => line !== '');
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

// The message `changeset status` prints when packages changed and the PR
// has no changesets. It exits before writing --output in that case.
export const NO_CHANGESETS_ERROR =
    'Some packages have been changed but no changesets were found';

// Returns the status a `changeset status --output` run describes. `output`
// is the text of the --output file, or `undefined` when the run wrote none.
// A run that wrote no file counts as "no changesets" only when `stderr`
// holds NO_CHANGESETS_ERROR. Any other failure, such as a malformed config
// or an unknown base ref, throws an Error that carries `stderr`, so the
// report shows the real cause.
export function parseStatusRun({ output, exitCode, stderr }) {
    if (output !== undefined) {
        return JSON.parse(output);
    }
    if (stderr.includes(NO_CHANGESETS_ERROR)) {
        return { changesets: [], releases: [] };
    }
    throw new Error(
        `changeset status failed with exit code ${String(exitCode)}:\n${stderr.trimEnd()}`,
    );
}

function changesetStatus(baseRef) {
    const dir = mkdtempSync(join(tmpdir(), 'changeset-status-'));
    const outPath = join(dir, 'status.json');
    try {
        const run = spawnSync(
            'pnpm',
            [
                'exec',
                'changeset',
                'status',
                `--since=${baseRef}`,
                `--output=${outPath}`,
            ],
            { encoding: 'utf8', stdio: ['ignore', 'ignore', 'pipe'] },
        );
        return parseStatusRun({
            output: existsSync(outPath)
                ? readFileSync(outPath, 'utf8')
                : undefined,
            exitCode: run.status,
            stderr: run.stderr ?? '',
        });
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
    let files;
    try {
        files = changedFiles(baseRef);
    } catch (err) {
        console.error(err.message);
        process.exit(1);
    }
    const byPackage = changedPackages(files, publishedPackages());
    if (byPackage.size === 0) {
        console.log(
            'No published package changed, so no changeset is required.',
        );
        return;
    }
    let status;
    try {
        status = changesetStatus(baseRef);
    } catch (err) {
        console.error(err.message);
        process.exit(1);
    }
    const missing = unreleasedPackages([...byPackage.keys()], status);
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
