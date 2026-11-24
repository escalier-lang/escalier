#!/usr/bin/env node
//
// Writes one npm package per entry in TARGETS under packages/platforms/.
//
//     node tools/gen-platform-packages.mjs
//
// Each package holds the escalier binary for one platform. The script
// writes package.json and README.md and leaves bin/ alone, since the
// cross-compile script fills bin/ at release time. It copies `version`,
// `license` and the repository links from packages/cli/package.json, so
// the platform packages always match `@escalier/cli`. It removes any
// `cli-*` directory whose target is no longer in TARGETS.
//
// It also sets the `optionalDependencies` of packages/cli/package.json to
// the platform packages. @escalier/cli reads that list at run time to know
// which platforms it supports.
//
// CI runs the script and fails when it changes a committed file, so edit
// tools/platforms.mjs and regenerate instead of editing the output.

import {
    existsSync,
    mkdirSync,
    readdirSync,
    readFileSync,
    rmSync,
    writeFileSync,
} from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { TARGETS, binaryName, platformPackageName } from './platforms.mjs';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');
const PLATFORMS_DIR = join(ROOT, 'packages', 'platforms');

// Returns the package.json object for `target`, taking the fields it
// shares with `@escalier/cli` from `cli`, the parsed packages/cli/package.json.
export function platformManifest(target, cli) {
    const name = platformPackageName(target.suffix);
    const binary = `bin/${binaryName(target)}`;
    return {
        name,
        version: cli.version,
        description: `The escalier compiler binary for ${target.label}. Install @escalier/cli instead of depending on this package.`,
        license: cli.license,
        author: cli.author,
        repository: {
            ...cli.repository,
            directory: `packages/platforms/cli-${target.suffix}`,
        },
        homepage: cli.homepage,
        bugs: cli.bugs,
        os: [target.os],
        cpu: [target.cpu],
        files: ['bin/'],
        preferUnplugged: true,
        scripts: {
            // bin/ is gitignored and filled by the cross-compile script, so
            // this stops a publish that would ship the package without its
            // binary. npm never lets a published version be replaced.
            prepublishOnly: `node -e "if (!require('fs').existsSync('${binary}')) { console.error('${binary} is missing. Build the platform binaries before publishing.'); process.exit(1); }"`,
        },
        publishConfig: {
            access: 'public',
            provenance: true,
        },
    };
}

// Returns the `optionalDependencies` object that makes `@escalier/cli`
// depend on the package for each entry in `targets`, with the keys sorted
// the way pnpm sorts them.
export function cliOptionalDependencies(targets) {
    const names = targets
        .map((target) => platformPackageName(target.suffix))
        .sort();
    return Object.fromEntries(names.map((name) => [name, 'workspace:*']));
}

// Returns the README.md text for `target`.
export function platformReadme(target) {
    const name = platformPackageName(target.suffix);
    return `# ${name}

The \`${binaryName(target)}\` binary of the [Escalier](https://github.com/escalier-lang/escalier) compiler for ${target.label}.

Install [\`@escalier/cli\`](https://www.npmjs.com/package/@escalier/cli) instead of this package. It depends on the package for each supported platform, and npm installs only the one that matches your machine.
`;
}

// Writes package.json and README.md for each entry in `targets` into
// `platformsDir`/cli-<suffix>, and removes every other `cli-*` directory in
// `platformsDir`. Files the function doesn't write, such as a target's bin/,
// are left in place. Returns the names of the removed directories.
export function writePlatformPackages(platformsDir, targets, cli) {
    mkdirSync(platformsDir, { recursive: true });

    const expected = new Set(targets.map((target) => `cli-${target.suffix}`));
    const removed = [];
    for (const entry of readdirSync(platformsDir)) {
        if (entry.startsWith('cli-') && !expected.has(entry)) {
            rmSync(join(platformsDir, entry), { recursive: true });
            removed.push(entry);
        }
    }

    for (const target of targets) {
        const dir = join(platformsDir, `cli-${target.suffix}`);
        if (!existsSync(dir)) {
            mkdirSync(dir);
        }
        const manifest = platformManifest(target, cli);
        writeFileSync(
            join(dir, 'package.json'),
            JSON.stringify(manifest, null, 4) + '\n',
        );
        writeFileSync(join(dir, 'README.md'), platformReadme(target));
    }
    return removed;
}

function main() {
    const cliPath = join(ROOT, 'packages', 'cli', 'package.json');
    const cli = JSON.parse(readFileSync(cliPath, 'utf8'));
    for (const entry of writePlatformPackages(PLATFORMS_DIR, TARGETS, cli)) {
        console.log(`removed packages/platforms/${entry}`);
    }
    console.log(`wrote ${TARGETS.length} packages under packages/platforms`);

    cli.optionalDependencies = cliOptionalDependencies(TARGETS);
    writeFileSync(cliPath, JSON.stringify(cli, null, 2) + '\n');
    console.log('wrote the optionalDependencies of packages/cli/package.json');
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
