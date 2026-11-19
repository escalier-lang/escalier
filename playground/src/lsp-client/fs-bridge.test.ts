import type { Stats } from 'node:fs';
import { afterEach, beforeEach, expect, test } from 'vitest';

import { BrowserFS } from '../fs/browser-fs';
import type { FSAPI } from '../fs/fs-api';

import { type MessageEndpoint, remoteFS, serveFS } from './fs-bridge';

const encoder = new TextEncoder();
const decoder = new TextDecoder();

let channel: MessageChannel;
let local: BrowserFS;
let remote: FSAPI;
let stopServing: () => void;

/** Starts port so its listeners hear messages, and returns it. */
function started(port: MessagePort): MessageEndpoint {
    port.start();
    return port;
}

beforeEach(() => {
    local = new BrowserFS({
        '/lib/index.esc': { content: encoder.encode('val x = 1\n') },
    });
    channel = new MessageChannel();
    stopServing = serveFS(started(channel.port1), local);
    remote = remoteFS(started(channel.port2));
});

afterEach(() => {
    stopServing();
    channel.port1.close();
    channel.port2.close();
});

/** Calls fn with a Node-style callback and resolves with its value or rejects with its error. */
function call<T>(
    fn: (
        callback: (err: NodeJS.ErrnoException | null, value: T) => void,
    ) => void,
): Promise<T> {
    return new Promise((resolve, reject) =>
        fn((err, value) => (err ? reject(err) : resolve(value))),
    );
}

test('stat returns the fields and the method Go reads', async () => {
    const stats = await call<Stats>((cb) => remote.stat('/lib/index.esc', cb));
    const dir = await call<Stats>((cb) => remote.stat('/lib', cb));

    expect(stats.size).toBe(10);
    expect(stats.mode).toBe(0o100644);
    expect(stats.isDirectory()).toBe(false);
    expect(dir.isDirectory()).toBe(true);
    expect(Object.keys(stats).sort()).toEqual([
        'atimeMs',
        'blksize',
        'blocks',
        'ctimeMs',
        'dev',
        'gid',
        'ino',
        'isDirectory',
        'mode',
        'mtimeMs',
        'nlink',
        'rdev',
        'size',
        'uid',
    ]);
});

test('an error keeps its code', async () => {
    const err = await call((cb) => remote.stat('/missing.esc', cb)).catch(
        (e: NodeJS.ErrnoException) => e,
    );

    expect(err).toMatchObject({ code: 'ENOENT' });
});

test('a call that throws answers with an error', async () => {
    local.stat = () => {
        throw new Error('Unexpected value');
    };

    const err = await call((cb) => remote.stat('/lib', cb)).catch(
        (e: NodeJS.ErrnoException) => e,
    );

    expect(err).toMatchObject({ message: 'Unexpected value', code: 'EIO' });
});

test('read fills the caller buffer at its offset', async () => {
    const fd = await call<number>((cb) =>
        remote.open('/lib/index.esc', 0, undefined, cb),
    );
    const buffer = new Uint8Array(16).fill(0xff);

    const n = await new Promise<number>((resolve, reject) =>
        remote.read(fd, buffer, 2, 8, null, (err, bytesRead) =>
            err ? reject(err) : resolve(bytesRead),
        ),
    );

    expect(n).toBe(8);
    expect(decoder.decode(buffer.subarray(2, 10))).toBe('val x = ');
    expect(buffer[0]).toBe(0xff);
    expect(buffer[10]).toBe(0xff);
    await call<void>((cb) => remote.close(fd, (err) => cb(err, undefined)));
});

test('write sends only the bytes it is asked to write', async () => {
    // O_WRONLY | O_CREAT | O_TRUNC, as Go's syscall package numbers them.
    const flags = 1 | 64 | 512;
    await call<void>((cb) => remote.mkdir('/build', cb));
    const fd = await call<number>((cb) =>
        remote.open('/build/index.js', flags, 0o644, cb),
    );
    const memory = encoder.encode('xxconst x = 1;yy');

    const written = await new Promise<number>((resolve, reject) =>
        remote.write(fd, memory, 2, 12, null, (err, n) =>
            err ? reject(err) : resolve(n),
        ),
    );
    await call<void>((cb) => remote.close(fd, (err) => cb(err, undefined)));

    expect(written).toBe(12);
    // The caller's buffer is copied, not transferred, so it stays usable.
    expect(memory.byteLength).toBe(16);
    const entries = await call<string[]>((cb) => remote.readdir('/build', cb));
    expect(entries).toEqual(['index.js']);
    const text = await new Promise<string>((resolve, reject) =>
        local.open('/build/index.js', 0, undefined, (err, readFd) => {
            if (err) {
                reject(err);
                return;
            }
            const out = new Uint8Array(64);
            local.read(readFd, out, 0, 64, null, (readErr, n) =>
                readErr
                    ? reject(readErr)
                    : resolve(decoder.decode(out.subarray(0, n))),
            );
        }),
    );
    expect(text).toBe('const x = 1;');
});

test('a write on the remote end fires the local filesystem events', async () => {
    const events: string[] = [];
    local.events.on((event) => events.push(`${event.type} ${event.path}`));

    await call<void>((cb) => remote.mkdir('/build', cb));
    await call<void>((cb) => remote.rename('/build', '/out', cb));
    await call<void>((cb) => remote.rmdir('/out', cb));

    expect(events).toEqual(['create /build', 'rename /out', 'delete /out']);
});

test('answers each call, even when replies arrive out of order', async () => {
    const results = await Promise.all([
        call<Stats>((cb) => remote.stat('/lib/index.esc', cb)),
        call<string[]>((cb) => remote.readdir('/lib', cb)),
        call<Stats>((cb) => remote.stat('/lib', cb)),
    ]);

    expect(results[0].size).toBe(10);
    expect(results[1]).toEqual(['index.esc']);
    expect(results[2].mode).toBe(0o40755);
});

test('fstat, lstat, writeFile, symlink and unlink cross the bridge', async () => {
    const fd = await call<number>((cb) =>
        remote.open('/lib/index.esc', 0, undefined, cb),
    );
    const byFd = await call<Stats>((cb) => remote.fstat(fd, cb));
    await call<void>((cb) => remote.close(fd, (err) => cb(err, undefined)));
    await call<void>((cb) =>
        remote.writeFile('/notes.txt', encoder.encode('hi'), cb),
    );
    await call<void>((cb) => remote.symlink('/notes.txt', '/link.txt', cb));
    const link = await call<Stats>((cb) => remote.lstat('/link.txt', cb));
    await call<void>((cb) => remote.unlink('/link.txt', cb));

    expect(byFd.size).toBe(10);
    expect(link.mode & 0o170000).toBe(0o120000);
    expect(await call<string[]>((cb) => remote.readdir('/', cb))).toEqual([
        'lib',
        'notes.txt',
    ]);
});
