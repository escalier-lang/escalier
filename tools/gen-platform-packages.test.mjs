import assert from 'node:assert/strict';
import {
    existsSync,
    mkdirSync,
    mkdtempSync,
    readdirSync,
    rmSync,
    writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, test } from 'node:test';

import {
    cliOptionalDependencies,
    platformManifest,
    platformReadme,
    writePlatformPackages,
} from './gen-platform-packages.mjs';
import { TARGETS } from './platforms.mjs';

const cli = {
    name: '@escalier/cli',
    version: '1.2.3',
    license: 'MIT',
    author: 'escalier-lang',
    repository: {
        type: 'git',
        url: 'git+https://github.com/escalier-lang/escalier.git',
        directory: 'packages/cli',
    },
    homepage: 'https://github.com/escalier-lang/escalier#readme',
    bugs: { url: 'https://github.com/escalier-lang/escalier/issues' },
};

const linuxArm = TARGETS.find((target) => target.suffix === 'linux-arm');

describe('platformManifest', () => {
    test('copies the shared fields from @escalier/cli and sets os and cpu', () => {
        assert.deepEqual(platformManifest(linuxArm, cli), {
            name: '@escalier/cli-linux-arm',
            version: '1.2.3',
            description:
                'The escalier compiler binary for Linux on 32-bit ARMv7. Install @escalier/cli instead of depending on this package.',
            license: 'MIT',
            author: 'escalier-lang',
            repository: {
                type: 'git',
                url: 'git+https://github.com/escalier-lang/escalier.git',
                directory: 'packages/platforms/cli-linux-arm',
            },
            homepage: 'https://github.com/escalier-lang/escalier#readme',
            bugs: { url: 'https://github.com/escalier-lang/escalier/issues' },
            os: ['linux'],
            cpu: ['arm'],
            files: ['bin/'],
            preferUnplugged: true,
            scripts: {
                prepublishOnly: `node -e "if (!require('fs').existsSync('bin/escalier')) { console.error('bin/escalier is missing. Build the platform binaries before publishing.'); process.exit(1); }"`,
            },
            publishConfig: { access: 'public', provenance: true },
        });
    });
});

describe('platformReadme', () => {
    test('names the Windows binary with its .exe suffix', () => {
        const win = TARGETS.find((target) => target.suffix === 'win32-x64');
        assert.equal(
            platformReadme(win),
            `# @escalier/cli-win32-x64

The \`escalier.exe\` binary of the [Escalier](https://github.com/escalier-lang/escalier) compiler for Windows on x64.

Install [\`@escalier/cli\`](https://www.npmjs.com/package/@escalier/cli) instead of this package. It depends on the package for each supported platform, and npm installs only the one that matches your machine.
`,
        );
    });
});

describe('TARGETS', () => {
    test('uses Node platform and arch names in every suffix', () => {
        for (const target of TARGETS) {
            assert.equal(target.suffix, `${target.os}-${target.cpu}`);
        }
    });
});

describe('writePlatformPackages', () => {
    test('removes cli-* directories for dropped targets and keeps bin/ of current ones', (t) => {
        const dir = mkdtempSync(join(tmpdir(), 'platforms-'));
        t.after(() => rmSync(dir, { recursive: true }));

        // A package whose target was dropped, with a built binary in it.
        mkdirSync(join(dir, 'cli-freebsd-x64', 'bin'), { recursive: true });
        writeFileSync(join(dir, 'cli-freebsd-x64', 'bin', 'escalier'), '');
        // A current target with a built binary, which must survive.
        mkdirSync(join(dir, 'cli-linux-arm', 'bin'), { recursive: true });
        writeFileSync(join(dir, 'cli-linux-arm', 'bin', 'escalier'), '');
        // A directory that isn't a platform package, which must survive.
        mkdirSync(join(dir, 'notes'));

        const removed = writePlatformPackages(dir, [linuxArm], cli);

        assert.deepEqual(removed, ['cli-freebsd-x64']);
        assert.deepEqual(readdirSync(dir).sort(), ['cli-linux-arm', 'notes']);
        assert.deepEqual(readdirSync(join(dir, 'cli-linux-arm')).sort(), [
            'README.md',
            'bin',
            'package.json',
        ]);
        assert.ok(existsSync(join(dir, 'cli-linux-arm', 'bin', 'escalier')));
    });
});

describe('cliOptionalDependencies', () => {
    test('depends on every platform package, sorted by name', () => {
        assert.deepEqual(cliOptionalDependencies(TARGETS), {
            '@escalier/cli-darwin-arm64': 'workspace:*',
            '@escalier/cli-darwin-x64': 'workspace:*',
            '@escalier/cli-linux-arm': 'workspace:*',
            '@escalier/cli-linux-arm64': 'workspace:*',
            '@escalier/cli-linux-x64': 'workspace:*',
            '@escalier/cli-win32-arm64': 'workspace:*',
            '@escalier/cli-win32-x64': 'workspace:*',
        });
    });
});
