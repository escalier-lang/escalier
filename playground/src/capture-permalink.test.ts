import * as monaco from 'monaco-editor-core';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

import { capturePermalink, fileTabsLayout } from './capture-permalink';
import { type Tab, type TabLayout, initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import { decodeSnapshot, snapshotParam } from './permalink';
import { DIAGNOSTICS_TAB_PATH } from './project-diagnostics';
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

describe('fileTabsLayout', () => {
    const tab = (path: string) => ({ path });
    const a = tab('/lib/a.esc');
    const b = tab('/lib/b.esc');
    const diagnostics = tab(DIAGNOSTICS_TAB_PATH);

    test.each<[string, Tab[], number | null, TabLayout]>([
        [
            'keeps tabs without the diagnostics tab',
            [a, b],
            1,
            { paths: [a.path, b.path], active: 1 },
        ],
        [
            'keeps an active tab before the diagnostics tab',
            [a, diagnostics, b],
            0,
            { paths: [a.path, b.path], active: 0 },
        ],
        [
            'shifts an active tab after the diagnostics tab',
            [a, diagnostics, b],
            2,
            { paths: [a.path, b.path], active: 1 },
        ],
        [
            'activates the tab before an active diagnostics tab',
            [a, diagnostics, b],
            1,
            { paths: [a.path, b.path], active: 0 },
        ],
        [
            'activates the tab after an active first diagnostics tab',
            [diagnostics, a],
            0,
            { paths: [a.path], active: 0 },
        ],
        [
            'leaves no active tab when only the diagnostics tab was open',
            [diagnostics],
            0,
            { paths: [], active: null },
        ],
    ])('%s', (_name, tabs, active, layout) => {
        expect(fileTabsLayout(tabs, active)).toEqual(layout);
    });
});
