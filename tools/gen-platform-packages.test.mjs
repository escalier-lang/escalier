import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import { platformManifest, platformReadme } from './gen-platform-packages.mjs';
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
                prepublishOnly: `node -e "if (!require('fs').existsSync('bin/escalier')) { console.error('bin/escalier is missing. Run node tools/build-binaries.mjs first.'); process.exit(1); }"`,
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
