import type { Stats } from 'node:fs';
import { beforeEach, describe, expect, test } from 'vitest';

import { BrowserFS } from './browser-fs';
import type { FSEvent } from './fs-events';
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

// Helper to promisify writeFile
function writeFile(
    fs: BrowserFS,
    path: string,
    data: Uint8Array,
): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.writeFile(path, data, (err) => {
            if (err) reject(err);
            else resolve();
        });
    });
}

// Helper to promisify mkdir
function mkdir(fs: BrowserFS, path: string): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.mkdir(path, (err) => {
            if (err) reject(err);
            else resolve();
        });
    });
}

// Helper to promisify unlink
function unlink(fs: BrowserFS, path: string): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.unlink(path, (err) => {
            if (err) reject(err);
            else resolve();
        });
    });
}

// Helper to promisify rmdir
function rmdir(fs: BrowserFS, path: string): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.rmdir(path, (err) => {
            if (err) reject(err);
            else resolve();
        });
    });
}

// Helper to promisify rename
function rename(
    fs: BrowserFS,
    oldPath: string,
    newPath: string,
): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.rename(oldPath, newPath, (err) => {
            if (err) reject(err);
            else resolve();
        });
    });
}

// Helper to promisify symlink
function symlink(fs: BrowserFS, target: string, path: string): Promise<void> {
    return new Promise((resolve, reject) => {
        fs.symlink(target, path, (err) => {
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

    describe('writeFile', () => {
        test('creates a new file', async () => {
            await writeFile(fs, '/new.txt', encoder.encode('new content'));
            const stats = await stat(fs, '/new.txt');
            expect(stats.isFile()).toBe(true);
            expect(stats.size).toBe(11);
        });

        test('overwrites an existing file', async () => {
            await writeFile(fs, '/hello.txt', encoder.encode('replaced'));
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
            const content = new TextDecoder().decode(
                buf.subarray(0, bytesRead),
            );
            expect(content).toBe('replaced');
        });

        test('creates a file in a subdirectory', async () => {
            await writeFile(fs, '/foo/new.txt', encoder.encode('in subdir'));
            const files = await readdir(fs, '/foo');
            expect(files).toContain('new.txt');
        });

        test('returns ENOENT when parent directory does not exist', async () => {
            await expect(
                writeFile(fs, '/no/such/dir/file.txt', encoder.encode('x')),
            ).rejects.toMatchObject({ code: 'ENOENT' });
        });

        test('returns EISDIR when path is a directory', async () => {
            await expect(
                writeFile(fs, '/foo', encoder.encode('x')),
            ).rejects.toMatchObject({ code: 'EISDIR' });
        });

        test('emits create event for new file', async () => {
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await writeFile(fs, '/new.txt', encoder.encode('data'));
            expect(events).toEqual([
                { type: 'create', path: '/new.txt', kind: 'file' },
            ]);
        });

        test('emits change event when overwriting existing file', async () => {
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await writeFile(fs, '/hello.txt', encoder.encode('updated'));
            expect(events).toEqual([
                { type: 'change', path: '/hello.txt', kind: 'file' },
            ]);
        });

        test('preserves node identity when overwriting (open fds stay valid)', async () => {
            const fd = await open(fs, '/hello.txt');
            await writeFile(fs, '/hello.txt', encoder.encode('new data'));
            // Reading from the already-open fd should see the new content
            const buffer = new Uint8Array(20);
            const { bytesRead, buffer: buf } = await read(
                fs,
                fd,
                buffer,
                0,
                20,
                0,
            );
            const content = new TextDecoder().decode(
                buf.subarray(0, bytesRead),
            );
            expect(content).toBe('new data');
        });
    });

    describe('mkdir', () => {
        test('creates a new directory', async () => {
            await mkdir(fs, '/newdir');
            const stats = await stat(fs, '/newdir');
            expect(stats.isDirectory()).toBe(true);
        });

        test('returns EEXIST when directory already exists', async () => {
            await expect(mkdir(fs, '/foo')).rejects.toMatchObject({
                code: 'EEXIST',
            });
        });

        test('returns ENOENT when parent does not exist', async () => {
            await expect(mkdir(fs, '/no/such/parent')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('emits create event', async () => {
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await mkdir(fs, '/newdir');
            expect(events).toEqual([
                { type: 'create', path: '/newdir', kind: 'dir' },
            ]);
        });
    });

    describe('unlink', () => {
        test('removes an existing file', async () => {
            await unlink(fs, '/hello.txt');
            await expect(stat(fs, '/hello.txt')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns ENOENT for non-existing file', async () => {
            await expect(unlink(fs, '/nope.txt')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns EISDIR when path is a directory', async () => {
            await expect(unlink(fs, '/foo')).rejects.toMatchObject({
                code: 'EISDIR',
            });
        });

        test('emits delete event', async () => {
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await unlink(fs, '/hello.txt');
            expect(events).toEqual([
                { type: 'delete', path: '/hello.txt', kind: 'file' },
            ]);
        });
    });

    describe('rmdir', () => {
        test('removes an empty directory', async () => {
            await mkdir(fs, '/emptydir');
            await rmdir(fs, '/emptydir');
            await expect(stat(fs, '/emptydir')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('returns ENOTEMPTY for non-empty directory', async () => {
            await expect(rmdir(fs, '/foo')).rejects.toMatchObject({
                code: 'ENOTEMPTY',
            });
        });

        test('returns ENOTDIR when path is a file', async () => {
            await expect(rmdir(fs, '/hello.txt')).rejects.toMatchObject({
                code: 'ENOTDIR',
            });
        });

        test('returns ENOENT for non-existing directory', async () => {
            await expect(rmdir(fs, '/nope')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('emits delete event', async () => {
            await mkdir(fs, '/emptydir');
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await rmdir(fs, '/emptydir');
            expect(events).toEqual([
                { type: 'delete', path: '/emptydir', kind: 'dir' },
            ]);
        });
    });

    describe('rename', () => {
        test('renames a file', async () => {
            await rename(fs, '/hello.txt', '/hi.txt');
            await expect(stat(fs, '/hello.txt')).rejects.toMatchObject({
                code: 'ENOENT',
            });
            const stats = await stat(fs, '/hi.txt');
            expect(stats.isFile()).toBe(true);
        });

        test('renames a directory', async () => {
            await rename(fs, '/foo', '/bar');
            await expect(stat(fs, '/foo')).rejects.toMatchObject({
                code: 'ENOENT',
            });
            const stats = await stat(fs, '/bar');
            expect(stats.isDirectory()).toBe(true);
            // Children should still be accessible
            const files = await readdir(fs, '/bar');
            expect(files).toContain('bar.txt');
        });

        test('returns ENOENT when source does not exist', async () => {
            await expect(
                rename(fs, '/nope.txt', '/dest.txt'),
            ).rejects.toMatchObject({ code: 'ENOENT' });
        });

        test('emits rename event', async () => {
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await rename(fs, '/hello.txt', '/hi.txt');
            expect(events).toEqual([
                {
                    type: 'rename',
                    path: '/hi.txt',
                    kind: 'file',
                    oldPath: '/hello.txt',
                },
            ]);
        });
    });

    describe('symlink', () => {
        test('creates a symlink to a file', async () => {
            await symlink(fs, '/hello.txt', '/link.txt');
            // stat follows the symlink
            const stats = await stat(fs, '/link.txt');
            expect(stats.isFile()).toBe(true);
            expect(stats.size).toBe(13);
        });

        test('lstat returns symlink stats', async () => {
            await symlink(fs, '/hello.txt', '/link.txt');
            const stats = await lstat(fs, '/link.txt');
            expect(stats.isSymbolicLink()).toBe(true);
            expect(stats.isFile()).toBe(false);
        });

        test('creates a symlink to a directory', async () => {
            await symlink(fs, '/foo', '/foolink');
            const stats = await stat(fs, '/foolink');
            expect(stats.isDirectory()).toBe(true);
            const files = await readdir(fs, '/foolink');
            expect(files).toContain('bar.txt');
        });

        test('follows relative symlinks', async () => {
            // Create /foo/link -> bar.txt (relative to /foo/)
            await symlink(fs, 'bar.txt', '/foo/link');
            const stats = await stat(fs, '/foo/link');
            expect(stats.isFile()).toBe(true);
        });

        test('follows chained symlinks', async () => {
            await symlink(fs, '/hello.txt', '/link1');
            await symlink(fs, '/link1', '/link2');
            const stats = await stat(fs, '/link2');
            expect(stats.isFile()).toBe(true);
            expect(stats.size).toBe(13);
        });

        test('returns EEXIST when symlink path already exists', async () => {
            await expect(
                symlink(fs, '/hello.txt', '/foo'),
            ).rejects.toMatchObject({ code: 'EEXIST' });
        });

        test('readdir returns symlink names as regular entries', async () => {
            await symlink(fs, '/hello.txt', '/foo/link');
            const files = await readdir(fs, '/foo');
            expect(files).toContain('link');
        });

        test('traverses symlink directories in path', async () => {
            // /foo contains bar.txt. Create /foolink -> /foo
            await symlink(fs, '/foo', '/foolink');
            // Access /foolink/bar.txt — should resolve through symlink
            const stats = await stat(fs, '/foolink/bar.txt');
            expect(stats.isFile()).toBe(true);
        });

        test('handles relative symlink with ..', async () => {
            // /foo/link -> ../hello.txt resolves to /hello.txt
            await symlink(fs, '../hello.txt', '/foo/link');
            const stats = await stat(fs, '/foo/link');
            expect(stats.isFile()).toBe(true);
            expect(stats.size).toBe(13);
        });

        test('returns ENOENT for dangling symlink via stat', async () => {
            await symlink(fs, '/nonexistent', '/dangling');
            await expect(stat(fs, '/dangling')).rejects.toMatchObject({
                code: 'ENOENT',
            });
        });

        test('lstat succeeds for dangling symlink', async () => {
            await symlink(fs, '/nonexistent', '/dangling');
            const stats = await lstat(fs, '/dangling');
            expect(stats.isSymbolicLink()).toBe(true);
        });

        test('emits create event', async () => {
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await symlink(fs, '/hello.txt', '/link.txt');
            expect(events).toEqual([
                { type: 'create', path: '/link.txt', kind: 'file' },
            ]);
        });
    });

    describe('writeFile through symlink', () => {
        test('writes through symlink to the target file', async () => {
            await symlink(fs, '/hello.txt', '/link.txt');
            await writeFile(fs, '/link.txt', encoder.encode('via link'));
            // The original file should have the new content
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
            expect(new TextDecoder().decode(buf.subarray(0, bytesRead))).toBe(
                'via link',
            );
        });

        test('emits change event when writing through symlink', async () => {
            await symlink(fs, '/hello.txt', '/link.txt');
            const events: FSEvent[] = [];
            fs.events.on((e) => events.push(e));
            await writeFile(fs, '/link.txt', encoder.encode('updated'));
            expect(events).toEqual([
                { type: 'change', path: '/link.txt', kind: 'file' },
            ]);
        });

        test('returns ENOENT when writing through dangling symlink', async () => {
            await symlink(fs, '/nonexistent', '/dangling');
            await expect(
                writeFile(fs, '/dangling', encoder.encode('data')),
            ).rejects.toMatchObject({ code: 'ENOENT' });
        });
    });

    describe('clear', () => {
        test('removes all entries except node_modules', async () => {
            // Add a node_modules entry
            await mkdir(fs, '/node_modules');
            await mkdir(fs, '/node_modules/pkg');
            await writeFile(
                fs,
                '/node_modules/pkg/index.js',
                encoder.encode('module'),
            );

            fs.clear();

            // Root should only have node_modules
            const files = await readdir(fs, '/');
            expect(files).toEqual(['node_modules']);

            // node_modules content should be preserved
            const pkgFiles = await readdir(fs, '/node_modules/pkg');
            expect(pkgFiles).toContain('index.js');
        });

        test('removes files and directories', async () => {
            fs.clear();
            const files = await readdir(fs, '/');
            expect(files).toEqual([]);
        });
    });
});
