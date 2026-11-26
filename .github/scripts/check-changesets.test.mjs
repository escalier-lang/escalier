import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, test } from 'node:test';

import {
    changedFiles,
    changedPackages,
    formatReport,
    isCliSource,
    owningPackage,
    parseStatusRun,
    unreleasedPackages,
} from './check-changesets.mjs';

const packages = [
    { name: '@escalier/cli', dir: 'packages/cli' },
    {
        name: '@escalier/cli-linux-x64',
        dir: 'packages/platforms/cli-linux-x64',
    },
    { name: '@escalier/runtime', dir: 'packages/runtime' },
];

describe('isCliSource', () => {
    const cases = [
        ['go.mod', true],
        ['go.sum', true],
        ['cmd/escalier/build.go', true],
        ['internal/solver/infer.go', true],
        ['internal/interop/data/std/prelude.esc', true],
        ['internal/ecma262/curated.json', true],
        ['internal/solver/README.md', false],
        ['internal/codegen/fixtures/out.js.map', false],
        ['cmd/escalier/README.md', false],
        ['internal/solver/infer_test.go', false],
        ['internal/printer/__snapshots__/printer_test.snap', false],
        ['internal/dts_parser/testdata/lib.d.ts', false],
        ['cmd/lsp-server/hover.go', false],
        ['fixtures/async_await/lib/async_await.esc', false],
        ['README.md', false],
    ];
    for (const [path, expected] of cases) {
        test(`${path} -> ${String(expected)}`, () => {
            assert.equal(isCliSource(path), expected);
        });
    }
});

describe('owningPackage', () => {
    const cases = [
        ['internal/solver/infer.go', '@escalier/cli'],
        ['packages/cli/src/index.ts', '@escalier/cli'],
        [
            'packages/platforms/cli-linux-x64/package.json',
            '@escalier/cli-linux-x64',
        ],
        ['packages/runtime/src/index.ts', '@escalier/runtime'],
        ['packages/runtime-extras/index.ts', undefined],
        ['packages/vscode-escalier/src/extension.ts', undefined],
        ['cmd/lsp-server/hover.go', undefined],
    ];
    for (const [path, expected] of cases) {
        test(`${path} -> ${String(expected)}`, () => {
            assert.equal(owningPackage(path, packages), expected);
        });
    }
});

describe('changedFiles', () => {
    test('lists a moved file under its old and its new path', (t) => {
        const dir = mkdtempSync(join(tmpdir(), 'changed-files-'));
        t.after(() => rmSync(dir, { recursive: true, force: true }));
        const git = (...args) =>
            execFileSync('git', args, { cwd: dir, encoding: 'utf8' });
        git('init', '-q', '-b', 'main');
        git('config', 'user.email', 'test@example.com');
        git('config', 'user.name', 'Test');
        mkdirSync(join(dir, 'internal'));
        writeFileSync(
            join(dir, 'internal', 'buildinfo.go'),
            'package buildinfo\n',
        );
        git('add', '.');
        git('commit', '-q', '-m', 'base');
        git('checkout', '-q', '-b', 'pr');
        mkdirSync(join(dir, 'tools'));
        git('mv', 'internal/buildinfo.go', 'tools/buildinfo.go');
        git('commit', '-q', '-m', 'move');

        assert.deepEqual(changedFiles('main', dir).sort(), [
            'internal/buildinfo.go',
            'tools/buildinfo.go',
        ]);
    });
});

describe('changedPackages', () => {
    test('groups the changed files by the package they belong to', () => {
        const byPackage = changedPackages(
            [
                'go.mod',
                'README.md',
                'packages/runtime/src/index.ts',
                'internal/solver/infer.go',
            ],
            packages,
        );
        assert.deepEqual(
            [...byPackage],
            [
                ['@escalier/cli', ['go.mod', 'internal/solver/infer.go']],
                ['@escalier/runtime', ['packages/runtime/src/index.ts']],
            ],
        );
    });
});

describe('unreleasedPackages', () => {
    const cliRelease = { name: '@escalier/cli', type: 'patch' };
    const runtimeRelease = { name: '@escalier/runtime', type: 'minor' };
    const required = ['@escalier/cli', '@escalier/runtime'];

    const cases = [
        {
            name: 'changesets that release every package',
            status: {
                changesets: [
                    { id: 'a', releases: [cliRelease, runtimeRelease] },
                ],
                releases: [cliRelease, runtimeRelease],
            },
            expected: [],
        },
        {
            name: 'an empty changeset',
            status: { changesets: [{ id: 'a', releases: [] }], releases: [] },
            expected: [],
        },
        {
            name: 'a changeset for one of the two packages',
            status: {
                changesets: [{ id: 'a', releases: [runtimeRelease] }],
                releases: [runtimeRelease],
            },
            expected: ['@escalier/cli'],
        },
        {
            name: 'a release with type none',
            status: {
                changesets: [{ id: 'a', releases: [runtimeRelease] }],
                releases: [runtimeRelease, { ...cliRelease, type: 'none' }],
            },
            expected: ['@escalier/cli'],
        },
        {
            name: 'no changesets',
            status: { changesets: [], releases: [] },
            expected: required,
        },
    ];
    for (const { name, status, expected } of cases) {
        test(name, () => {
            assert.deepEqual(unreleasedPackages(required, status), expected);
        });
    }
});

describe('parseStatusRun', () => {
    test('parses the --output file when the run wrote one', () => {
        assert.deepEqual(
            parseStatusRun({
                output: '{"changesets":[],"releases":[]}',
                exitCode: 0,
                stderr: '',
            }),
            { changesets: [], releases: [] },
        );
    });

    test('reads a no-changesets failure as an empty status', () => {
        assert.deepEqual(
            parseStatusRun({
                output: undefined,
                exitCode: 1,
                stderr: '🦋  error Some packages have been changed but no changesets were found. Run `changeset add` to resolve this error.\n',
            }),
            { changesets: [], releases: [] },
        );
    });

    test('reports any other failure with its stderr', () => {
        assert.throws(
            () =>
                parseStatusRun({
                    output: undefined,
                    exitCode: 1,
                    stderr: "🦋  error fatal: bad revision 'origin/missing...HEAD'\n",
                }),
            {
                message:
                    "changeset status failed with exit code 1:\n🦋  error fatal: bad revision 'origin/missing...HEAD'",
            },
        );
    });
});

describe('formatReport', () => {
    test('lists each unreleased package with its files and both fixes', () => {
        const byPackage = new Map([
            ['@escalier/cli', ['go.mod', 'internal/solver/infer.go']],
            ['@escalier/runtime', ['packages/runtime/src/index.ts']],
        ]);
        assert.equal(
            formatReport(['@escalier/cli'], byPackage),
            [
                'These packages changed, but no changeset releases them:',
                '  @escalier/cli',
                '    go.mod',
                '    internal/solver/infer.go',
                "Run `pnpm changeset` and select each package, or run `pnpm changeset --empty` if the changes shouldn't release.",
            ].join('\n'),
        );
    });
});
