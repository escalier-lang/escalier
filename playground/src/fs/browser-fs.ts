import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';

import { ErrnoException } from './errno-exception';
import type { FSAPI } from './fs-api';
import type { FSDir, FSNode } from './fs-node';
import { SimpleStats } from './simple-stats';
import { type Volume, volumeToDir } from './volume';

const constants = {
    O_DIRECTORY: 1048576,
};

function assertNever(x: never): never {
    throw new Error(`Unexpected value: ${x}`);
}

export class BrowserFS implements FSAPI {
    fileID: number;
    openFiles: Map<number, FSNode> = new Map();
    readPositions: Map<number, number> = new Map();
    rootDir: FSDir;

    constructor(volume: Volume) {
        this.fileID = 3;
        this.rootDir = volumeToDir(volume);
    }

    /**
     * Locate any node (file or directory) within the in‑memory directory tree.
     *
     * @param pathStr Path string (e.g. "/foo/bar").
     * @returns The FSNode if found, otherwise undefined.
     */
    private findNodeInRootDir(pathStr: string): FSNode | undefined {
        const parts = pathStr.split('/').filter((p) => p.length > 0);
        let node: FSNode | undefined = this.rootDir;
        for (const part of parts) {
            if (node && node.type === 'dir') {
                node = node.children.get(part);
            } else {
                return undefined;
            }
        }
        return node;
    }

    fstat(
        fd: number,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ) {
        const fileData = this.openFiles.get(fd);
        if (!fileData) {
            callback(
                new ErrnoException('Bad file descriptor', {
                    code: 'EBADF',
                    errno: -9,
                    syscall: 'fstat',
                }),
                null as any,
            );
            return;
        }

        switch (fileData.type) {
            case 'dir': {
                const stats = new SimpleStats(
                    0,
                    false,
                    true,
                ) as unknown as Stats;
                callback(null, stats);
                break;
            }
            case 'file': {
                const stats = new SimpleStats(
                    fileData.content.length,
                    true,
                    false,
                ) as unknown as Stats;
                callback(null, stats);
                break;
            }
            default:
                assertNever(fileData);
        }
    }

