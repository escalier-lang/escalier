import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const PLATFORM_PACKAGE_PREFIX = '@escalier/cli-';

// tools/gen-platform-packages.mjs writes one optional dependency per target
// in tools/platforms.mjs, so this package's own manifest lists the
// platforms without a second copy of the list.
const manifest = createRequire(import.meta.url)('../package.json') as {
    optionalDependencies?: Record<string, string>;
};

// The platforms that have an `@escalier/cli-<platform>-<arch>` package, named
// by Node's `process.platform` and `process.arch`, in alphabetical order.
export const SUPPORTED_PLATFORMS: readonly string[] = Object.keys(
    manifest.optionalDependencies ?? {},
)
    .filter((name) => name.startsWith(PLATFORM_PACKAGE_PREFIX))
    .map((name) => name.slice(PLATFORM_PACKAGE_PREFIX.length));

// The environment variable that names an escalier binary to use instead of
// the one from the platform package.
export const BINARY_PATH_ENV = 'ESCALIER_BINARY_PATH';

// The environment variable the escalier binary reads the stdlib directory
// from.
export const STDLIB_DIR_ENV = 'ESCALIER_STDLIB_DIR';

// Returns the directory that holds the stdlib `.esc` files this package
// ships. The escalier binary reads them at run time and finds them only
// when ESCALIER_STDLIB_DIR or its `--stdlib-dir` flag names the directory.
export function getStdlibDir(): string {
    return join(dirname(fileURLToPath(import.meta.url)), '..', 'data');
}

// Returns the name of the package that holds the escalier binary for
// `platform` and `arch`, such as `@escalier/cli-linux-x64`, or `undefined`
// when no package exists for that pair.
export function platformPackageName(
    platform: string,
    arch: string,
): string | undefined {
    const key = `${platform}-${arch}`;
    return SUPPORTED_PLATFORMS.includes(key)
        ? `${PLATFORM_PACKAGE_PREFIX}${key}`
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

// Returns the error message for a platform package that is installed but
// holds no binary. A published package always has one, so this happens in
// a checkout of the escalier repo before the binaries are built.
export function missingBinaryMessage(
    packageName: string,
    fileName: string,
): string {
    return [
        `${packageName} is installed but has no bin/${fileName}.`,
        `In a checkout of the escalier repo, build it with \`node tools/build-binaries.mjs\`, or set ${BINARY_PATH_ENV} to the path of an escalier binary.`,
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
// explanation when the platform has no package, its package isn't
// installed, or the package has no binary.
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
    const fileName = binaryFileName(platform);
    try {
        return resolve(`${packageName}/bin/${fileName}`);
    } catch {
        // Resolving the package's manifest tells an uninstalled package apart
        // from an installed one whose binary is missing.
        try {
            resolve(`${packageName}/package.json`);
        } catch {
            throw new Error(missingPackageMessage(packageName, platform, arch));
        }
        throw new Error(missingBinaryMessage(packageName, fileName));
    }
}
