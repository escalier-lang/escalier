import { describe, expect, test } from 'vitest';

import type { FSDir, FSNode } from './fs/fs-node';
import {
    type PlaygroundSnapshot,
    collectProjectFiles,
    decodeSnapshot,
    encodeSnapshot,
    permalinkUrl,
    snapshotParam,
} from './permalink';

const snapshot: PlaygroundSnapshot = {
    files: {
        '/package.json': '{ "name": "demo" }',
        '/lib/index.esc': 'export val greeting = "héllo 👋"\n',
    },
    left: { paths: ['/lib/index.esc', '/package.json'], active: 1 },
    right: { paths: ['/build/lib/index.js'], active: 0 },
};

/** Returns value as the compressed, URL-safe text a permalink carries. */
async function encodeRaw(value: unknown): Promise<string> {
    const stream = new Blob([JSON.stringify(value)])
        .stream()
        .pipeThrough(new CompressionStream('deflate-raw'));
    const bytes = new Uint8Array(await new Response(stream).arrayBuffer());
    return btoa(String.fromCharCode(...bytes))
        .replace(/\+/g, '-')
        .replace(/\//g, '_')
        .replace(/=+$/, '');
}

function dir(name: string, children: FSNode[]): FSDir {
    return {
        type: 'dir',
        name,
        children: new Map(children.map((child) => [child.name, child])),
    };
}

function file(name: string, text: string): FSNode {
    return { type: 'file', name, content: new TextEncoder().encode(text) };
}

test('decodeSnapshot returns the snapshot encodeSnapshot encoded', async () => {
    const encoded = await encodeSnapshot(snapshot);

    expect(encoded).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(await decodeSnapshot(encoded)).toEqual(snapshot);
});

describe('collectProjectFiles', () => {
    const root = dir('', [
        file('package.json', '{}'),
        dir('lib', [file('index.esc', 'on disk')]),
        dir('build', [dir('lib', [file('index.js', 'output')])]),
        dir('node_modules', [file('lib.esc', 'standard library')]),
        dir('packages', [
            dir('foo', [
                file('package.json', '{ "name": "foo" }'),
                dir('build', [file('index.js', 'output')]),
            ]),
        ]),
    ]);

    test('returns the source files and skips node_modules and build output', () => {
        expect(collectProjectFiles(root, () => undefined)).toEqual({
            '/package.json': '{}',
            '/lib/index.esc': 'on disk',
            '/packages/foo/package.json': '{ "name": "foo" }',
        });
    });

    test("takes a file's text from currentText when it returns one", () => {
        const files = collectProjectFiles(root, (path) =>
            path === '/lib/index.esc' ? 'edited' : undefined,
        );

        expect(files['/lib/index.esc']).toBe('edited');
        expect(files['/package.json']).toBe('{}');
    });
});

describe('decodeSnapshot rejects', () => {
    const layouts = {
        left: { paths: ['/lib/index.esc'], active: 0 },
        right: { paths: [], active: null },
    };

    test('text that is not an encoded snapshot', async () => {
        await expect(decodeSnapshot('not*a*snapshot')).rejects.toThrow(
            new Error('The link is damaged or incomplete.'),
        );
    });

    test.each([
        {
            name: 'a value that is not an object',
            value: [],
            message: 'The link holds no project.',
        },
        {
            name: 'another format version',
            value: { v: 2, files: {}, ...layouts },
            message:
                'The link uses format version 2, which this playground does not read.',
        },
        {
            name: 'a missing file list',
            value: { v: 1, ...layouts },
            message: 'The link holds no project files.',
        },
        {
            name: 'a file in node_modules',
            value: { v: 1, files: { '/node_modules/x.esc': '' }, ...layouts },
            message:
                'The link holds a file at /node_modules/x.esc, outside the project.',
        },
        {
            name: 'a build file',
            value: { v: 1, files: { '/build/lib/index.js': '' }, ...layouts },
            message:
                'The link holds a file at /build/lib/index.js, outside the project.',
        },
        {
            name: 'a relative path',
            value: { v: 1, files: { 'lib/index.esc': '' }, ...layouts },
            message:
                'The link holds a file at lib/index.esc, outside the project.',
        },
        {
            name: 'a path that climbs out with ..',
            value: { v: 1, files: { '/lib/../../x.esc': '' }, ...layouts },
            message:
                'The link holds a file at /lib/../../x.esc, outside the project.',
        },
        {
            name: 'a file without text',
            value: { v: 1, files: { '/lib/index.esc': 1 }, ...layouts },
            message: 'The link holds no text for /lib/index.esc.',
        },
        {
            name: 'a missing tab layout',
            value: { v: 1, files: {}, left: layouts.left },
            message: 'The link holds no tabs for the right side.',
        },
        {
            name: 'a tab path that is not a string',
            value: {
                v: 1,
                files: {},
                ...layouts,
                left: { paths: [1], active: 0 },
            },
            message: 'The link holds no tabs for the left side.',
        },
        {
            name: 'a tab outside the project',
            value: {
                v: 1,
                files: {},
                ...layouts,
                left: { paths: ['/node_modules/lib.esc'], active: 0 },
            },
            message:
                'The link opens a tab for /node_modules/lib.esc, outside the project.',
        },
        {
            name: 'a relative tab path',
            value: {
                v: 1,
                files: {},
                ...layouts,
                left: { paths: ['lib/index.esc'], active: 0 },
            },
            message:
                'The link opens a tab for lib/index.esc, outside the project.',
        },
        {
            name: 'the same tab twice on one side',
            value: {
                v: 1,
                files: {},
                ...layouts,
                right: {
                    paths: ['/build/lib/index.js', '/build/lib/index.js'],
                    active: 0,
                },
            },
            message: 'The link opens the same file twice on the right side.',
        },
        {
            name: 'an active tab past the last tab',
            value: {
                v: 1,
                files: {},
                ...layouts,
                left: { paths: ['/lib/index.esc'], active: 1 },
            },
            message: 'The link names no open tab on the left side.',
        },
        {
            name: 'no active tab on a side with tabs',
            value: {
                v: 1,
                files: {},
                ...layouts,
                right: { paths: ['/build/lib/index.js'], active: null },
            },
            message: 'The link names no open tab on the right side.',
        },
    ])('$name', async ({ value, message }) => {
        await expect(decodeSnapshot(await encodeRaw(value))).rejects.toThrow(
            new Error(message),
        );
    });
});

test.each([
    { hash: '#project=abc_-', expected: 'abc_-' },
    { hash: 'project=abc', expected: 'abc' },
    { hash: '#other=1&project=abc', expected: 'abc' },
    { hash: '', expected: null },
    { hash: '#other=1', expected: null },
])('snapshotParam($hash) returns $expected', ({ hash, expected }) => {
    expect(snapshotParam(hash)).toBe(expected);
});

test('permalinkUrl replaces the query and hash with the snapshot', () => {
    expect(
        permalinkUrl(
            'https://example.com/playground/?example=enums#old',
            'abc_-',
        ),
    ).toBe('https://example.com/playground/#project=abc_-');
});
