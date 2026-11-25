#!/usr/bin/env node
//
// Creates the GitHub release for each published package version that
// doesn't have one yet.
//
//     node tools/create-github-releases.mjs
//
// The release workflow runs it after changesets/action takes its publish
// path. The script reads each public workspace package's version, and
// creates a release only for a version that is published and has no GitHub
// release yet. A version counts as published when PUBLISHED_PACKAGES, the
// action's publishedPackages output, lists it, or else when `npm view`
// finds it on npm. Running it again creates only what is still missing, so
// re-running a release whose last step failed, or the next push to main,
// finishes the job. The @escalier/cli-<platform>-<arch> packages always
// share @escalier/cli's version, so they fold into one @escalier/cli
// release instead of one release each. That release carries the escalier
// binary for every target in tools/platforms.mjs, for people who don't
// install from npm. Every other package gets a release of its own.
//
// Each release gets the tag <name>@<version> on GITHUB_SHA and takes its
// notes from that version's section of the package's CHANGELOG.md, or a
// one-line note when the package has no CHANGELOG.md. The script computes
// every release's notes before it creates any release, so a bad changelog
// creates none. It runs `gh`, which reads its token from GH_TOKEN.

import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { TARGETS, binaryName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

export const CLI_PACKAGE = '@escalier/cli';
const PLATFORM_PREFIX = `${CLI_PACKAGE}-`;

// Returns the releases for `packages`, a list of `{ name, version }`, in
// the order each first appears. Each
// release is `{ name, version, tag }`. A @escalier/cli-<suffix> package
// joins the @escalier/cli release. Throws when two packages in one release
// have different versions.
export function groupReleases(packages) {
    const releases = new Map();
    for (const { name, version } of packages) {
        const releaseName = name.startsWith(PLATFORM_PREFIX)
            ? CLI_PACKAGE
            : name;
        const existing = releases.get(releaseName);
        if (existing === undefined) {
            releases.set(releaseName, {
                name: releaseName,
                version,
                tag: `${releaseName}@${version}`,
            });
        } else if (existing.version !== version) {
            throw new Error(
                `${name}@${version} doesn't match ${existing.tag}, which it releases with`,
            );
        }
    }
    return [...releases.values()];
}

// Returns the body of the `## <version>` section of `changelog`, the text
// of a CHANGELOG.md that `changeset version` writes, without its heading.
// Throws when the changelog has no section for `version`.
export function changelogEntry(changelog, version) {
    const lines = changelog.split('\n');
    const start = lines.findIndex((line) => line.trim() === `## ${version}`);
    if (start === -1) {
        throw new Error(`the changelog has no "## ${version}" section`);
    }
    const end = lines.findIndex(
        (line, i) => i > start && line.startsWith('## '),
    );
    return lines
        .slice(start + 1, end === -1 ? undefined : end)
        .join('\n')
        .trim();
}

// Returns the notes for the release `{ version, tag }`. `changelog` is the
// text of the package's CHANGELOG.md, or `undefined` when it has none,
// which happens when a package publishes without ever having had a
// changeset. Throws when a changelog exists but has no section for
// `version`.
export function releaseNotes(changelog, { version, tag }) {
    if (changelog === undefined) {
        return `${tag} has no changelog entry.`;
    }
    return changelogEntry(changelog, version);
}

// Returns the file name that the binary for `target` gets as an asset of
// the @escalier/cli release, such as `escalier-linux-x64` or
// `escalier-win32-x64.exe`.
export function assetName(target) {
    const extension = binaryName(target).endsWith('.exe') ? '.exe' : '';
    return `escalier-${target.suffix}${extension}`;
}

// Returns the `<name>@<version>` of each package in `json`, the
// publishedPackages output of changesets/action: a JSON array of
// `{ name, version }`. Returns an empty set when `json` is empty or
// undefined.
export function publishedTags(json) {
    if (!json) {
        return new Set();
    }
    return new Set(
        JSON.parse(json).map(({ name, version }) => `${name}@${version}`),
    );
}

// Returns the releases in `releases` to create: those whose version
// `isPublished(release)` reports as published and whose tag
// `hasRelease(release)` reports without a GitHub release.
export function pendingReleases(releases, { isPublished, hasRelease }) {
    return releases.filter(
        (release) => isPublished(release) && !hasRelease(release),
    );
}

// Returns each public workspace package under packages/ as
// `{ name, version, dir }`.
function workspacePackages() {
    const packages = [];
    const parents = [
        join(ROOT, 'packages'),
        join(ROOT, 'packages', 'platforms'),
    ];
    for (const parent of parents) {
        for (const entry of fs.readdirSync(parent)) {
            const manifestPath = join(parent, entry, 'package.json');
            if (!fs.existsSync(manifestPath)) {
                continue;
            }
            const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
            if (!manifest.private) {
                const { name, version } = manifest;
                packages.push({ name, version, dir: join(parent, entry) });
            }
        }
    }
    return packages;
}

// Runs `command` with `args` and returns its exit status and stdout.
// Throws when the command can't be started.
function run(command, args) {
    const result = spawnSync(command, args, { encoding: 'utf8' });
    if (result.error !== undefined) {
        throw new Error(`failed to run ${command}: ${result.error.message}`);
    }
    return { status: result.status, stdout: result.stdout.trim() };
}

// Copies each target's binary into `dir` under its asset name and returns
// the copies' paths. Throws when a binary is missing.
function copyAssets(dir) {
    return TARGETS.map((target) => {
        const binary = join(
            ROOT,
            'packages',
            'platforms',
            `cli-${target.suffix}`,
            'bin',
            binaryName(target),
        );
        const asset = join(dir, assetName(target));
        fs.copyFileSync(binary, asset);
        return asset;
    });
}

function main() {
    const sha = process.env.GITHUB_SHA;
    if (!sha) {
        throw new Error('GITHUB_SHA is not set');
    }
    const packages = workspacePackages();
    const dirs = new Map(packages.map(({ name, dir }) => [name, dir]));
    const justPublished = publishedTags(process.env.PUBLISHED_PACKAGES);
    const releases = pendingReleases(groupReleases(packages), {
        isPublished: ({ name, version, tag }) => {
            // npm's registry reads can lag a publish by minutes, so a
            // version this run published counts without asking npm. The
            // npm check covers a re-run, where PUBLISHED_PACKAGES lists
            // only what that run published.
            if (justPublished.has(tag)) {
                return true;
            }
            const result = run('npm', [
                'view',
                `${name}@${version}`,
                'version',
            ]);
            return result.status === 0 && result.stdout === version;
        },
        hasRelease: ({ tag }) =>
            run('gh', ['release', 'view', tag]).status === 0,
    });
    if (releases.length === 0) {
        console.log('every published version has a GitHub release');
        return;
    }

    // Every release's notes come first, so a changelog that lacks its
    // section stops the script before it creates any release.
    const notes = releases.map((release) => {
        const changelogPath = join(dirs.get(release.name), 'CHANGELOG.md');
        const changelog = fs.existsSync(changelogPath)
            ? fs.readFileSync(changelogPath, 'utf8')
            : undefined;
        return releaseNotes(changelog, release);
    });

    const workDir = fs.mkdtempSync(join(tmpdir(), 'escalier-releases-'));
    try {
        releases.forEach(({ name, version, tag }, i) => {
            const notesPath = join(workDir, 'notes.md');
            fs.writeFileSync(notesPath, notes[i]);
            const assets = name === CLI_PACKAGE ? copyAssets(workDir) : [];
            const args = ['release', 'create', tag, '--target', sha];
            args.push('--title', tag, '--notes-file', notesPath);
            if (version.includes('-')) {
                args.push('--prerelease');
            }
            console.log(`creating the ${tag} release`);
            const result = spawnSync('gh', [...args, ...assets], {
                stdio: 'inherit',
            });
            if (result.error !== undefined) {
                throw new Error(`failed to run gh: ${result.error.message}`);
            }
            if (result.status !== 0) {
                throw new Error(`gh release create ${tag} failed`);
            }
        });
    } finally {
        fs.rmSync(workDir, { recursive: true, force: true });
    }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    try {
        main();
    } catch (err) {
        console.error(err.message);
        process.exit(1);
    }
}
