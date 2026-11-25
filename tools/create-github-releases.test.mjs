import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import {
    assetName,
    changelogEntry,
    groupReleases,
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

describe('assetName', () => {
    test('names each binary after its target', () => {
        assert.deepEqual(TARGETS.map(assetName), [
            'escalier-darwin-arm64',
            'escalier-darwin-x64',
            'escalier-linux-x64',
            'escalier-linux-arm64',
            'escalier-linux-arm',
            'escalier-win32-x64.exe',
            'escalier-win32-arm64.exe',
        ]);
    });
});
