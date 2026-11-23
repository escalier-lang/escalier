import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import {
    formatReport,
    hasCliChangeset,
    isCliSource,
} from './check-cli-changeset.mjs';

describe('isCliSource', () => {
    const cases = [
        ['go.mod', true],
        ['go.sum', true],
        ['cmd/escalier/build.go', true],
        ['internal/solver/infer.go', true],
        ['internal/stdlibdir/std/prelude.esc', true],
        ['internal/solver/infer_test.go', false],
        ['internal/printer/__snapshots__/printer_test.snap', false],
        ['internal/dts_parser/testdata/lib.d.ts', false],
        ['cmd/lsp-server/hover.go', false],
        ['fixtures/async_await/lib/async_await.esc', false],
        ['packages/cli/src/index.ts', false],
        ['README.md', false],
    ];
    for (const [path, expected] of cases) {
        test(`${path} -> ${expected}`, () => {
            assert.equal(isCliSource(path), expected);
        });
    }
});

describe('hasCliChangeset', () => {
    const cliRelease = { name: '@escalier/cli', type: 'patch' };
    const platformRelease = { name: '@escalier/cli-linux-x64', type: 'patch' };
    const runtimeRelease = { name: '@escalier/runtime', type: 'minor' };

    const cases = [
        {
            name: 'a changeset that releases @escalier/cli',
            status: {
                changesets: [{ id: 'a', releases: [cliRelease] }],
                releases: [cliRelease, platformRelease],
            },
            expected: true,
        },
        {
            name: 'an empty changeset',
            status: { changesets: [{ id: 'a', releases: [] }], releases: [] },
            expected: true,
        },
        {
            name: 'a changeset for another package only',
            status: {
                changesets: [{ id: 'a', releases: [runtimeRelease] }],
                releases: [runtimeRelease],
            },
            expected: false,
        },
        {
            name: 'a release of @escalier/cli with type none',
            status: {
                changesets: [{ id: 'a', releases: [runtimeRelease] }],
                releases: [runtimeRelease, { ...cliRelease, type: 'none' }],
            },
            expected: false,
        },
        {
            name: 'no changesets',
            status: { changesets: [], releases: [] },
            expected: false,
        },
    ];
    for (const { name, status, expected } of cases) {
        test(`${name} -> ${String(expected)}`, () => {
            assert.equal(hasCliChangeset(status), expected);
        });
    }
});

describe('formatReport', () => {
    test('lists the compiler files and both ways to resolve it', () => {
        assert.equal(
            formatReport(['go.mod', 'internal/solver/infer.go']),
            [
                'These changes build into the escalier binary, but no changeset releases @escalier/cli:',
                '  go.mod',
                '  internal/solver/infer.go',
                "Run `pnpm changeset` and select @escalier/cli, or run `pnpm changeset --empty` if the change shouldn't release.",
            ].join('\n'),
        );
    });
});
