import type { PathLike, Stats } from 'node:fs';

import { ErrnoException } from '../fs/errno-exception';
import type { FSAPI } from '../fs/fs-api';

/**
 * The part of a Worker, a worker's global scope or a MessagePort that the bridge uses.
 * All three have it.
 */
export interface MessageEndpoint {
    postMessage(message: unknown, transfer?: Transferable[]): void;
    addEventListener(
        type: 'message',
        listener: (event: MessageEvent) => void,
    ): void;
    removeEventListener(
        type: 'message',
        listener: (event: MessageEvent) => void,
    ): void;
}

/** The FSAPI calls that remoteFS forwards, and the arguments each one sends. */
type FSRequestArgs = {
    fstat: [fd: number];
    lstat: [path: string];
    stat: [path: string];
    open: [path: string, flags: unknown, mode: unknown];
    close: [fd: number];
    read: [fd: number, length: number, position: number | bigint | null];
    write: [fd: number, bytes: Uint8Array, position: number | null];
    readdir: [path: string];
    writeFile: [path: string, data: Uint8Array];
    mkdir: [path: string];
    unlink: [path: string];
    rmdir: [path: string];
    rename: [oldPath: string, newPath: string];
    symlink: [target: string, path: string];
};

type FSOp = keyof FSRequestArgs;

/** A call that remoteFS sends to serveFS. */
export type FSRequest = {
    [K in FSOp]: { kind: 'fs'; id: number; op: K; args: FSRequestArgs[K] };
}[FSOp];

/** An error as it crosses the bridge. Structured cloning drops an Error's `code`. */
type FSError = { message: string; code?: string };

/** serveFS's answer to an FSRequest with the same id. */
export type FSReply = {
    kind: 'fs-reply';
    id: number;
    error: FSError | null;
    value?: unknown;
};

/**
 * The Stats fields Go's syscall package reads, as plain data. Cloning a class instance
 * drops its methods, so remoteFS rebuilds the one method Go calls. See remoteStats.
 */
type StatsData = Pick<
    Stats,
    | 'dev'
    | 'ino'
    | 'mode'
    | 'nlink'
    | 'uid'
    | 'gid'
    | 'rdev'
    | 'size'
    | 'blksize'
    | 'blocks'
    | 'atimeMs'
    | 'mtimeMs'
    | 'ctimeMs'
>;

function statsData(stats: Stats): StatsData {
    return {
        dev: stats.dev,
        ino: stats.ino,
        mode: stats.mode,
        nlink: stats.nlink,
        uid: stats.uid,
        gid: stats.gid,
        rdev: stats.rdev,
        size: stats.size,
        blksize: stats.blksize,
        blocks: stats.blocks,
        atimeMs: stats.atimeMs,
        mtimeMs: stats.mtimeMs,
        ctimeMs: stats.ctimeMs,
    };
}

/** The bits of a mode that give the file's type, and the type of a directory. */
const S_IFMT = 0o170000;
const S_IFDIR = 0o040000;

/**
 * Returns data as the Stats remoteFS passes on. Go's syscall package reads the fields
 * and calls `isDirectory()` when it opens a file.
 */
function remoteStats(data: StatsData): Stats {
    return {
        ...data,
        isDirectory: () => (data.mode & S_IFMT) === S_IFDIR,
    } as Stats;
}

function errorData(err: NodeJS.ErrnoException): FSError {
    return { message: err.message, code: err.code };
}

/**
 * Answers the FSRequests that arrive at endpoint by calling fs, and returns a function
 * that stops answering. It is the end of the bridge that holds the filesystem.
 */
export function serveFS(endpoint: MessageEndpoint, fs: FSAPI): () => void {
    const listener = (event: MessageEvent) => {
        const request = event.data as FSRequest | undefined;
        if (request?.kind !== 'fs') {
            return;
        }
        const reply = (
            err: NodeJS.ErrnoException | null,
            value?: unknown,
            transfer: Transferable[] = [],
        ) => {
            const message: FSReply = {
                kind: 'fs-reply',
                id: request.id,
                error: err ? errorData(err) : null,
                value,
            };
            endpoint.postMessage(message, err ? [] : transfer);
        };
        const done = (err: NodeJS.ErrnoException | null) => reply(err);
        const stats = (err: NodeJS.ErrnoException | null, st: Stats) =>
            reply(err, err ? undefined : statsData(st));

        // A call that throws instead of calling back still answers, or the server would
        // wait for the answer forever. Go panics on an error code it has no errno for,
        // so a thrown error without a code is answered as EIO.
        try {
            dispatch(request, fs, reply, done, stats);
        } catch (err) {
            const thrown = err as NodeJS.ErrnoException;
            reply(
                new ErrnoException(String(thrown?.message ?? err), {
                    code: thrown?.code ?? 'EIO',
                }),
            );
        }
    };
    endpoint.addEventListener('message', listener);
    return () => endpoint.removeEventListener('message', listener);
}

