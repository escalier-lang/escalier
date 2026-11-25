import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import {
    archiveCommand,
    archiveName,
    archiveReadme,
    changelogEntry,
    groupReleases,
    pendingReleases,
    publishedTags,
    releaseNotes,
} from './create-github-releases.mjs';
import { TARGETS } from './platforms.mjs';

describe('groupReleases', () => {
    test('folds the platform packages into the @escalier/cli release', () => {
        assert.deepEqual(
            groupReleases([
                { name: '@escalier/cli-darwin-arm64', version: '0.1.0' },
                { name: '@escalier/cli', version: '0.1.0' },
                { name: '@escalier/cli-win32-x64', version: '0.1.0' },
                { name: '@escalier/runtime', version: '0.0.1' },
            ]),
            [
                {
                    name: '@escalier/cli',
                    version: '0.1.0',
                    tag: '@escalier/cli@0.1.0',
                },
                {
                    name: '@escalier/runtime',
                    version: '0.0.1',
                    tag: '@escalier/runtime@0.0.1',
                },
            ],
        );
    });

    test('returns no releases when nothing was published', () => {
        assert.deepEqual(groupReleases([]), []);
    });

    test('rejects a platform package whose version differs', () => {
        assert.throws(
            () =>
                groupReleases([
                    { name: '@escalier/cli', version: '0.2.0' },
                    { name: '@escalier/cli-linux-x64', version: '0.1.0' },
                ]),
            {
                message:
                    "@escalier/cli-linux-x64@0.1.0 doesn't match @escalier/cli@0.2.0, which it releases with",
            },
        );
    });
});

describe('pendingReleases', () => {
    const cli = {
        name: '@escalier/cli',
        version: '0.1.0',
        tag: '@escalier/cli@0.1.0',
    };
    const runtime = {
        name: '@escalier/runtime',
        version: '0.0.1',
        tag: '@escalier/runtime@0.0.1',
    };

    const cases = [
        {
            name: 'keeps a published version with no release',
            published: [cli.tag, runtime.tag],
            released: [],
            expected: [cli, runtime],
        },
        {
            name: 'skips a version that already has a release',
            published: [cli.tag, runtime.tag],
            released: [cli.tag],
            expected: [runtime],
        },
        {
            name: 'skips a version that is not on npm',
            published: [cli.tag],
            released: [],
            expected: [cli],
        },
        {
            name: 'returns nothing when every version has a release',
            published: [cli.tag, runtime.tag],
            released: [cli.tag, runtime.tag],
            expected: [],
        },
    ];
    for (const { name, published, released, expected } of cases) {
        test(name, () => {
            assert.deepEqual(
                pendingReleases([cli, runtime], {
                    isPublished: ({ tag }) => published.includes(tag),
                    hasRelease: ({ tag }) => released.includes(tag),
                }),
                expected,
            );
        });
    }
});

describe('changelogEntry', () => {
    const changelog = [
        '# @escalier/cli',
        '',
        '## 0.2.0',
        '',
        '### Minor Changes',
        '',
        '- abc1234: Add a flag.',
        '',
        '## 0.1.0',
        '',
        '### Patch Changes',
        '',
        '- def5678: Fix a bug.',
        '',
    ].join('\n');

    test('returns the section for the latest version', () => {
        assert.equal(
            changelogEntry(changelog, '0.2.0'),
            '### Minor Changes\n\n- abc1234: Add a flag.',
        );
    });

    test('returns the last section up to the end of the file', () => {
        assert.equal(
            changelogEntry(changelog, '0.1.0'),
            '### Patch Changes\n\n- def5678: Fix a bug.',
        );
    });

    test('rejects a version with no section', () => {
        assert.throws(() => changelogEntry(changelog, '0.3.0'), {
            message: 'the changelog has no "## 0.3.0" section',
        });
    });
});

