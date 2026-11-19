import * as monaco from 'monaco-editor-core';
import { afterEach, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

import { capturePermalink } from './capture-permalink';
import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import { decodeSnapshot, snapshotParam } from './permalink';
import { writeProjectFiles } from './project-loader';

afterEach(() => {
    vi.mocked(monaco.editor.getModel).mockReset();
});

test("captures the project with the editor's text and the open tabs", async () => {
    const fs = new BrowserFS({});
    writeProjectFiles(fs, [
        { path: '/lib/index.esc', content: 'on disk' },
        { path: '/package.json', content: '{}' },
        { path: '/build/lib/index.js', content: 'output' },
    ]);
    vi.mocked(monaco.editor.getModel).mockImplementation(
        (uri) =>
            (String(uri) === 'file:///lib/index.esc'
                ? { getValue: () => 'edited' }
                : null) as ReturnType<typeof monaco.editor.getModel>,
    );
    useEditorStore.setState({
        ...initialEditorState,
        leftTabs: [{ path: '/lib/index.esc' }, { path: '/package.json' }],
        activeLeftTabIndex: 1,
        rightTabs: [{ path: '/build/lib/index.js' }],
        activeRightTabIndex: 0,
    });

    const url = await capturePermalink(
        fs,
        'https://example.com/playground/?example=enums',
    );

    expect(url).toMatch(
        /^https:\/\/example\.com\/playground\/#project=[A-Za-z0-9_-]+$/,
    );
    const encoded = snapshotParam(new URL(url).hash) ?? '';
    expect(await decodeSnapshot(encoded)).toEqual({
        files: { '/lib/index.esc': 'edited', '/package.json': '{}' },
        left: { paths: ['/lib/index.esc', '/package.json'], active: 1 },
        right: { paths: ['/build/lib/index.js'], active: 0 },
    });
});