/** Calls the fs method that request names, with its arguments and the given callbacks. */
function dispatch(
    request: FSRequest,
    fs: FSAPI,
    reply: (
        err: NodeJS.ErrnoException | null,
        value?: unknown,
        transfer?: Transferable[],
    ) => void,
    done: (err: NodeJS.ErrnoException | null) => void,
    stats: (err: NodeJS.ErrnoException | null, st: Stats) => void,
) {
    switch (request.op) {
        case 'fstat':
            return fs.fstat(...request.args, stats);
        case 'lstat':
            return fs.lstat(...request.args, stats);
        case 'stat':
            return fs.stat(...request.args, stats);
        case 'open': {
            const [path, flags, mode] = request.args;
            return fs.open(path, flags as never, mode as never, reply);
        }
        case 'close':
            return fs.close(...request.args, done);
        case 'read': {
            const [fd, length, position] = request.args;
            const buffer = new Uint8Array(length);
            return fs.read(fd, buffer, 0, length, position, (err, n) =>
                reply(err, err ? undefined : buffer.subarray(0, n), [
                    buffer.buffer,
                ]),
            );
        }
        case 'write': {
            const [fd, bytes, position] = request.args;
            return fs.write(fd, bytes, 0, bytes.length, position, (err, n) =>
                reply(err, n),
            );
        }
        case 'readdir':
            return fs.readdir(...request.args, reply);
        case 'writeFile':
            return fs.writeFile(...request.args, done);
        case 'mkdir':
            return fs.mkdir(...request.args, done);
        case 'unlink':
            return fs.unlink(...request.args, done);
        case 'rmdir':
            return fs.rmdir(...request.args, done);
        case 'rename':
            return fs.rename(...request.args, done);
        case 'symlink':
            return fs.symlink(...request.args, done);
    }
}

/**
 * Returns an FSAPI whose calls go to the serveFS at the other end of endpoint. Each call
 * calls back once the answer arrives. Errors keep their `code`, which Go maps to an
 * errno.
 */
export function remoteFS(endpoint: MessageEndpoint): FSAPI {
    let nextId = 0;
    const pending = new Map<
        number,
        (err: NodeJS.ErrnoException | null, value: any) => void
    >();
    endpoint.addEventListener('message', (event: MessageEvent) => {
        const reply = event.data as FSReply | undefined;
        if (reply?.kind !== 'fs-reply') {
            return;
        }
        const callback = pending.get(reply.id);
        pending.delete(reply.id);
        callback?.(
            reply.error
                ? new ErrnoException(reply.error.message, {
                      code: reply.error.code,
                  })
                : null,
            reply.value,
        );
    });

    function call<K extends FSOp>(
        op: K,
        args: FSRequestArgs[K],
        callback: (err: NodeJS.ErrnoException | null, value: any) => void,
        transfer: Transferable[] = [],
    ) {
        const id = nextId++;
        pending.set(id, callback);
        endpoint.postMessage(
            { kind: 'fs', id, op, args } as FSRequest,
            transfer,
        );
    }

    const path = (p: PathLike) => String(p);
    const statsCallback =
        (callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void) =>
        (err: NodeJS.ErrnoException | null, data: StatsData) =>
            callback(err, err ? (undefined as never) : remoteStats(data));

    return {
        fstat: (fd, callback) => call('fstat', [fd], statsCallback(callback)),
        lstat: (p, callback) =>
            call('lstat', [path(p)], statsCallback(callback)),
        stat: (p, callback) => call('stat', [path(p)], statsCallback(callback)),
        open: (p, flags, mode, callback) =>
            call('open', [path(p), flags, mode], callback),
        close: (fd, callback) => call('close', [fd], callback),
        read: (fd, buffer, offset, length, position, callback) =>
            call(
                'read',
                [fd, length, position],
                (err, bytes: Uint8Array | undefined) => {
                    if (err || !bytes) {
                        callback(err, 0, buffer);
                        return;
                    }
                    buffer.set(bytes, offset);
                    callback(null, bytes.length, buffer);
                },
            ),
        write: (fd, buffer, offset, length, position, callback) => {
            // buffer can be a view into the server's whole memory, which cloning would
            // copy in full, so the call sends a copy of just the bytes to write.
            const bytes = buffer.slice(offset, offset + length);
            call(
                'write',
                [fd, bytes, position],
                (err, written: number | undefined) =>
                    callback(err, written ?? 0, buffer),
                [bytes.buffer],
            );
        },
        readdir: (p, callback) => call('readdir', [path(p)], callback),
        writeFile: (p, data, callback) =>
            call('writeFile', [path(p), data.slice()], callback),
        mkdir: (p, callback) => call('mkdir', [path(p)], callback),
        unlink: (p, callback) => call('unlink', [path(p)], callback),
        rmdir: (p, callback) => call('rmdir', [path(p)], callback),
        rename: (from, to, callback) =>
            call('rename', [path(from), path(to)], callback),
        symlink: (target, p, callback) =>
            call('symlink', [path(target), path(p)], callback),
    };
}