describe('releaseNotes', () => {
    const release = { version: '0.1.0', tag: '@escalier/runtime@0.1.0' };

    test('takes the notes from the changelog', () => {
        assert.equal(
            releaseNotes(
                '# @escalier/runtime\n\n## 0.1.0\n\n- Fix it.\n',
                release,
            ),
            '- Fix it.',
        );
    });

    test('notes a package that has no changelog', () => {
        assert.equal(
            releaseNotes(undefined, release),
            '@escalier/runtime@0.1.0 has no changelog entry.',
        );
    });
});

describe('archiveName', () => {
    test('names each archive after its target', () => {
        assert.deepEqual(TARGETS.map(archiveName), [
            'escalier-darwin-arm64.tar.gz',
            'escalier-darwin-x64.tar.gz',
            'escalier-linux-x64.tar.gz',
            'escalier-linux-arm64.tar.gz',
            'escalier-linux-arm.tar.gz',
            'escalier-win32-x64.zip',
            'escalier-win32-arm64.zip',
        ]);
    });
});

describe('archiveCommand', () => {
    const target = (suffix) => TARGETS.find((t) => t.suffix === suffix);

    test('packs a Linux or macOS target with tar', () => {
        assert.deepEqual(archiveCommand(target('linux-x64')), {
            command: 'tar',
            args: ['-czf', 'escalier-linux-x64.tar.gz', 'escalier-linux-x64'],
        });
    });

    test('packs a Windows target with zip', () => {
        assert.deepEqual(archiveCommand(target('win32-x64')), {
            command: 'zip',
            args: ['-qr', 'escalier-win32-x64.zip', 'escalier-win32-x64'],
        });
    });
});

describe('archiveReadme', () => {
    const target = (suffix) => TARGETS.find((t) => t.suffix === suffix);

    test('names the binary for a Windows target', () => {
        assert.equal(
            archiveReadme(target('win32-x64'), '0.2.0'),
            `# escalier 0.2.0 for Windows on x64

This archive holds the escalier compiler and the standard library
it compiles against.

## Install

1. Unpack the archive anywhere.
2. Add its \`bin\` directory to your \`PATH\`.
3. Run \`escalier --version\` to check that it prints
   \`0.2.0\`.

Keep \`bin/\` and \`share/\` together. \`bin/escalier.exe\` reads the
standard library from \`share/escalier/data/\`.

To use escalier in a JavaScript project, install \`@escalier/cli\`
from npm instead.
`,
        );
    });

    test('tells macOS users how to lift the quarantine', () => {
        assert.equal(
            archiveReadme(target('darwin-arm64'), '0.2.0'),
            `# escalier 0.2.0 for macOS on Apple silicon

This archive holds the escalier compiler and the standard library
it compiles against.

## Install

1. Unpack the archive anywhere.
2. Add its \`bin\` directory to your \`PATH\`.
3. Run \`escalier --version\` to check that it prints
   \`0.2.0\`.

Keep \`bin/\` and \`share/\` together. \`bin/escalier\` reads the
standard library from \`share/escalier/data/\`.

macOS blocks a downloaded binary that is not signed. To allow
this one, run:

\`\`\`sh
xattr -d com.apple.quarantine bin/escalier
\`\`\`

To use escalier in a JavaScript project, install \`@escalier/cli\`
from npm instead.
`,
        );
    });
});

describe('publishedTags', () => {
    test('returns the tag of each published package', () => {
        assert.deepEqual(
            publishedTags(
                JSON.stringify([
                    { name: '@escalier/cli', version: '0.1.0' },
                    { name: '@escalier/cli-linux-x64', version: '0.1.0' },
                ]),
            ),
            new Set(['@escalier/cli@0.1.0', '@escalier/cli-linux-x64@0.1.0']),
        );
    });

    for (const json of [undefined, '']) {
        test(`returns an empty set for ${JSON.stringify(json)}`, () => {
            assert.deepEqual(publishedTags(json), new Set());
        });
    }
});
