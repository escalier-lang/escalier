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
        publishConfig: {
            access: 'public',
            provenance: true,
        },
    };
}

// Returns the README.md text for `target`.
export function platformReadme(target) {
    const name = platformPackageName(target.suffix);
    return `# ${name}

The \`${binaryName(target)}\` binary of the [Escalier](https://github.com/escalier-lang/escalier) compiler for ${target.label}.

Install [\`@escalier/cli\`](https://www.npmjs.com/package/@escalier/cli) instead of this package. It depends on the package for each supported platform, and npm installs only the one that matches your machine.
`;
}

function main() {
    const cli = JSON.parse(
        readFileSync(join(ROOT, 'packages', 'cli', 'package.json'), 'utf8'),
    );
    mkdirSync(PLATFORMS_DIR, { recursive: true });

    const expected = new Set(TARGETS.map((target) => `cli-${target.suffix}`));
    for (const entry of readdirSync(PLATFORMS_DIR)) {
        if (entry.startsWith('cli-') && !expected.has(entry)) {
            rmSync(join(PLATFORMS_DIR, entry), { recursive: true });
            console.log(`removed packages/platforms/${entry}`);
        }
    }

    for (const target of TARGETS) {
        const dir = join(PLATFORMS_DIR, `cli-${target.suffix}`);
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
    console.log(`wrote ${TARGETS.length} packages under packages/platforms`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main();
}
