#!/usr/bin/env node
//
// Creates the GitHub releases for the packages that a release published.
//
//     PUBLISHED_PACKAGES='[{"name":"@escalier/cli","version":"0.1.0"}]' \
//         node tools/create-github-releases.mjs
//
// The release workflow runs it after changesets/action publishes, with
// PUBLISHED_PACKAGES set to the action's publishedPackages output. The
// @escalier/cli-<platform>-<arch> packages always share @escalier/cli's
// version, so they fold into one @escalier/cli release instead of one
// release each. That release carries the escalier binary for every target
// in tools/platforms.mjs, for people who don't install from npm. Every other
// package gets a release of its own.
//
// Each release gets the tag <name>@<version> on GITHUB_SHA and takes its
// notes from that version's section of the package's CHANGELOG.md, or a
// one-line note when the package has no CHANGELOG.md. The
// script runs `gh release create`, which reads its token from GH_TOKEN.

import { spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { TARGETS, binaryName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

export const CLI_PACKAGE = '@escalier/cli';
const PLATFORM_PREFIX = `${CLI_PACKAGE}-`;

// Returns the releases for `published`, the list of `{ name, version }`
// that changesets/action reports, in the order each first appears. Each
// release is `{ name, version, tag }`. A @escalier/cli-<suffix> package
// joins the @escalier/cli release. Throws when two packages in one release
// have different versions.
export function groupReleases(published) {
    const releases = new Map();
    for (const { name, version } of published) {
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

// Returns the directory of each workspace package under packages/, keyed
// by package name.
function packageDirs() {
    const dirs = new Map();
    const parents = [
        join(ROOT, 'packages'),
        join(ROOT, 'packages', 'platforms'),
    ];
    for (const parent of parents) {
        for (const entry of fs.readdirSync(parent)) {
            const manifestPath = join(parent, entry, 'package.json');
            if (fs.existsSync(manifestPath)) {
                const { name } = JSON.parse(
                    fs.readFileSync(manifestPath, 'utf8'),
                );
                dirs.set(name, join(parent, entry));
            }
        }
    }
    return dirs;
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
    const releases = groupReleases(JSON.parse(process.env.PUBLISHED_PACKAGES));
    const dirs = packageDirs();
    const workDir = fs.mkdtempSync(join(tmpdir(), 'escalier-releases-'));
    try {
        for (const release of releases) {
            const { name, version, tag } = release;
            const dir = dirs.get(name);
            if (dir === undefined) {
                throw new Error(`no workspace package is named ${name}`);
            }
            const changelogPath = join(dir, 'CHANGELOG.md');
            const changelog = fs.existsSync(changelogPath)
                ? fs.readFileSync(changelogPath, 'utf8')
                : undefined;
            const notesPath = join(workDir, 'notes.md');
            fs.writeFileSync(notesPath, releaseNotes(changelog, release));

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
        }
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
