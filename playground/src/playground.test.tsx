// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import {
    afterEach,
    beforeAll,
    beforeEach,
    describe,
    expect,
    test,
    vi,
} from 'vitest';

vi.mock('monaco-editor-core');
vi.mock('./language', () => ({ languageID: 'escalier' }));
vi.mock('./capture-permalink', () => ({ capturePermalink: vi.fn() }));

import * as monaco from 'monaco-editor-core';

import { capturePermalink } from './capture-permalink';
import { stubModalDialogs } from './components/dialog-stub';
import { initialEditorState } from './editor-state';
import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { MAX_PERMALINK_CHARS } from './permalink';
import { Playground, oversizedLinkMessage, slugToLabel } from './playground';
import { initialPlaygroundState } from './playground-state';
import { usePlaygroundStore } from './playground-store';
import { ERRORS_TAB_PATH } from './project-errors';
import { useProjectErrorsStore } from './project-errors-store';

describe('slugToLabel', () => {
    test.each([
        ['hello-world', 'Hello world'],
        ['calculator', 'Calculator'],
        ['overloads-and-generators', 'Overloads and generators'],
    ])('labels %s as %s', (slug, label) => {
        expect(slugToLabel(slug)).toBe(label);
    });
});

test('oversizedLinkMessage gives the link length and the limit', () => {
    expect(oversizedLinkMessage(45_678)).toBe(
        "A link to this project would be 45,678 characters long, but the playground makes links of at most 32,000 characters. Remove the files and code that the problem doesn't need, then share again.",
    );
});

const manifest: Manifest = {
    types: [],
    templates: {},
    examples: {},
    builtins: [],
};

function renderPlayground(layoutRestored = false) {
    render(
        <Playground
            fs={new BrowserFS({})}
            manifest={manifest}
            baseUrl="/"
            layoutRestored={layoutRestored}
        />,
    );
}

