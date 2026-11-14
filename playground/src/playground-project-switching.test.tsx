// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import {
    cleanup,
    fireEvent,
    render,
    screen,
    waitFor,
} from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, beforeAll, beforeEach, expect, test, vi } from 'vitest';

vi.mock('monaco-editor-core');

// The editor stub renders the toolbar and a button that reports an edit, so a
// test can drive the playground the way the real editor does.
vi.mock('./editor', () => ({
    Editor: ({
        toolbar,
        onProjectModified,
    }: {
        toolbar?: ReactNode;
        onProjectModified?: () => void;
    }) => (
        <div>
            {toolbar}
            <button type="button" onClick={() => onProjectModified?.()}>
                Edit
            </button>
        </div>
    ),
}));

vi.mock('./project-loader', () => ({
    loadProject: vi.fn(async () => '/lib/index.esc'),
}));

import type { BrowserFS } from './fs/browser-fs';
import { FSEventEmitter } from './fs/fs-events';
import type { Manifest } from './fs/volume';
import { Playground } from './playground';
import { usePlaygroundStore } from './playground-store';
import { loadProject } from './project-loader';

const manifest: Manifest = {
    types: [],
    templates: { basic: ['lib/index.esc'] },
    examples: { enums: ['lib/index.esc'] },
};

function renderPlayground() {
    const fs = {
        events: new FSEventEmitter(),
        // No build output exists, so the playground waits for its first compile.
        stat: (_path: string, cb: (err: Error | null) => void) =>
            cb(new Error('ENOENT')),
    } as unknown as BrowserFS;
    render(<Playground fs={fs} manifest={manifest} baseUrl="/" />);
}

/** Picks the Enums example from the Examples menu. */
function pickEnums() {
    fireEvent.click(screen.getByRole('button', { name: /Examples/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Enums' }));
}

beforeAll(() => {
    // jsdom has no modal dialog support, so opening one sets the `open`
    // attribute the way a browser does.
    HTMLDialogElement.prototype.showModal = function () {
        this.setAttribute('open', '');
    };
    HTMLDialogElement.prototype.close = function () {
        this.removeAttribute('open');
    };
});

beforeEach(() => {
    usePlaygroundStore.setState(usePlaygroundStore.getInitialState(), true);
    vi.mocked(loadProject).mockClear();
});

afterEach(cleanup);

test('loads an example without asking until the project is edited', async () => {
    renderPlayground();

    pickEnums();

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(loadProject).toHaveBeenCalledWith(
        'enums',
        'example',
        manifest,
        '/',
        expect.anything(),
    );
});

test('asks after an edit, and stops asking once the new project loads', async () => {
    renderPlayground();

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect(usePlaygroundStore.getState().projectModified).toBe(true);

    pickEnums();
    expect(loadProject).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Replace' }));

    await waitFor(() =>
        expect(usePlaygroundStore.getState().projectModified).toBe(false),
    );
    expect(loadProject).toHaveBeenCalledTimes(1);
});

test('clears the modified flag once a template loads', async () => {
    renderPlayground();

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.click(screen.getByRole('button', { name: /New Project/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Basic' }));
    fireEvent.click(screen.getByRole('button', { name: 'Replace' }));

    await waitFor(() =>
        expect(usePlaygroundStore.getState().projectModified).toBe(false),
    );
    expect(loadProject).toHaveBeenCalledWith(
        'basic',
        'template',
        manifest,
        '/',
        expect.anything(),
    );
});