    lstat(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ) {
        const pathStr = String(path);
        const node = this.findNodeInRootDir(pathStr);
        if (!node) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'lstat',
                    path: pathStr,
                }),
                null as any,
            );
            return;
        }

        let stats: Stats;
        if (node.type === 'file') {
            stats = new SimpleStats(
                node.content.length,
                true,
                false,
            ) as unknown as Stats;
        } else {
            // Directory
            stats = new SimpleStats(0, false, true) as unknown as Stats;
        }
        callback(null, stats);
    }

    stat(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ) {
        const pathStr = String(path);
        const node = this.findNodeInRootDir(pathStr);
        if (!node) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'stat',
                    path: pathStr,
                }),
                null as any,
            );
            return;
        }

        switch (node.type) {
            case 'file': {
                const stats = new SimpleStats(
                    node.content.length,
                    true,
                    false,
                ) as unknown as Stats;
                callback(null, stats);
                break;
            }
            case 'dir': {
                const stats = new SimpleStats(
                    0,
                    false,
                    true,
                ) as unknown as Stats;
                callback(null, stats);
                break;
            }
            default:
                assertNever(node);
        }
    }

    open(
        path: PathLike,
        _flags: OpenMode | undefined,
        _mode: Mode | undefined,
        callback: (err: NodeJS.ErrnoException | null, fd: number) => void,
    ) {
        // Basic validation: path must be a non‑empty string
        const pathStr = String(path);
        if (pathStr.length === 0) {
            callback(
                new ErrnoException('Invalid argument', {
                    code: 'EINVAL',
                    errno: -22,
                    syscall: 'open',
                }),
                -1,
            );
            return;
        }

        if (
            typeof _flags === 'number' &&
            (_flags & constants.O_DIRECTORY) !== 0
        ) {
            const node = this.findNodeInRootDir(pathStr);
            if (node && node.type === 'dir') {
                // Allocate a new file descriptor
                const fd = this.fileID++;
                this.openFiles.set(fd, node);
                callback(null, fd);
            } else {
                callback(
                    new ErrnoException('Not a directory', {
                        code: 'ENOTDIR',
                        errno: -20,
                        syscall: 'open',
                        path: pathStr,
                    }),
                    -1,
                );
            }
            return;
        }

        // Resolve the file in the in‑memory directory tree
        const node = this.findNodeInRootDir(pathStr);
        if (!node) {
            // File does not exist
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'open',
                    path: pathStr,
                }),
                -1,
            );
            return;
        }

        switch (node.type) {
            case 'dir':
                callback(
                    new ErrnoException('Is a directory', {
                        code: 'EISDIR',
                        errno: -21,
                        syscall: 'open',
                        path: pathStr,
                    }),
                    -1,
                );
                break;
            case 'file': {
                // Allocate a new file descriptor
                const fd = this.fileID++;
                this.openFiles.set(fd, node);
                callback(null, fd);
                break;
            }
            default:
                assertNever(node);
        }
    }

    close(_fd: number, callback: (error: Error | null) => void) {
        this.openFiles.delete(_fd);
        this.readPositions.delete(_fd);
        callback(null);
    }

    read(
        fd: number,
        buffer: Uint8Array,
        offset: number,
        length: number,
        position: number | bigint | null,
        callback: (
            err: NodeJS.ErrnoException | null,
            bytesRead: number,
            buffer: Uint8Array,
        ) => void,
    ) {
        if (!this.openFiles.has(fd)) {
            callback(
                new ErrnoException('File not open', {
                    code: 'EBADF',
                    errno: -9,
                    syscall: 'read',
                }),
                0,
                buffer,
            );
            return;
        }

        // Validate arguments
        if (offset < 0 || length < 0 || offset + length > buffer.length) {
            callback(
                new ErrnoException('Invalid argument', {
                    code: 'EINVAL',
                    errno: -22,
                    syscall: 'read',
                }),
                0,
                buffer,
            );
            return;
        }
        if (position !== null && Number(position) < 0) {
            callback(
                new ErrnoException('Invalid argument', {
                    code: 'EINVAL',
                    errno: -22,
                    syscall: 'read',
                }),
                0,
                buffer,
            );
            return;
        }

        const readPosition =
            position == null
                ? this.readPositions.get(fd) || 0
                : Number(position);

        const file = this.openFiles.get(fd);
        if (!file) {
            callback(
                new ErrnoException('File not open', {
                    code: 'EBADF',
                    errno: -9,
                    syscall: 'read',
                }),
                0,
                buffer,
            );
            return;
        }

        switch (file.type) {
            case 'dir':
                callback(
                    new ErrnoException('Is a directory', {
                        code: 'EISDIR',
                        errno: -21,
                        syscall: 'read',
                    }),
                    0,
                    buffer,
                );
                break;
            case 'file': {
                const bytesToRead = Math.min(
                    length,
                    file.content.length - readPosition,
                );

                if (bytesToRead <= 0) {
                    callback(null, 0, buffer);
                    return;
                }

                buffer.set(
                    file.content.subarray(
                        readPosition,
                        readPosition + bytesToRead,
                    ),
                    offset,
                );

                if (position == null) {
                    this.readPositions.set(fd, readPosition + bytesToRead);
                }

                callback(null, bytesToRead, buffer);
                break;
            }
            default:
                assertNever(file);
        }
    }

    readdir(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, files: string[]) => void,
    ) {
        const pathStr = String(path);
        const node = this.findNodeInRootDir(pathStr);
        if (!node) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'readdir',
                    path: pathStr,
                }),
                [],
            );
            return;
        }

        if (node.type !== 'dir') {
            callback(
                new ErrnoException('Not a directory', {
                    code: 'ENOTDIR',
                    errno: -20,
                    syscall: 'readdir',
                    path: pathStr,
                }),
                [],
            );
            return;
        }

        const files = Array.from(node.children.keys());
        callback(null, files);
    }
}
