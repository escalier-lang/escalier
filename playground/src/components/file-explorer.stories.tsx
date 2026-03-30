import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';

import type { BrowserFS } from '../fs/browser-fs';
import { FSEventEmitter } from '../fs/fs-events';
import type { FSDir } from '../fs/fs-node';

import { FileExplorer } from './file-explorer';

const fileOpenSpy = fn();

function makeFakeFS(rootDir: FSDir): BrowserFS {
    return { rootDir, events: new FSEventEmitter() } as unknown as BrowserFS;
}

const simpleRoot: FSDir = {
    type: 'dir',
    name: '/',
    children: new Map([
        [
            'bin',
            {
                type: 'dir',
                name: 'bin',
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
        [
            'lib',
            {
                type: 'dir',
                name: 'lib',
                children: new Map([
                    [
                        'utils.esc',
                        {
                            type: 'file',
                            name: 'utils.esc',
                            content: new Uint8Array(),
                        },
                    ],
                    [
                        'math.esc',
                        {
                            type: 'file',
                            name: 'math.esc',
                            content: new Uint8Array(),
                        },
                    ],
                ]),
            },
        ],
        [
            'package.json',
            {
                type: 'file',
                name: 'package.json',
                content: new Uint8Array(),
            },
        ],
        [
            'escalier.toml',
            {
                type: 'file',
                name: 'escalier.toml',
                content: new Uint8Array(),
            },
        ],
    ]),
};

const deepRoot: FSDir = {
    type: 'dir',
    name: '/',
    children: new Map([
        [
            'bin',
            {
                type: 'dir',
                name: 'bin',
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
        [
            'build',
            {
                type: 'dir',
                name: 'build',
                children: new Map([
                    [
                        'bin',
                        {
                            type: 'dir',
                            name: 'bin',
                            children: new Map([
                                [
                                    'main.js',
                                    {
                                        type: 'file',
                                        name: 'main.js',
                                        content: new Uint8Array(),
                                    },
                                ],
                            ]),
                        },
                    ],
                ]),
            },
        ],
        [
            'node_modules',
            {
                type: 'dir',
                name: 'node_modules',
                children: new Map([
                    [
                        'typescript',
                        {
                            type: 'dir',
                            name: 'typescript',
                            children: new Map([
                                [
                                    'package.json',
                                    {
                                        type: 'file',
                                        name: 'package.json',
                                        content: new Uint8Array(),
                                    },
                                ],
                            ]),
                        },
                    ],
                ]),
            },
        ],
        [
            'package.json',
            {
                type: 'file',
                name: 'package.json',
                content: new Uint8Array(),
            },
        ],
    ]),
};

const emptyRoot: FSDir = {
    type: 'dir',
    name: '/',
    children: new Map(),
};

const meta = {
    title: 'Components/FileExplorer',
    component: FileExplorer,
    decorators: [
        (Story) => (
            <div style={{ width: 220, height: 400 }}>
                <Story />
            </div>
        ),
    ],
    beforeEach: () => {
        fileOpenSpy.mockClear();
    },
} satisfies Meta<typeof FileExplorer>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SimpleProject: Story = {
    args: {
        fs: makeFakeFS(simpleRoot),
        onFileOpen: fileOpenSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        // Header is present
        await expect(canvas.getByText('EXPLORER')).toBeVisible();

        // Directories first (alphabetical), then files (alphabetical)
        const topLevel = ['bin', 'lib', 'escalier.toml', 'package.json'];
        const nodes = topLevel.map((name) => canvas.getByText(name));
        for (let i = 0; i < nodes.length - 1; i++) {
            // Earlier in DOM order means the node comes before the next one
            const position = nodes[i].compareDocumentPosition(nodes[i + 1]);
            await expect(
                position & Node.DOCUMENT_POSITION_FOLLOWING,
            ).toBeTruthy();
        }

        // Directories are expanded by default, showing children
        await expect(canvas.getByText('main.esc')).toBeVisible();
        await expect(canvas.getByText('math.esc')).toBeVisible();
        await expect(canvas.getByText('utils.esc')).toBeVisible();
    },
};

export const ClickFile: Story = {
    args: {
        fs: makeFakeFS(simpleRoot),
        onFileOpen: fileOpenSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        // Click a file to open it
        await userEvent.click(canvas.getByText('main.esc'));
        await expect(fileOpenSpy).toHaveBeenCalledTimes(1);
        await expect(fileOpenSpy).toHaveBeenCalledWith('/bin/main.esc');
    },
};

export const CollapseDirectory: Story = {
    args: {
        fs: makeFakeFS(simpleRoot),
        onFileOpen: fileOpenSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        // lib directory children are visible
        await expect(canvas.getByText('utils.esc')).toBeVisible();

        // Click the lib directory to collapse it
        await userEvent.click(canvas.getByText('lib'));

        // Children should no longer be visible
        expect(canvas.queryByText('utils.esc')).toBeNull();
        expect(canvas.queryByText('math.esc')).toBeNull();

        // Click again to expand
        await userEvent.click(canvas.getByText('lib'));
        await expect(canvas.getByText('utils.esc')).toBeVisible();
    },
};

export const WithBuildAndNodeModules: Story = {
    args: {
        fs: makeFakeFS(deepRoot),
        onFileOpen: fileOpenSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        // build and node_modules directories start collapsed
        await expect(canvas.getByText('build')).toBeVisible();
        await expect(canvas.getByText('node_modules')).toBeVisible();

        // Their children are not visible
        expect(canvas.queryByText('main.js')).toBeNull();
        expect(canvas.queryByText('typescript')).toBeNull();

        // Expand build directory
        await userEvent.click(canvas.getByText('build'));
        // build/bin is now visible alongside the top-level bin
        await expect(canvas.getAllByText('bin')).toHaveLength(2);
    },
};

export const EmptyProject: Story = {
    args: {
        fs: makeFakeFS(emptyRoot),
        onFileOpen: fileOpenSpy,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        // Header is still present
        await expect(canvas.getByText('EXPLORER')).toBeVisible();

        // No files or directories rendered (just the header)
        expect(canvas.queryAllByRole('button')).toHaveLength(0);
    },
};
