// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

import type { BrowserFS } from '../fs/browser-fs';
import { FSEventEmitter } from '../fs/fs-events';
import type { FSDir } from '../fs/fs-node';
import { FileExplorer } from './file-explorer';

type Callback = (err: Error | null) => void;

/** Returns a filesystem holding /lib/main.esc whose writes all succeed. */
function makeFakeFS() {
    const rootDir: FSDir = {
        type: 'dir',
        name: '/',
        children: new Map([
            [
                'lib',
                {
                    type: 'dir',
                    name: 'lib',
                    children: new Map([
                        [
                            'main.esc',
                            {
                                type: 'file',
                                name: 'main.esc',
                                content: new Uint8Array(),
                            },
                        ],
                    ]),
                },
            ],
        ]),
    };
    const fs = {
        rootDir,
        events: new FSEventEmitter(),
        writeFile: vi.fn((_path: string, _data: Uint8Array, cb: Callback) =>
            cb(null),
        ),
        mkdir: vi.fn((_path: string, cb: Callback) => cb(null)),
        rename: vi.fn((_from: string, _to: string, cb: Callback) => cb(null)),
        unlink: vi.fn((_path: string, cb: Callback) => cb(null)),
        rmdir: vi.fn((_path: string, cb: Callback) => cb(null)),
    };
    return fs;
}

function renderExplorer() {
    const fs = makeFakeFS();
    const props = {
        onFileOpen: vi.fn(),
        onFileDelete: vi.fn(),
        onFileRename: vi.fn(),
        onFilesChange: vi.fn(),
    };
    render(<FileExplorer fs={fs as unknown as BrowserFS} {...props} />);
    return { fs, ...props };
}

/** Types name into the inline name input and presses Enter. */
function submitName(input: HTMLElement, name: string) {
    fireEvent.change(input, { target: { value: name } });
    fireEvent.keyDown(input, { key: 'Enter' });
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

afterEach(cleanup);

describe('FileExplorer reports changes to the files', () => {
    test('after creating a file', () => {
        const { fs, onFilesChange, onFileOpen } = renderExplorer();

        fireEvent.click(screen.getByLabelText('New File'));
        submitName(screen.getByLabelText('New file name'), 'notes.esc');

        expect(fs.writeFile).toHaveBeenCalled();
        expect(onFilesChange).toHaveBeenCalledTimes(1);
        expect(onFileOpen).toHaveBeenCalledWith('/notes.esc');
    });

    test('after creating a folder', () => {
        const { fs, onFilesChange } = renderExplorer();

        fireEvent.click(screen.getByLabelText('New Folder'));
        submitName(screen.getByLabelText('New dir name'), 'src');

        expect(fs.mkdir).toHaveBeenCalled();
        expect(onFilesChange).toHaveBeenCalledTimes(1);
    });

    test('after renaming a file', () => {
        const { fs, onFilesChange, onFileRename } = renderExplorer();

        fireEvent.contextMenu(screen.getByRole('button', { name: 'main.esc' }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Rename' }));
        submitName(screen.getByLabelText('Rename main.esc'), 'app.esc');

        expect(fs.rename).toHaveBeenCalled();
        expect(onFilesChange).toHaveBeenCalledTimes(1);
        expect(onFileRename).toHaveBeenCalledWith(
            '/lib/main.esc',
            '/lib/app.esc',
        );
    });

    test('after deleting a file', () => {
        const { fs, onFilesChange, onFileDelete } = renderExplorer();

        fireEvent.contextMenu(screen.getByRole('button', { name: 'main.esc' }));
        fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));
        fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

        expect(fs.unlink).toHaveBeenCalled();
        expect(onFilesChange).toHaveBeenCalledTimes(1);
        expect(onFileDelete).toHaveBeenCalledWith('/lib/main.esc');
    });
});
