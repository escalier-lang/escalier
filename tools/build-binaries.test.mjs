import assert from 'node:assert/strict';
import { join } from 'node:path';
import { describe, test } from 'node:test';

import {
    BINARIES,
    goBuildArgs,
    goEnv,
    parseArgs,
    selectBuilds,
} from './build-binaries.mjs';
import { TARGETS } from './platforms.mjs';

const escalier = BINARIES.find((binary) => binary.name === 'escalier');
const lspServer = BINARIES.find((binary) => binary.name === 'lsp-server');
const linuxArm = TARGETS.find((target) => target.suffix === 'linux-arm');
const winX64 = TARGETS.find((target) => target.suffix === 'win32-x64');

describe('parseArgs', () => {
    test('selects everything with no arguments', () => {
        assert.deepEqual(parseArgs([]), { only: [], onlyBinary: [] });
    });

    test('collects repeated --only and --only-binary values', () => {
        assert.deepEqual(
            parseArgs([
                '--only',
                'linux-x64',
                '--only-binary',
                'escalier',
                '--only',
                'darwin-arm64',
            ]),
            { only: ['linux-x64', 'darwin-arm64'], onlyBinary: ['escalier'] },
        );
    });

    const errors = [
        [['--all'], 'unknown argument: --all'],
        [['--only'], '--only needs a value'],
        [['--only', '--only-binary', 'escalier'], '--only needs a value'],
        [
            ['--only', 'freebsd-x64'],
            'unknown target freebsd-x64, expected one of: darwin-arm64, darwin-x64, linux-x64, linux-arm64, linux-arm, win32-x64, win32-arm64',
        ],
        [
            ['--only-binary', 'formatter'],
            'unknown binary formatter, expected one of: escalier, lsp-server',
        ],
    ];
    for (const [argv, message] of errors) {
        test(`rejects ${argv.join(' ')}`, () => {
            assert.throws(() => parseArgs(argv), { message });
        });
    }
});

describe('selectBuilds', () => {
    test('builds both binaries for every target by default', () => {
        const builds = selectBuilds({ only: [], onlyBinary: [] });
        assert.equal(builds.length, TARGETS.length * BINARIES.length);
    });

    test('narrows to the selected targets and binaries', () => {
        const builds = selectBuilds({
            only: ['win32-x64', 'linux-arm'],
            onlyBinary: ['lsp-server'],
        });
        assert.deepEqual(
            builds.map(
                ({ target, binary }) => `${target.suffix}/${binary.name}`,
            ),
            ['linux-arm/lsp-server', 'win32-x64/lsp-server'],
        );
    });
});

describe('BINARIES', () => {
    test('puts escalier in its platform package and lsp-server under bin/', () => {
        assert.equal(
            escalier.outDir(winX64),
            join('packages', 'platforms', 'cli-win32-x64', 'bin'),
        );
        assert.equal(lspServer.outDir(winX64), join('bin', 'win32-x64'));
    });
});

describe('goEnv', () => {
    test('disables cgo, pins the CPU levels and sets GOARM only for 32-bit ARM', () => {
        const pinned = {
            CGO_ENABLED: '0',
            GOAMD64: 'v1',
            GOARM64: 'v8.0',
            GOFLAGS: '-mod=readonly',
        };
        assert.deepEqual(goEnv(linuxArm), {
            GOOS: 'linux',
            GOARCH: 'arm',
            ...pinned,
            GOARM: '7',
        });
        assert.deepEqual(goEnv(winX64), {
            GOOS: 'windows',
            GOARCH: 'amd64',
            ...pinned,
        });
    });
});

describe('goBuildArgs', () => {
    test('strips the binary and sets buildinfo.Version', () => {
        assert.deepEqual(goBuildArgs(escalier, '/out/escalier', '1.2.3'), [
            'build',
            '-trimpath',
            '-ldflags',
            '-s -w -X github.com/escalier-lang/escalier/internal/buildinfo.Version=1.2.3',
            '-o',
            '/out/escalier',
            './cmd/escalier',
        ]);
    });
});
