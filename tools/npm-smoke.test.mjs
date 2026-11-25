import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import {
    execCommand,
    findTarballs,
    installCommand,
    parseArgs,
    projectManifest,
    smokeArgsFromEnv,
} from './npm-smoke.mjs';
import { TARGETS } from './platforms.mjs';

const linuxArm = TARGETS.find((target) => target.suffix === 'linux-arm');
const linuxX64 = TARGETS.find((target) => target.suffix === 'linux-x64');

const TARBALLS = [
    'escalier-cli-0.0.1.tgz',
    'escalier-cli-darwin-arm64-0.0.1.tgz',
    'escalier-cli-darwin-x64-0.0.1.tgz',
    'escalier-cli-linux-arm-0.0.1.tgz',
    'escalier-cli-linux-arm64-0.0.1.tgz',
    'escalier-cli-linux-x64-0.0.1.tgz',
    'escalier-cli-win32-arm64-0.0.1.tgz',
    'escalier-cli-win32-x64-0.0.1.tgz',
];

describe('parseArgs', () => {
    test('defaults to npm and the host target', () => {
        assert.deepEqual(parseArgs(['--tarballs', 'out'], 'linux-x64'), {
            tarballs: 'out',
            packageManager: 'npm',
            target: linuxX64,
            emulator: undefined,
        });
    });

    test('accepts another target with an emulator', () => {
        assert.deepEqual(
            parseArgs(
                [
                    '--tarballs=out',
                    '--target=linux-arm',
                    '--emulator=qemu-arm-static',
                ],
                'linux-x64',
            ),
            {
                tarballs: 'out',
                packageManager: 'npm',
                target: linuxArm,
                emulator: 'qemu-arm-static',
            },
        );
    });

    const errors = [
        [[], '--tarballs <dir> is required'],
        [
            ['--tarballs', 'out', '--package-manager', 'yarn'],
            'unknown package manager yarn, expected npm or pnpm',
        ],
        [
            ['--tarballs', 'out', '--target', 'freebsd-x64'],
            'unknown target freebsd-x64, expected one of: darwin-arm64, darwin-x64, linux-x64, linux-arm64, linux-arm, win32-x64, win32-arm64',
        ],
        [
            ['--tarballs', 'out', '--target', 'linux-arm'],
            "--target linux-arm differs from this machine's linux-x64 and needs --emulator",
        ],
        [
            [
                '--tarballs',
                'out',
                '--target',
                'linux-arm',
                '--emulator',
                'qemu-arm-static',
                '--package-manager',
                'pnpm',
            ],
            "--target linux-arm differs from this machine's linux-x64, which only npm can install for",
        ],
        [
            ['--tarballs', 'out', 'extra'],
            "Unexpected argument 'extra'. This command does not take positional arguments",
        ],
    ];
    for (const [argv, message] of errors) {
        test(`rejects ${argv.join(' ') || 'no arguments'}`, () => {
            assert.throws(() => parseArgs(argv, 'linux-x64'), { message });
        });
    }
});

describe('findTarballs', () => {
    test('maps each package to its tarball', () => {
        assert.deepEqual(findTarballs(['README.md', ...TARBALLS]), {
            '@escalier/cli': 'escalier-cli-0.0.1.tgz',
            '@escalier/cli-darwin-arm64': 'escalier-cli-darwin-arm64-0.0.1.tgz',
            '@escalier/cli-darwin-x64': 'escalier-cli-darwin-x64-0.0.1.tgz',
            '@escalier/cli-linux-x64': 'escalier-cli-linux-x64-0.0.1.tgz',
            '@escalier/cli-linux-arm64': 'escalier-cli-linux-arm64-0.0.1.tgz',
            '@escalier/cli-linux-arm': 'escalier-cli-linux-arm-0.0.1.tgz',
            '@escalier/cli-win32-x64': 'escalier-cli-win32-x64-0.0.1.tgz',
            '@escalier/cli-win32-arm64': 'escalier-cli-win32-arm64-0.0.1.tgz',
        });
    });

    test('rejects a missing platform tarball', () => {
        const files = TARBALLS.filter((f) => !f.includes('linux-arm-'));
        assert.throws(() => findTarballs(files), {
            message:
                'no tarball for @escalier/cli-linux-arm, expected escalier-cli-linux-arm-*.tgz',
        });
    });

    test('rejects a missing @escalier/cli tarball', () => {
        assert.throws(() => findTarballs(TARBALLS.slice(1)), {
            message:
                'no tarball for @escalier/cli, expected escalier-cli-*.tgz',
        });
    });
});

