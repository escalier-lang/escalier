// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import type { FSDir } from './fs/fs-node';
import type { Manifest } from './fs/volume';
import { openExample } from './initial-project';

let fs: BrowserFS;

/** Returns the text of every file in fs, keyed by path. */
function projectFiles(): Record<string, string> {
    const files: Record<string, string> = {};
    const visit = (dir: FSDir, dirPath: string) => {
        for (const [name, node] of dir.children) {
            const path = `${dirPath}/${name}`;
            if (node.type === 'dir') {
                visit(node, path);
            } else if (node.type === 'file') {
                files[path] = new TextDecoder().decode(node.content);
            }
        }
    };
    visit(fs.rootDir, '');
    return files;
}

beforeEach(() => {
    fs = new BrowserFS({});
    useEditorStore.setState({ ...initialEditorState });
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    history.replaceState(null, '', '/');
});

describe('openExample', () => {
    const manifest: Manifest = {
        types: [],
        templates: {},
        examples: {
            'hello-world': ['lib/index.esc'],
            enums: ['lib/index.esc', 'bin/main.esc'],
        },
        builtins: [],
    };

    beforeEach(() => {
        vi.stubGlobal(
            'fetch',
            vi.fn((url: string) =>
                Promise.resolve({
                    ok: true,
                    text: () => Promise.resolve(`// ${url}`),
                }),
            ),
        );
    });

    test('loads the example the query names', async () => {
        history.replaceState(null, '', '/?example=enums');

        await openExample(manifest, '/', fs);

        expect(projectFiles()).toEqual({
            '/lib/index.esc': '// /examples/enums/lib/index.esc',
            '/bin/main.esc': '// /examples/enums/bin/main.esc',
        });
        expect(useEditorStore.getState().leftTabs).toEqual([
            { path: '/lib/index.esc' },
        ]);
    });

    test('loads Hello World in place of an unknown example and says so', async () => {
        history.replaceState(null, '', '/?example=missing');

        await openExample(manifest, '/', fs);

        expect(projectFiles()).toEqual({
            '/lib/index.esc': '// /examples/hello-world/lib/index.esc',
        });
        expect(window.location.search).toBe('?example=hello-world');
        expect(useEditorStore.getState().notification).toEqual({
            message: 'Unknown example "missing". Loading Hello World instead.',
            type: 'warning',
        });
    });

    test('says so when the example fails to load', async () => {
        vi.stubGlobal(
            'fetch',
            vi.fn(() =>
                Promise.resolve({ ok: false, statusText: 'Not Found' }),
            ),
        );

        await openExample(manifest, '/', fs);

        expect(useEditorStore.getState().notification).toEqual({
            message:
                'Failed to load initial project: Failed to fetch /examples/hello-world/lib/index.esc: Not Found',
            type: 'error',
        });
    });
});
