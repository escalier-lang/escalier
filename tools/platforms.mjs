// The platforms the escalier binary is published for. Each target becomes
// one npm package, `@escalier/cli-<suffix>`, that `@escalier/cli` lists in
// its optionalDependencies. The suffix and the `os`/`cpu` values use Node's
// `process.platform` and `process.arch` names, which is what npm matches
// against when it picks the package to install. `goos`, `goarch` and
// `goarm` are the Go build settings that produce the binary.
//
// The list covers macOS, Linux and Windows on the x64 and ARM CPUs that
// Node and VS Code ship builds for. The rest of the targets that
// `go tool dist list` reports, such as the BSDs, 32-bit x86 and wasm, have
// few or no Node users. Adding a target here and rerunning
// tools/gen-platform-packages.mjs adds its package.
export const TARGETS = [
    {
        suffix: 'darwin-arm64',
        os: 'darwin',
        cpu: 'arm64',
        goos: 'darwin',
        goarch: 'arm64',
        label: 'macOS on Apple silicon',
    },
    {
        suffix: 'darwin-x64',
        os: 'darwin',
        cpu: 'x64',
        goos: 'darwin',
        goarch: 'amd64',
        label: 'macOS on Intel',
    },
    {
        suffix: 'linux-x64',
        os: 'linux',
        cpu: 'x64',
        goos: 'linux',
        goarch: 'amd64',
        label: 'Linux on x64',
    },
    {
        suffix: 'linux-arm64',
        os: 'linux',
        cpu: 'arm64',
        goos: 'linux',
        goarch: 'arm64',
        label: 'Linux on ARM64',
    },
    {
        suffix: 'linux-arm',
        os: 'linux',
        cpu: 'arm',
        goos: 'linux',
        goarch: 'arm',
        goarm: '7',
        label: 'Linux on 32-bit ARMv7',
    },
    {
        suffix: 'win32-x64',
        os: 'win32',
        cpu: 'x64',
        goos: 'windows',
        goarch: 'amd64',
        label: 'Windows on x64',
    },
    {
        suffix: 'win32-arm64',
        os: 'win32',
        cpu: 'arm64',
        goos: 'windows',
        goarch: 'arm64',
        label: 'Windows on ARM64',
    },
];

// Returns the npm package name for a target suffix, such as
// `@escalier/cli-linux-x64` for `linux-x64`.
export function platformPackageName(suffix) {
    return `@escalier/cli-${suffix}`;
}

// Returns the file name of the executable `name` on the target's OS, such
// as `lsp-server.exe` for `lsp-server` on Windows.
export function executableName(name, target) {
    return target.os === 'win32' ? `${name}.exe` : name;
}

// Returns the file name of the escalier binary on the target's OS.
export function binaryName(target) {
    return executableName('escalier', target);
}