describe('projectManifest', () => {
    const paths = {
        '@escalier/cli': './tarballs/escalier-cli-0.0.1.tgz',
        '@escalier/cli-linux-x64':
            './tarballs/escalier-cli-linux-x64-0.0.1.tgz',
    };

    test('puts npm overrides at the top level', () => {
        assert.deepEqual(projectManifest(paths, 'npm'), {
            name: 'escalier-smoke-test',
            private: true,
            version: '0.0.0',
            dependencies: {
                '@escalier/cli': 'file:./tarballs/escalier-cli-0.0.1.tgz',
            },
            overrides: {
                '@escalier/cli-linux-x64':
                    'file:./tarballs/escalier-cli-linux-x64-0.0.1.tgz',
            },
        });
    });

    test('puts pnpm overrides under pnpm', () => {
        assert.deepEqual(projectManifest(paths, 'pnpm'), {
            name: 'escalier-smoke-test',
            private: true,
            version: '0.0.0',
            dependencies: {
                '@escalier/cli': 'file:./tarballs/escalier-cli-0.0.1.tgz',
            },
            pnpm: {
                overrides: {
                    '@escalier/cli-linux-x64':
                        'file:./tarballs/escalier-cli-linux-x64-0.0.1.tgz',
                },
            },
        });
    });
});

describe('installCommand', () => {
    test('installs for the host with npm', () => {
        assert.deepEqual(installCommand('npm', linuxX64, false), [
            'npm',
            ['install', '--no-audit', '--no-fund'],
        ]);
    });

    test('passes --os and --cpu to npm for another target', () => {
        assert.deepEqual(installCommand('npm', linuxArm, true), [
            'npm',
            ['install', '--no-audit', '--no-fund', '--os=linux', '--cpu=arm'],
        ]);
    });

    test('installs with pnpm', () => {
        assert.deepEqual(installCommand('pnpm', linuxX64, false), [
            'pnpm',
            ['install', '--no-frozen-lockfile'],
        ]);
    });
});

describe('execCommand', () => {
    test('separates the npx flags from the escalier arguments', () => {
        assert.deepEqual(execCommand('npm', ['--version']), [
            'npx',
            ['--no', '--', 'escalier', '--version'],
        ]);
    });

    test('runs pnpm exec', () => {
        assert.deepEqual(execCommand('pnpm', ['build', 'fixture']), [
            'pnpm',
            ['exec', 'escalier', 'build', 'fixture'],
        ]);
    });
});

describe('smokeArgsFromEnv', () => {
    test('returns undefined without ESCALIER_SMOKE_TARBALLS', () => {
        assert.equal(smokeArgsFromEnv({}), undefined);
        assert.equal(
            smokeArgsFromEnv({ ESCALIER_SMOKE_TARBALLS: '' }),
            undefined,
        );
    });

    test('turns each set variable into its flag', () => {
        assert.deepEqual(
            smokeArgsFromEnv({
                ESCALIER_SMOKE_TARBALLS: 'npm-tarballs',
                ESCALIER_SMOKE_PACKAGE_MANAGER: 'pnpm',
                ESCALIER_SMOKE_TARGET: 'linux-arm',
                ESCALIER_SMOKE_EMULATOR: 'qemu-arm',
            }),
            [
                '--tarballs',
                'npm-tarballs',
                '--package-manager',
                'pnpm',
                '--target',
                'linux-arm',
                '--emulator',
                'qemu-arm',
            ],
        );
    });

    test('leaves out unset and empty variables', () => {
        assert.deepEqual(
            smokeArgsFromEnv({
                ESCALIER_SMOKE_TARBALLS: 'npm-tarballs',
                ESCALIER_SMOKE_EMULATOR: '',
            }),
            ['--tarballs', 'npm-tarballs'],
        );
    });
});
