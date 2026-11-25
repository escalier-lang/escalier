import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, test } from 'vitest';

import {
    SUPPORTED_PLATFORMS,
    getBinaryPath,
    getStdlibDir,
    platformPackageName,
} from './index.js';

const notFound = () => {
    throw new Error('Cannot find module');
};

describe('platformPackageName', () => {
    test.each([
        ['linux', 'x64', '@escalier/cli-linux-x64'],
        ['linux', 'arm', '@escalier/cli-linux-arm'],
        ['darwin', 'arm64', '@escalier/cli-darwin-arm64'],
        ['win32', 'arm64', '@escalier/cli-win32-arm64'],
        ['freebsd', 'x64', undefined],
        ['linux', 'ia32', undefined],
    ])('%s %s -> %s', (platform, arch, expected) => {
        expect(platformPackageName(platform, arch)).toBe(expected);
    });
});

describe('getBinaryPath', () => {
    test('resolves the binary inside the platform package', () => {
        const specifiers: string[] = [];
        const path = getBinaryPath({
            platform: 'win32',
            arch: 'x64',
            env: {},
            resolve: (specifier) => {
                specifiers.push(specifier);
                return `/node_modules/${specifier}`;
            },
        });
        expect(specifiers).toEqual([
            '@escalier/cli-win32-x64/bin/escalier.exe',
        ]);
        expect(path).toBe(
            '/node_modules/@escalier/cli-win32-x64/bin/escalier.exe',
        );
    });

    test('prefers ESCALIER_BINARY_PATH', () => {
        const path = getBinaryPath({
            platform: 'freebsd',
            arch: 'x64',
            env: { ESCALIER_BINARY_PATH: '/opt/escalier' },
            resolve: notFound,
        });
        expect(path).toBe('/opt/escalier');
    });

    test('ignores an empty ESCALIER_BINARY_PATH', () => {
        const path = getBinaryPath({
            platform: 'linux',
            arch: 'x64',
            env: { ESCALIER_BINARY_PATH: '' },
            resolve: (specifier) => `/node_modules/${specifier}`,
        });
        expect(path).toBe('/node_modules/@escalier/cli-linux-x64/bin/escalier');
    });

    test('explains an unsupported platform', () => {
        expect(() =>
            getBinaryPath({
                platform: 'freebsd',
                arch: 'x64',
                env: {},
                resolve: notFound,
            }),
        ).toThrow(
            new Error(
                [
                    '@escalier/cli has no escalier binary for freebsd-x64.',
                    'Supported platforms: darwin-arm64, darwin-x64, linux-arm, linux-arm64, linux-x64, win32-arm64, win32-x64.',
                    'Set ESCALIER_BINARY_PATH to the path of an escalier binary to use one you built yourself.',
                ].join('\n'),
            ),
        );
    });

    test('explains an installed platform package with no binary', () => {
        expect(() =>
            getBinaryPath({
                platform: 'win32',
                arch: 'arm64',
                env: {},
                resolve: (specifier) => {
                    if (specifier.endsWith('/package.json')) {
                        return `/node_modules/${specifier}`;
                    }
                    return notFound();
                },
            }),
        ).toThrow(
            new Error(
                [
                    '@escalier/cli-win32-arm64 is installed but has no bin/escalier.exe.',
                    'In a checkout of the escalier repo, build it with `node tools/build-binaries.mjs`, or set ESCALIER_BINARY_PATH to the path of an escalier binary.',
                ].join('\n'),
            ),
        );
    });

    test('explains a missing platform package', () => {
        expect(() =>
            getBinaryPath({
                platform: 'linux',
                arch: 'arm64',
                env: {},
                resolve: notFound,
            }),
        ).toThrow(
            new Error(
                [
                    "@escalier/cli couldn't find @escalier/cli-linux-arm64, which holds the escalier binary for linux-arm64.",
                    'Package managers install it as an optional dependency of @escalier/cli. It is missing when optional dependencies are skipped (--no-optional or --omit=optional) or when the lockfile was created on another platform.',
                    'Reinstall @escalier/cli without skipping optional dependencies, or set ESCALIER_BINARY_PATH to the path of an escalier binary.',
                ].join('\n'),
            ),
        );
    });
});

describe('SUPPORTED_PLATFORMS', () => {
    test('lists the platform packages in optionalDependencies', () => {
        expect(SUPPORTED_PLATFORMS).toEqual([
            'darwin-arm64',
            'darwin-x64',
            'linux-arm',
            'linux-arm64',
            'linux-x64',
            'win32-arm64',
            'win32-x64',
        ]);
    });
});

describe('getStdlibDir', () => {
    test('names the data directory at the package root', () => {
        const packageRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
        expect(getStdlibDir()).toBe(join(packageRoot, 'data'));
    });
});