describe('Playground', () => {
    beforeAll(stubModalDialogs);

    beforeEach(() => {
        useEditorStore.setState({ ...initialEditorState });
        usePlaygroundStore.setState({ ...initialPlaygroundState });
        useProjectErrorsStore.setState({ errors: {} });
        vi.spyOn(console, 'error').mockImplementation(() => {});
    });

    afterEach(() => {
        cleanup();
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
        history.replaceState(null, '', '/');
    });

    test('Share copies the link and shows it in the address bar', async () => {
        const link = `${window.location.origin}/#project=abc`;
        vi.mocked(capturePermalink).mockResolvedValue(link);
        const writeText = vi.fn().mockResolvedValue(undefined);
        vi.stubGlobal('navigator', { clipboard: { writeText } });
        renderPlayground();

        fireEvent.click(screen.getByRole('button', { name: 'Share' }));

        expect(await screen.findByRole('alert')).toHaveTextContent(
            'Copied a link to this project.',
        );
        expect(writeText).toHaveBeenCalledWith(link);
        expect(window.location.href).toBe(link);
    });

    test('Share says to copy from the address bar when the clipboard refuses', async () => {
        const link = `${window.location.origin}/#project=abc`;
        vi.mocked(capturePermalink).mockResolvedValue(link);
        vi.stubGlobal('navigator', {
            clipboard: {
                writeText: vi.fn().mockRejectedValue(new Error('denied')),
            },
        });
        renderPlayground();

        fireEvent.click(screen.getByRole('button', { name: 'Share' }));

        expect(await screen.findByRole('alert')).toHaveTextContent(
            'Could not copy a link to this project. If the address bar shows a #project link, copy it from there.',
        );
        expect(window.location.href).toBe(link);
    });

    test('Share explains a link that is too long and copies nothing', async () => {
        const link = `${window.location.origin}/#project=${'a'.repeat(MAX_PERMALINK_CHARS)}`;
        vi.mocked(capturePermalink).mockResolvedValue(link);
        const writeText = vi.fn();
        vi.stubGlobal('navigator', { clipboard: { writeText } });
        renderPlayground();

        fireEvent.click(screen.getByRole('button', { name: 'Share' }));

        const dialog = await screen.findByRole('dialog', {
            name: 'Project too large to share',
        });
        expect(dialog).toHaveAccessibleDescription(
            oversizedLinkMessage(link.length),
        );
        expect(writeText).not.toHaveBeenCalled();
        expect(window.location.hash).toBe('');

        fireEvent.click(screen.getByRole('button', { name: 'OK' }));
        expect(
            screen.queryByRole('dialog', {
                name: 'Project too large to share',
            }),
        ).not.toBeInTheDocument();
    });

    test('choosing an example after a permalink drops the link and waits for the compile', async () => {
        vi.stubGlobal(
            'fetch',
            vi.fn(() =>
                Promise.resolve({
                    ok: true,
                    text: () => Promise.resolve('// enums'),
                }),
            ),
        );
        history.replaceState(null, '', '/#project=abc');
        render(
            <Playground
                fs={new BrowserFS({})}
                manifest={{
                    ...manifest,
                    examples: { enums: ['lib/index.esc'] },
                }}
                baseUrl="/"
                layoutRestored
            />,
        );
        expect(usePlaygroundStore.getState().initialCompileDone).toBe(true);

        fireEvent.click(screen.getByRole('button', { name: /Examples/ }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Enums' }));
        fireEvent.click(screen.getByRole('button', { name: 'Replace' }));

        await vi.waitFor(() =>
            expect(window.location.search).toBe('?example=enums'),
        );
        expect(window.location.hash).toBe('');
        expect(usePlaygroundStore.getState().initialCompileDone).toBe(false);
    });

    test('Report a bug opens a report with the errors and the latest crash', async () => {
        vi.mocked(capturePermalink).mockResolvedValue(
            `${window.location.origin}/#project=abc`,
        );
        vi.mocked(monaco.editor.getModelMarkers).mockReturnValue([
            {
                resource: { path: '/lib/index.esc' },
                severity: monaco.MarkerSeverity.Error,
                message: 'bad',
                startLineNumber: 1,
                startColumn: 2,
            },
        ] as unknown as monaco.editor.IMarker[]);
        useEditorStore.setState({
            leftTabs: [{ path: '/lib/index.esc' }],
            lastCrashReport: { headline: 'panic: boom', text: 'panic: boom' },
        });
        renderPlayground();

        fireEvent.click(screen.getByRole('button', { name: 'Report a bug' }));

        expect(
            await screen.findByRole('dialog', { name: 'Report a bug' }),
        ).toHaveTextContent(
            'The report also holds 1 error shown in the open files, the latest crash report and your browser and the playground build.',
        );
        const issueLink = await screen.findByRole('link', {
            name: 'Open GitHub issue',
        });
        const fields = new URL(issueLink.getAttribute('href') ?? '')
            .searchParams;
        expect(fields.get('errors')).toBe('/lib/index.esc:1:2: bad');
        expect(fields.get('environment')).toMatch(
            /^Browser: .+\nPlayground build: unknown$/,
        );

        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        expect(
            screen.queryByRole('dialog', { name: 'Report a bug' }),
        ).not.toBeInTheDocument();
    });

    test("the status bar counts the project's errors and opens them in a tab", () => {
        const error = (path: string) => ({
            path,
            line: 1,
            column: 1,
            message: 'bad',
        });
        useProjectErrorsStore.setState({
            errors: {
                '/lib/a.esc': [error('/lib/a.esc'), error('/lib/a.esc')],
                '/lib/b.esc': [error('/lib/b.esc')],
            },
        });
        renderPlayground();

        fireEvent.click(screen.getByRole('button', { name: '3 errors' }));

        expect(
            useEditorStore.getState().leftTabs.map((tab) => tab.path),
        ).toContain(ERRORS_TAB_PATH);
        expect(screen.getByRole('tab', { name: /^Errors/ })).toHaveAttribute(
            'aria-selected',
            'true',
        );
    });

    test('restored tabs show without waiting for the compile', () => {
        renderPlayground(true);

        expect(usePlaygroundStore.getState().initialCompileDone).toBe(true);
    });

    test('tabs from a project wait for the compile', () => {
        renderPlayground(false);

        expect(usePlaygroundStore.getState().initialCompileDone).toBe(false);
    });
});
