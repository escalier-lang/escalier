import type { Stats } from 'node:fs';
import { beforeEach, describe, expect, test, vi } from 'vitest';

import { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { loadProject } from './project-loader';

const encoder = new TextEncoder();
const decoder = new TextDecoder();

// --- Helpers ---

function stat(fs: BrowserFS, path: string): Promise<Stats> {
    return new Promise((resolve, reject) => {
        fs.stat(path, (err, stats) => {
            if (err) reject(err);
            else resolve(stats);
        });
    });
}

function readFile(fs: BrowserFS, path: string): Promise<string> {
    return new Promise((resolve, reject) => {
        fs.open(path, 'r', undefined, (openErr, fd) => {
            if (openErr || fd === undefined) {
                reject(openErr ?? new Error('no fd'));
                return;
            }
            fs.fstat(fd, (statErr, stats) => {
                if (statErr) {
                    fs.close(fd, () => {});
                    reject(statErr);
                    return;
                }
                const buf = new Uint8Array(stats.size);
                fs.read(fd, buf, 0, stats.size, 0, (readErr) => {
                    fs.close(fd, () => {});
                    if (readErr) reject(readErr);
                    else resolve(decoder.decode(buf));
                });
            });
        });
    });
}

function readdir(fs: BrowserFS, path: string): Promise<string[]> {
    return new Promise((resolve, reject) => {
        fs.readdir(path, (err, files) => {
            if (err) reject(err);
            else resolve(files);
        });
    });
}

// --- Test data ---

const manifest: Manifest = {
    types: [],
    templates: {
        'single-package': [
            'escalier.toml',
            'package.json',
            'lib/index.esc',
            'bin/main.esc',
        ],
    },
    examples: {
        'hello-world': [
            'escalier.toml',
            'package.json',
            'lib/index.esc',
            'bin/main.esc',
        ],
        'no-lib-index': ['escalier.toml', 'package.json', 'bin/main.esc'],
        'no-esc': ['escalier.toml', 'package.json'],
    },
};

const fileContents: Record<string, string> = {
    'examples/hello-world/escalier.toml': '[project]\nname = "hello-world"\n',
    'examples/hello-world/package.json': '{"name":"hello-world"}',
    'examples/hello-world/lib/index.esc':
        'export fn add(a: number, b: number) -> number {\n    return a + b\n}\n',
    'examples/hello-world/bin/main.esc': 'val sum = add(5, 10)\n',
    'examples/no-lib-index/escalier.toml': '[project]\nname = "no-lib-index"\n',
    'examples/no-lib-index/package.json': '{"name":"no-lib-index"}',
    'examples/no-lib-index/bin/main.esc': 'console.log("hi")\n',
    'examples/no-esc/escalier.toml': '[project]\nname = "no-esc"\n',
    'examples/no-esc/package.json': '{"name":"no-esc"}',
    'templates/single-package/escalier.toml':
        '[project]\nname = "my-project"\n',
    'templates/single-package/package.json': '{"name":"my-project"}',
    'templates/single-package/lib/index.esc':
        'export fn greet() -> string {\n    return "hi"\n}\n',
    'templates/single-package/bin/main.esc': 'console.log(greet())\n',
};

let fs: BrowserFS;

beforeEach(() => {
    fs = new BrowserFS({});

    vi.stubGlobal(
        'fetch',
        vi.fn((url: string) => {
            const path = url.replace('/', '');
            const content = fileContents[path];
            if (content !== undefined) {
                return Promise.resolve({
                    ok: true,
                    text: () => Promise.resolve(content),
                });
            }
            return Promise.resolve({
                ok: false,
                statusText: 'Not Found',
            });
        }),
    );
});

describe('loadProject', () => {
    test('loads an example and writes files to the filesystem', async () => {
        const primaryFile = await loadProject(
            'hello-world',
            'example',
            manifest,
            '/',
            fs,
        );

        expect(primaryFile).toBe('/lib/index.esc');

        const content = await readFile(fs, '/lib/index.esc');
        expect(content).toContain('export fn add');

        const binContent = await readFile(fs, '/bin/main.esc');
        expect(binContent).toContain('add(5, 10)');

        const toml = await readFile(fs, '/escalier.toml');
        expect(toml).toContain('hello-world');

        const pkg = await readFile(fs, '/package.json');
        expect(JSON.parse(pkg).name).toBe('hello-world');
    });

    test('loads a template and writes files to the filesystem', async () => {
        const primaryFile = await loadProject(
            'single-package',
            'template',
            manifest,
            '/',
            fs,
        );

        expect(primaryFile).toBe('/lib/index.esc');

        const content = await readFile(fs, '/lib/index.esc');
        expect(content).toContain('export fn greet');
    });

    test('creates parent directories for nested files', async () => {
        await loadProject('hello-world', 'example', manifest, '/', fs);

        const libStat = await stat(fs, '/lib');
        expect(libStat.isDirectory()).toBe(true);

        const binStat = await stat(fs, '/bin');
        expect(binStat.isDirectory()).toBe(true);
    });

    test('clears existing project files before writing', async () => {
        // Write a file that should be removed after loading a project
        fs.mkdir('/old-dir', () => {});
        fs.writeFile('/old-dir/stale.txt', encoder.encode('stale'), () => {});

        await loadProject('hello-world', 'example', manifest, '/', fs);

        await expect(stat(fs, '/old-dir/stale.txt')).rejects.toMatchObject({
            code: 'ENOENT',
        });
    });

    test('preserves node_modules across project loads', async () => {
        // Simulate node_modules existing before project load
        fs.mkdir('/node_modules', () => {});
        fs.mkdir('/node_modules/typescript', () => {});
        fs.writeFile(
            '/node_modules/typescript/index.d.ts',
            encoder.encode('declare module "typescript"'),
            () => {},
        );

        await loadProject('hello-world', 'example', manifest, '/', fs);

        const content = await readFile(
            fs,
            '/node_modules/typescript/index.d.ts',
        );
        expect(content).toBe('declare module "typescript"');
    });

    test('returns first .esc file when lib/index.esc is absent', async () => {
        const primaryFile = await loadProject(
            'no-lib-index',
            'example',
            manifest,
            '/',
            fs,
        );

        expect(primaryFile).toBe('/bin/main.esc');
    });

    test('returns first file when no .esc files exist', async () => {
        const primaryFile = await loadProject(
            'no-esc',
            'example',
            manifest,
            '/',
            fs,
        );

        expect(primaryFile).toBe('/escalier.toml');
    });

    test('throws for an unknown example', async () => {
        await expect(
            loadProject('nonexistent', 'example', manifest, '/', fs),
        ).rejects.toThrow('Unknown example: nonexistent');
    });

    test('throws for an unknown template', async () => {
        await expect(
            loadProject('nonexistent', 'template', manifest, '/', fs),
        ).rejects.toThrow('Unknown template: nonexistent');
    });

    test('throws when fetch fails', async () => {
        const badManifest: Manifest = {
            types: [],
            templates: {},
            examples: { broken: ['missing-file.txt'] },
        };

        await expect(
            loadProject('broken', 'example', badManifest, '/', fs),
        ).rejects.toThrow('Failed to fetch');
    });

    test('constructs correct fetch URLs with baseUrl', async () => {
        // Stub fetch to accept any URL and return dummy content
        vi.stubGlobal(
            'fetch',
            vi.fn(() =>
                Promise.resolve({
                    ok: true,
                    text: () => Promise.resolve('content'),
                }),
            ),
        );

        await loadProject(
            'hello-world',
            'example',
            manifest,
            '/playground/',
            fs,
        );

        expect(fetch).toHaveBeenCalledWith(
            '/playground/examples/hello-world/escalier.toml',
        );
        expect(fetch).toHaveBeenCalledWith(
            '/playground/examples/hello-world/lib/index.esc',
        );
    });

    test('switching projects replaces all files', async () => {
        await loadProject('hello-world', 'example', manifest, '/', fs);
        const rootBefore = await readdir(fs, '/');
        expect(rootBefore).toContain('lib');

        await loadProject('no-esc', 'example', manifest, '/', fs);

        // lib/ and bin/ should no longer exist
        await expect(stat(fs, '/lib')).rejects.toMatchObject({
            code: 'ENOENT',
        });
        await expect(stat(fs, '/bin')).rejects.toMatchObject({
            code: 'ENOENT',
        });

        // New project files should exist
        const toml = await readFile(fs, '/escalier.toml');
        expect(toml).toContain('no-esc');
    });
});
