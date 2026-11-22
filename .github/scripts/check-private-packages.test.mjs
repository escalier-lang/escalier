import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import {
    findUnmarkedPackages,
    formatReport,
    isPublished,
} from './check-private-packages.mjs';

describe('isPublished', () => {
    const cases = [
        ['@escalier/runtime', true],
        ['@escalier/escalier', true],
        ['@escalier/lsp-server', true],
        ['@escalier/escalier-linux-x64', true],
        ['@escalier/escalier-linux-arm', true],
        ['@escalier/lsp-server-win32-arm64', true],
        ['@escalier/escalier-plugin-vite', false],
        ['@escalier/lsp-server-freebsd-x64', false],
        ['@escalier/cli', false],
        ['escalier', false],
        ['async_await', false],
    ];
    for (const [name, expected] of cases) {
        test(`${name} -> ${expected}`, () => {
            assert.equal(isPublished(name), expected);
        });
    }
});

describe('findUnmarkedPackages', () => {
    test('reports only packages that are neither private nor published', () => {
        const packages = [
            { name: 'escalier', path: '/repo', private: true },
            {
                name: '@escalier/runtime',
                path: '/repo/packages/runtime',
                private: false,
            },
            {
                name: '@escalier/escalier-darwin-arm64',
                path: '/repo/packages/platforms/escalier-darwin-arm64',
                private: false,
            },
            {
                name: 'async_await',
                path: '/repo/fixtures/async_await',
                private: false,
            },
            {
                name: 'bin_only',
                path: '/repo/fixtures/bin_only',
                private: true,
            },
        ];
        assert.deepEqual(findUnmarkedPackages(packages), [
            {
                name: 'async_await',
                path: '/repo/fixtures/async_await',
                private: false,
            },
        ]);
    });
});

describe('formatReport', () => {
    test('lists each package with its manifest path relative to cwd', () => {
        const unmarked = [
            { name: 'escalier', path: '/repo', private: false },
            {
                name: 'async_await',
                path: '/repo/fixtures/async_await',
                private: false,
            },
        ];
        assert.equal(
            formatReport(unmarked, '/repo'),
            [
                'These workspace packages are not published to npm but lack "private": true:',
                '  escalier (./package.json)',
                '  async_await (fixtures/async_await/package.json)',
                'Add "private": true to each one, or add the package to PUBLISHED_NAMES ' +
                    'in .github/scripts/check-private-packages.mjs if it should be published.',
            ].join('\n'),
        );
    });
});
