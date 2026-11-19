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
        "A link to this project would be 45,678 characters, and a link can hold at most 32,000. Remove the files and code that the problem doesn't need, then share again.",
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

    test('restored tabs show without waiting for the compile', () => {
        renderPlayground(true);

        expect(usePlaygroundStore.getState().initialCompileDone).toBe(true);
    });

    test('tabs from a project wait for the compile', () => {
        renderPlayground(false);

        expect(usePlaygroundStore.getState().initialCompileDone).toBe(false);
    });
});
