// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { openExample, openSnapshot } from './initial-project';
import { collectProjectFiles, encodeSnapshot } from './permalink';

let fs: BrowserFS;

/** Returns the text of every project file in fs, keyed by path. */
function projectFiles(): Record<string, string> {
    return collectProjectFiles(fs.rootDir, () => undefined);
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

describe('openSnapshot', () => {
    test('writes the files and opens the tabs of a snapshot', async () => {
        const encoded = await encodeSnapshot({
            files: { '/lib/index.esc': 'export val x = 1' },
            left: { paths: ['/lib/index.esc'], active: 0 },
            right: { paths: ['/build/lib/index.js'], active: 0 },
        });

        expect(await openSnapshot(encoded, fs)).toBe(true);

        expect(projectFiles()).toEqual({
            '/lib/index.esc': 'export val x = 1',
        });
        const state = useEditorStore.getState();
        expect(state.leftTabs).toEqual([{ path: '/lib/index.esc' }]);
        expect(state.rightTabs).toEqual([{ path: '/build/lib/index.js' }]);
        expect(state.activeRightTabIndex).toBe(0);
    });

    test('says why a damaged snapshot fails and drops it from the address bar', async () => {
        history.replaceState(null, '', '/#project=damaged');

        expect(await openSnapshot('damaged', fs)).toBe(false);

        expect(window.location.hash).toBe('');
        expect(useEditorStore.getState().notification).toEqual({
            message:
                'Could not open the project in this link. The link is damaged or incomplete. Loading an example instead.',
            type: 'error',
        });
    });
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
