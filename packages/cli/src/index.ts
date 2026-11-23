import { createRequire } from 'node:module';

// The platforms that have an `@escalier/cli-<platform>-<arch>` package, named
// by Node's `process.platform` and `process.arch`. The list matches TARGETS
// in tools/platforms.mjs, which a test checks.
export const SUPPORTED_PLATFORMS = [
    'darwin-arm64',
    'darwin-x64',
    'linux-x64',
    'linux-arm64',
    'linux-arm',
    'win32-x64',
    'win32-arm64',
] as const;

// The environment variable that names an escalier binary to use instead of
// the one from the platform package.
export const BINARY_PATH_ENV = 'ESCALIER_BINARY_PATH';

// Returns the name of the package that holds the escalier binary for
// `platform` and `arch`, such as `@escalier/cli-linux-x64`, or `undefined`
// when no package exists for that pair.
export function platformPackageName(
    platform: string,
    arch: string,
): string | undefined {
    const key = `${platform}-${arch}`;
    return (SUPPORTED_PLATFORMS as readonly string[]).includes(key)
        ? `@escalier/cli-${key}`
        : undefined;
}

// Returns the file name of the escalier binary on `platform`.
export function binaryFileName(platform: string): string {
    return platform === 'win32' ? 'escalier.exe' : 'escalier';
}

// Returns the error message for a platform that has no package.
export function unsupportedPlatformMessage(
    platform: string,
    arch: string,
): string {
    return [
        `@escalier/cli has no escalier binary for ${platform}-${arch}.`,
        `Supported platforms: ${SUPPORTED_PLATFORMS.join(', ')}.`,
        `Set ${BINARY_PATH_ENV} to the path of an escalier binary to use one you built yourself.`,
    ].join('\n');
}

// Returns the error message for a supported platform whose package isn't
// installed.
export function missingPackageMessage(
    packageName: string,
    platform: string,
    arch: string,
): string {
    return [
        `@escalier/cli couldn't find ${packageName}, which holds the escalier binary for ${platform}-${arch}.`,
        `Package managers install it as an optional dependency of @escalier/cli. It is missing when optional dependencies are skipped (--no-optional or --omit=optional) or when the lockfile was created on another platform.`,
        `Reinstall @escalier/cli without skipping optional dependencies, or set ${BINARY_PATH_ENV} to the path of an escalier binary.`,
    ].join('\n');
}

export interface BinaryPathOptions {
    // Defaults to `process.platform`.
    platform?: string;
    // Defaults to `process.arch`.
    arch?: string;
    // Defaults to `process.env`.
    env?: Record<string, string | undefined>;
    // Resolves a module specifier to a file path, like `require.resolve`.
    // Defaults to resolving from this package.
    resolve?: (specifier: string) => string;
}

// Returns the path of the escalier binary to run. A non-empty
// ESCALIER_BINARY_PATH wins. Otherwise the path points into the installed
// `@escalier/cli-<platform>-<arch>` package. Throws an Error with the full
// explanation when the platform has no package or its package isn't
// installed.
export function getBinaryPath(options: BinaryPathOptions = {}): string {
    const platform = options.platform ?? process.platform;
    const arch = options.arch ?? process.arch;
    const env = options.env ?? process.env;
    const resolve = options.resolve ?? createRequire(import.meta.url).resolve;

    const override = env[BINARY_PATH_ENV];
    if (override !== undefined && override !== '') {
        return override;
    }

    const packageName = platformPackageName(platform, arch);
    if (packageName === undefined) {
        throw new Error(unsupportedPlatformMessage(platform, arch));
    }
    try {
        return resolve(`${packageName}/bin/${binaryFileName(platform)}`);
    } catch {
        throw new Error(missingPackageMessage(packageName, platform, arch));
    }
}
