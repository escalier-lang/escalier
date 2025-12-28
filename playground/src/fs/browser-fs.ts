import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';

import { ErrnoException } from './errno-exception';
import type { FSAPI } from './fs-api';
import type { FSDir, FSNode } from './fs-node';
import { SimpleStats } from './simple-stats';
import { type Volume, volumeToDir } from './volume';

export class BrowserFS implements FSAPI {
    fileID: number;
    openFiles: Map<number, Uint8Array> = new Map();
    readPositions: Map<number, number> = new Map();
    rootDir: FSDir;

    constructor(volume: Volume) {
        this.fileID = 3;
        this.rootDir = volumeToDir(volume);
    }

    /**
     * Locate a file within the in‑memory directory tree.
     *
     * @param pathStr Absolute or relative path string (e.g. "/foo/bar.txt").
     * @returns The file's Uint8Array content if found, otherwise `undefined`.
     */
    private findFileInRootDir(pathStr: string): Uint8Array | undefined {
        const node = this.findNodeInRootDir(pathStr);
        return node && node.type === 'file' ? node.content : undefined;
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

        const stats = new SimpleStats(
            fileData.length,
            true,
            false,
        ) as unknown as Stats;
        callback(null, stats);
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

        // Resolve the file in the in‑memory directory tree
        const fileData = this.findFileInRootDir(pathStr);
        if (!fileData) {
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

        // Allocate a new file descriptor
        const fd = this.fileID++;
        this.openFiles.set(fd, fileData);
        callback(null, fd);
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

        const fileData = this.openFiles.get(fd);
        if (!fileData) {
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
        const bytesToRead = Math.min(length, fileData.length - readPosition);

        if (bytesToRead <= 0) {
            callback(null, 0, buffer);
            return;
        }

        buffer.set(
            fileData.subarray(readPosition, readPosition + bytesToRead),
            offset,
        );

        if (position == null) {
            this.readPositions.set(fd, readPosition + bytesToRead);
        }

        callback(null, bytesToRead, buffer);
    }
}
