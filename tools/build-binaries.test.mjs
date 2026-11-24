import assert from 'node:assert/strict';
import { join } from 'node:path';
import { describe, test } from 'node:test';

import {
    binaryDir,
    goBuildArgs,
    goEnv,
    parseArgs,
    selectTargets,
} from './build-binaries.mjs';
import { TARGETS } from './platforms.mjs';

const linuxArm = TARGETS.find((target) => target.suffix === 'linux-arm');
const winX64 = TARGETS.find((target) => target.suffix === 'win32-x64');

describe('parseArgs', () => {
    test('selects every target with no arguments', () => {
        assert.deepEqual(parseArgs([]), []);
    });

    test('collects repeated --only values', () => {
        assert.deepEqual(
            parseArgs(['--only', 'linux-x64', '--only', 'darwin-arm64']),
            ['linux-x64', 'darwin-arm64'],
        );
    });

    const errors = [
        [['--all'], 'unknown argument: --all'],
        [['--only'], '--only needs a value'],
        [['--only', '--only', 'linux-x64'], '--only needs a value'],
        [
            ['--only', 'freebsd-x64'],
            'unknown target freebsd-x64, expected one of: darwin-arm64, darwin-x64, linux-x64, linux-arm64, linux-arm, win32-x64, win32-arm64',
        ],
    ];
    for (const [argv, message] of errors) {
        test(`rejects ${argv.join(' ')}`, () => {
            assert.throws(() => parseArgs(argv), { message });
        });
    }
});

describe('selectTargets', () => {
    test('builds every target by default', () => {
        assert.deepEqual(selectTargets([]), TARGETS);
    });

    test('narrows to the selected targets in TARGETS order', () => {
        assert.deepEqual(
            selectTargets(['win32-x64', 'linux-arm']).map(
                (target) => target.suffix,
            ),
            ['linux-arm', 'win32-x64'],
        );
    });
});

describe('binaryDir', () => {
    test('puts the binary in its platform package', () => {
        assert.equal(
            binaryDir(winX64),
            join('packages', 'platforms', 'cli-win32-x64', 'bin'),
        );
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
        assert.deepEqual(goBuildArgs('/out/escalier', '1.2.3'), [
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
