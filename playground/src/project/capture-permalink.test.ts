import * as monaco from 'monaco-editor-core';
import { afterEach, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

import { DIAGNOSTICS_TAB_PATH } from '#diagnostics/project-diagnostics';
import { initialEditorState } from '#editor/editor-state';
import { useEditorStore } from '#editor/editor-store';
import { BrowserFS } from '#fs/browser-fs';
import { capturePermalink } from './capture-permalink';
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

test('records the diagnostics tab by its path and none of its diagnostics', async () => {
    const fs = new BrowserFS({});
    writeProjectFiles(fs, [
        { path: '/lib/index.esc', content: 'export val x = 1' },
    ]);
    // The editor's model for the tab holds the diagnostics it shows.
    vi.mocked(monaco.editor.getModel).mockImplementation(
        (uri) =>
            (String(uri) === `file://${DIAGNOSTICS_TAB_PATH}`
                ? { getValue: () => '/lib/index.esc:1:1: error: bad\n' }
                : null) as ReturnType<typeof monaco.editor.getModel>,
    );
    useEditorStore.setState({
        ...initialEditorState,
        leftTabs: [{ path: '/lib/index.esc' }, { path: DIAGNOSTICS_TAB_PATH }],
        activeLeftTabIndex: 1,
        rightTabs: [],
        activeRightTabIndex: null,
    });

    const url = await capturePermalink(fs, 'https://example.com/');

    expect(
        await decodeSnapshot(snapshotParam(new URL(url).hash) ?? ''),
    ).toEqual({
        files: { '/lib/index.esc': 'export val x = 1' },
        left: { paths: ['/lib/index.esc', DIAGNOSTICS_TAB_PATH], active: 1 },
        right: { paths: [], active: null },
    });
});
