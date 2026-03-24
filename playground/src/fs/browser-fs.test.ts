import type { Stats } from 'node:fs';
import { beforeEach, describe, expect, test } from 'vitest';

import { BrowserFS } from './browser-fs';
import type { Volume } from './volume';

const encoder = new TextEncoder();

function createTestVolume(): Volume {
    return {
        '/hello.txt': { content: encoder.encode('Hello, World!') },
        '/foo/bar.txt': { content: encoder.encode('bar content') },
        '/foo/baz.txt': { content: encoder.encode('baz content') },
        '/empty.txt': { content: encoder.encode('') },
    };
}

// Helper to promisify stat
function stat(fs: BrowserFS, path: string): Promise<Stats> {
    return new Promise((resolve, reject) => {
        fs.stat(path, (err, stats) => {
            if (err) reject(err);
            else resolve(stats);
        });
    });
}

// Helper to promisify lstat
function lstat(fs: BrowserFS, path: string): Promise<Stats> {
    return new Promise((resolve, reject) => {
        fs.lstat(path, (err, stats) => {
            if (err) reject(err);
            else resolve(stats);
        });
    });
}

// Helper to promisify fstat
function fstat(fs: BrowserFS, fd: number): Promise<Stats> {
    return new Promise((resolve, reject) => {
        fs.fstat(fd, (err, stats) => {
            if (err) reject(err);
            else resolve(stats);
        });
    });
}

// Helper to promisify open
function open(fs: BrowserFS, path: string, flags?: number): Promise<number> {
    return new Promise((resolve, reject) => {
        fs.open(path, flags, undefined, (err, fd) => {
            if (err) reject(err);
            else resolve(fd);
        });
    });
}

// Helper to promisify read
function read(
    fs: BrowserFS,
    fd: number,
    buffer: Uint8Array,
    offset: number,
    length: number,
    position: number | null,
): Promise<{ bytesRead: number; buffer: Uint8Array }> {
    return new Promise((resolve, reject) => {
        fs.read(fd, buffer, offset, length, position, (err, bytesRead, buf) => {
            if (err) reject(err);
            else resolve({ bytesRead, buffer: buf });
        });
    });
}

// Helper to promisify readdir
function readdir(fs: BrowserFS, path: string): Promise<string[]> {
    return new Promise((resolve, reject) => {
        fs.readdir(path, (err, files) => {
            if (err) reject(err);
            else resolve(files);
        });
    });
}

// Helper to promisify close
function close(fs: BrowserFS, fd: number): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.close(fd, (err) => {
            if (err) reject(err);
            else resolve();
        });
    });
}

const O_DIRECTORY = 8192;

describe('BrowserFS', () => {
    let fs: BrowserFS;

    beforeEach(() => {
        fs = new BrowserFS(createTestVolume());
    });

    describe('stat', () => {
        test('returns stats for existing file', async () => {
            const stats = await stat(fs, '/hello.txt');
            expect(stats.isFile()).toBe(true);
            expect(stats.isDirectory()).toBe(false);
            expect(stats.size).toBe(13); // "Hello, World!".length
        });

        test('returns stats for existing directory', async () => {
            const stats = await stat(fs, '/foo');
            expect(stats.isFile()).toBe(false);
            expect(stats.isDirectory()).toBe(true);
        });

        test('returns ENOENT for non-existing file', async () => {
            await expect(stat(fs, '/nonexistent.txt')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns stats for root directory', async () => {
            const stats = await stat(fs, '/');
            expect(stats.isDirectory()).toBe(true);
        });
    });

    describe('lstat', () => {
        test('returns stats for existing file', async () => {
            const stats = await lstat(fs, '/hello.txt');
            expect(stats.isFile()).toBe(true);
            expect(stats.size).toBe(13);
        });

        test('returns stats for existing directory', async () => {
            const stats = await lstat(fs, '/foo');
            expect(stats.isDirectory()).toBe(true);
        });

        test('returns ENOENT for non-existing path', async () => {
            await expect(lstat(fs, '/does/not/exist')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });
    });

    describe('open', () => {
        test('opens existing file and returns fd', async () => {
            const fd = await open(fs, '/hello.txt');
            expect(fd).toBeGreaterThanOrEqual(3);
        });

        test('returns ENOENT for non-existing file', async () => {
            await expect(open(fs, '/nonexistent.txt')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns EISDIR when trying to open directory without O_DIRECTORY', async () => {
            await expect(open(fs, '/foo')).rejects.toMatchObject({
                code: 'EISDIR',
            });
        });

        test('opens directory with O_DIRECTORY flag', async () => {
            const fd = await open(fs, '/foo', O_DIRECTORY);
            expect(fd).toBeGreaterThanOrEqual(3);
        });

        test('returns ENOTDIR when using O_DIRECTORY on a file', async () => {
            await expect(
                open(fs, '/hello.txt', O_DIRECTORY),
            ).rejects.toMatchObject({
                code: 'ENOTDIR',
            });
        });

        test('returns ENOENT when using O_DIRECTORY on non-existent path', async () => {
            await expect(
                open(fs, '/does-not-exist', O_DIRECTORY),
            ).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns EINVAL for empty path', async () => {
            await expect(open(fs, '')).rejects.toMatchObject({
                code: 'EINVAL',
            });
        });
    });

    describe('fstat', () => {
        test('returns stats for open file', async () => {
            const fd = await open(fs, '/hello.txt');
            const stats = await fstat(fs, fd);
            expect(stats.isFile()).toBe(true);
            expect(stats.size).toBe(13);
        });

        test('returns stats for open directory', async () => {
            const fd = await open(fs, '/foo', O_DIRECTORY);
            const stats = await fstat(fs, fd);
            expect(stats.isDirectory()).toBe(true);
        });

        test('returns EBADF for invalid fd', async () => {
            await expect(fstat(fs, 999)).rejects.toMatchObject({
                code: 'EBADF',
            });
        });
    });

    describe('read', () => {
        test('reads entire file content', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(20);
            const { bytesRead, buffer: buf } = await read(
                fs,
                fd,
                buffer,
                0,
                20,
                null,
            );
            expect(bytesRead).toBe(13);
            const content = new TextDecoder().decode(
                buf.subarray(0, bytesRead),
            );
            expect(content).toBe('Hello, World!');
        });

        test('reads file with offset in buffer', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(20);
            const { bytesRead, buffer: buf } = await read(
                fs,
                fd,
                buffer,
                5,
                10,
                null,
            );
            expect(bytesRead).toBe(10);
            // First 5 bytes should be 0
            expect(buf[0]).toBe(0);
            expect(buf[4]).toBe(0);
            // Content starts at offset 5
            const content = new TextDecoder().decode(
                buf.subarray(5, 5 + bytesRead),
            );
            expect(content).toBe('Hello, Wor');
        });

        test('reads file from specific position', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(10);
            const { bytesRead, buffer: buf } = await read(
                fs,
                fd,
                buffer,
                0,
                10,
                7,
            );
            expect(bytesRead).toBe(6); // "World!" from position 7
            const content = new TextDecoder().decode(
                buf.subarray(0, bytesRead),
            );
            expect(content).toBe('World!');
        });

        test('sequential reads advance position', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(5);

            const result1 = await read(fs, fd, buffer, 0, 5, null);
            expect(result1.bytesRead).toBe(5);
            expect(new TextDecoder().decode(result1.buffer)).toBe('Hello');

            // Second read should continue from position 5
            const result2 = await read(fs, fd, buffer, 0, 5, null);
            expect(result2.bytesRead).toBe(5);
            expect(new TextDecoder().decode(result2.buffer)).toBe(', Wor');
        });

        test('returns 0 bytes when reading empty file', async () => {
            const fd = await open(fs, '/empty.txt');
            const buffer = new Uint8Array(10);
            const { bytesRead } = await read(fs, fd, buffer, 0, 10, null);
            expect(bytesRead).toBe(0);
        });

        test('returns EBADF for invalid fd', async () => {
            const buffer = new Uint8Array(10);
            await expect(
                read(fs, 999, buffer, 0, 10, null),
            ).rejects.toMatchObject({
                code: 'EBADF',
            });
        });

        test('returns EINVAL for negative offset', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(10);
            await expect(
                read(fs, fd, buffer, -1, 10, null),
            ).rejects.toMatchObject({
                code: 'EINVAL',
            });
        });

        test('returns EINVAL for negative length', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(10);
            await expect(
                read(fs, fd, buffer, 0, -1, null),
            ).rejects.toMatchObject({
                code: 'EINVAL',
            });
        });

        test('returns EINVAL for negative position', async () => {
            const fd = await open(fs, '/hello.txt');
            const buffer = new Uint8Array(10);
            await expect(read(fs, fd, buffer, 0, 10, -1)).rejects.toMatchObject(
                {
                    code: 'EINVAL',
                },
            );
        });

        test('throws EISDIR when reading from directory', async () => {
            const fd = await open(fs, '/foo', O_DIRECTORY);
            const buffer = new Uint8Array(10);
            await expect(
                read(fs, fd, buffer, 0, 10, null),
            ).rejects.toMatchObject({
                code: 'EISDIR',
            });
        });
    });

    describe('readdir', () => {
        test('lists files in directory', async () => {
            const files = await readdir(fs, '/foo');
            expect(files).toContain('bar.txt');
            expect(files).toContain('baz.txt');
            expect(files.length).toBe(2);
        });

        test('lists files in root directory', async () => {
            const files = await readdir(fs, '/');
            expect(files).toContain('hello.txt');
            expect(files).toContain('foo');
            expect(files).toContain('empty.txt');
        });

        test('returns ENOENT for non-existing directory', async () => {
            await expect(readdir(fs, '/nonexistent')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns ENOTDIR when path is a file', async () => {
            await expect(readdir(fs, '/hello.txt')).rejects.toMatchObject({
                code: 'ENOTDIR',
            });
        });
    });

    describe('close', () => {
        test('closes file without error', async () => {
            const fd = await open(fs, '/hello.txt');
            await expect(close(fs, fd)).resolves.toBeUndefined();
        });
    });
});
