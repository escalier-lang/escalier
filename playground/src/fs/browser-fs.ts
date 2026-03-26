import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';

import { ErrnoException } from './errno-exception';
import type { FSAPI } from './fs-api';
import { FSEventEmitter } from './fs-events';
import type { FSDir, FSFile, FSNode, FSSymlink } from './fs-node';
import { SimpleStats } from './simple-stats';
import { type Volume, volumeToDir } from './volume';

// Defined in src/syscall/syscall_js.go in https://github.com/golang
const constants = {
    O_WRONLY: 1,
    O_RDWR: 2,
    O_CREAT: 64,
    O_TRUNC: 512,
    O_APPEND: 1024,
    O_EXCL: 128,
    O_DIRECTORY: 8192,
};

function assertNever(x: never): never {
    throw new Error(`Unexpected value: ${x}`);
}

export class BrowserFS implements FSAPI {
    fileID: number;
    openFiles: Map<number, FSNode> = new Map();
    readPositions: Map<number, number> = new Map();
    rootDir: FSDir;
    volume: Volume;
    readonly events = new FSEventEmitter();

    constructor(volume: Volume) {
        this.fileID = 3;
        this.volume = volume;
        this.rootDir = volumeToDir(volume);
    }

    private ensureContent(pathStr: string, node: FSFile): void {
        const entry = this.volume[pathStr];
        if (entry && entry.content === null && entry.url) {
            // Synchronous XHR doesn't support responseType = 'arraybuffer',
            // so we use charset=x-user-defined to get raw binary data as a
            // string where each character maps 1:1 to a byte value.  This
            // preserves the original UTF-8 bytes from the server, which is
            // what the Go WASM LSP expects when reading file content.
            const xhr = new XMLHttpRequest();
            xhr.open('GET', entry.url, false);
            xhr.overrideMimeType('text/plain; charset=x-user-defined');
            xhr.send();
            const text = xhr.responseText;
            const content = new Uint8Array(text.length);
            for (let i = 0; i < text.length; i++) {
                content[i] = text.charCodeAt(i) & 0xff;
            }
            entry.content = content;
            node.content = content;
        }
    }

    private static readonly SYMLINK_DEPTH_LIMIT = 40;

    /**
     * Resolve a path to its absolute form, handling `.` and `..` segments.
     */
    private resolvePath(pathStr: string): string {
        const parts = pathStr.split('/').filter((p) => p.length > 0);
        const resolved: string[] = [];
        for (const part of parts) {
            if (part === '.') continue;
            if (part === '..') {
                resolved.pop();
            } else {
                resolved.push(part);
            }
        }
        return `/${resolved.join('/')}`;
    }

    /**
     * Locate any node (file or directory) within the in‑memory directory tree.
     * Follows symlinks transparently (up to SYMLINK_DEPTH_LIMIT hops).
     *
     * @param pathStr Path string (e.g. "/foo/bar").
     * @param followLastSymlink Whether to follow a symlink if the final path
     *   component is one. `stat` passes true, `lstat` passes false.
     * @returns The FSNode if found, otherwise undefined.
     */
    private findNodeInRootDir(
        pathStr: string,
        followLastSymlink = true,
    ): FSNode | undefined {
        return this._findNode(pathStr, followLastSymlink, 0);
    }

    private _findNode(
        pathStr: string,
        followLastSymlink: boolean,
        depth: number,
    ): FSNode | undefined {
        if (depth > BrowserFS.SYMLINK_DEPTH_LIMIT) {
            return undefined; // ELOOP — circular symlink
        }

        const parts = pathStr.split('/').filter((p) => p.length > 0);
        let node: FSNode | undefined = this.rootDir;
        // Track the current absolute path for resolving relative symlink targets
        const currentParts: string[] = [];

        for (let i = 0; i < parts.length; i++) {
            const part = parts[i];
            if (!node || node.type !== 'dir') {
                return undefined;
            }
            node = node.children.get(part);
            if (!node) return undefined;

            currentParts.push(part);

            if (node.type === 'symlink') {
                // Don't follow the symlink if it's the last component and
                // followLastSymlink is false (lstat behavior).
                const isLast = i === parts.length - 1;
                if (isLast && !followLastSymlink) {
                    return node;
                }

                // Resolve symlink target
                let targetPath: string;
                if (node.target.startsWith('/')) {
                    targetPath = node.target;
                } else {
                    // Resolve relative to symlink's parent directory
                    const parentPath = `/${currentParts.slice(0, -1).join('/')}`;
                    targetPath = this.resolvePath(
                        `${parentPath}/${node.target}`,
                    );
                }

                // If there are remaining path segments, append them
                const remaining = parts.slice(i + 1);
                if (remaining.length > 0) {
                    targetPath = `${targetPath}/${remaining.join('/')}`;
                }

                return this._findNode(targetPath, followLastSymlink, depth + 1);
            }
        }

        return node;
    }

    /**
     * Find the parent directory for a given path and return both the
     * parent FSDir and the base name of the final path component.
     * Does NOT follow symlinks on the final component itself.
     */
    private findParent(
        pathStr: string,
    ): { parent: FSDir; name: string } | undefined {
        const resolved = this.resolvePath(pathStr);
        const parts = resolved.split('/').filter((p) => p.length > 0);
        if (parts.length === 0) return undefined; // can't get parent of root

        const parentPath = `/${parts.slice(0, -1).join('/')}`;
        const parent = this.findNodeInRootDir(parentPath, true);
        if (!parent || parent.type !== 'dir') return undefined;

        return { parent, name: parts[parts.length - 1] };
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
                // Note: fstat is called after open, which already ensures content
                const stats = new SimpleStats(
                    fileData.content.length,
                    true,
                    false,
                ) as unknown as Stats;
                callback(null, stats);
                break;
            }
            case 'symlink': {
                // Symlinks should be resolved by open(), but handle defensively
                const stats = new SimpleStats(
                    0,
                    false,
                    false,
                    true,
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
        const node = this.findNodeInRootDir(pathStr, false);
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
            this.ensureContent(pathStr, node);
            stats = new SimpleStats(
                node.content.length,
                true,
                false,
            ) as unknown as Stats;
        } else if (node.type === 'symlink') {
            stats = new SimpleStats(
                0,
                false,
                false,
                true,
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
                this.ensureContent(pathStr, node);
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
            case 'symlink': {
                // stat follows symlinks, so we should not normally reach here
                // (dangling symlink case)
                callback(
                    new ErrnoException('No such file or directory', {
                        code: 'ENOENT',
                        errno: -2,
                        syscall: 'stat',
                        path: pathStr,
                    }),
                    null as any,
                );
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
            constants.O_DIRECTORY !== undefined &&
            (_flags & constants.O_DIRECTORY) !== 0
        ) {
            const node = this.findNodeInRootDir(pathStr);
            if (!node) {
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
            if (node.type === 'dir') {
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
                this.ensureContent(pathStr, node);
                // Allocate a new file descriptor
                const fd = this.fileID++;
                this.openFiles.set(fd, node);
                callback(null, fd);
                break;
            }
            case 'symlink':
                // findNodeInRootDir follows symlinks, so this means dangling
                callback(
                    new ErrnoException('No such file or directory', {
                        code: 'ENOENT',
                        errno: -2,
                        syscall: 'open',
                        path: pathStr,
                    }),
                    -1,
                );
                break;
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
            case 'symlink':
                // Symlinks should be resolved by open(), handle defensively
                callback(
                    new ErrnoException('Bad file descriptor', {
                        code: 'EBADF',
                        errno: -9,
                        syscall: 'read',
                    }),
                    0,
                    buffer,
                );
                break;
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

    writeFile(
        path: PathLike,
        data: Uint8Array,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ) {
        const pathStr = String(path);
        const result = this.findParent(pathStr);
        if (!result) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'writeFile',
                    path: pathStr,
                }),
            );
            return;
        }

        const { parent, name } = result;
        const existing = parent.children.get(name);
        if (existing && existing.type === 'dir') {
            callback(
                new ErrnoException('Is a directory', {
                    code: 'EISDIR',
                    errno: -21,
                    syscall: 'writeFile',
                    path: pathStr,
                }),
            );
            return;
        }

        const isNew = !existing;
        const content = new Uint8Array(data);
        parent.children.set(name, { type: 'file', name, content });

        // Update the volume so ensureContent doesn't clobber the write
        this.volume[pathStr] = { content };

        if (isNew) {
            this.events.emit({ type: 'create', path: pathStr, kind: 'file' });
        }
        callback(null);
    }

    mkdir(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ) {
        const pathStr = String(path);
        const result = this.findParent(pathStr);
        if (!result) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'mkdir',
                    path: pathStr,
                }),
            );
            return;
        }

        const { parent, name } = result;
        if (parent.children.has(name)) {
            callback(
                new ErrnoException('File exists', {
                    code: 'EEXIST',
                    errno: -17,
                    syscall: 'mkdir',
                    path: pathStr,
                }),
            );
            return;
        }

        parent.children.set(name, {
            type: 'dir',
            name,
            children: new Map(),
        });
        this.events.emit({ type: 'create', path: pathStr, kind: 'dir' });
        callback(null);
    }

    unlink(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ) {
        const pathStr = String(path);
        const result = this.findParent(pathStr);
        if (!result) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'unlink',
                    path: pathStr,
                }),
            );
            return;
        }

        const { parent, name } = result;
        const node = parent.children.get(name);
        if (!node) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'unlink',
                    path: pathStr,
                }),
            );
            return;
        }
        if (node.type === 'dir') {
            callback(
                new ErrnoException('Is a directory', {
                    code: 'EISDIR',
                    errno: -21,
                    syscall: 'unlink',
                    path: pathStr,
                }),
            );
            return;
        }

        parent.children.delete(name);
        delete this.volume[pathStr];
        this.events.emit({ type: 'delete', path: pathStr, kind: 'file' });
        callback(null);
    }

    rmdir(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ) {
        const pathStr = String(path);
        const result = this.findParent(pathStr);
        if (!result) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'rmdir',
                    path: pathStr,
                }),
            );
            return;
        }

        const { parent, name } = result;
        const node = parent.children.get(name);
        if (!node) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'rmdir',
                    path: pathStr,
                }),
            );
            return;
        }
        if (node.type !== 'dir') {
            callback(
                new ErrnoException('Not a directory', {
                    code: 'ENOTDIR',
                    errno: -20,
                    syscall: 'rmdir',
                    path: pathStr,
                }),
            );
            return;
        }
        if (node.children.size > 0) {
            callback(
                new ErrnoException('Directory not empty', {
                    code: 'ENOTEMPTY',
                    errno: -39,
                    syscall: 'rmdir',
                    path: pathStr,
                }),
            );
            return;
        }

        parent.children.delete(name);
        this.events.emit({ type: 'delete', path: pathStr, kind: 'dir' });
        callback(null);
    }

    rename(
        oldPath: PathLike,
        newPath: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ) {
        const oldPathStr = String(oldPath);
        const newPathStr = String(newPath);

        const oldResult = this.findParent(oldPathStr);
        if (!oldResult) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'rename',
                    path: oldPathStr,
                }),
            );
            return;
        }

        const node = oldResult.parent.children.get(oldResult.name);
        if (!node) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'rename',
                    path: oldPathStr,
                }),
            );
            return;
        }

        const newResult = this.findParent(newPathStr);
        if (!newResult) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'rename',
                    path: newPathStr,
                }),
            );
            return;
        }

        const kind = node.type === 'dir' ? 'dir' : 'file';

        // Remove from old location
        oldResult.parent.children.delete(oldResult.name);
        if (this.volume[oldPathStr]) {
            this.volume[newPathStr] = this.volume[oldPathStr];
            delete this.volume[oldPathStr];
        }

        // Insert at new location with updated name
        node.name = newResult.name;
        newResult.parent.children.set(newResult.name, node);

        this.events.emit({
            type: 'rename',
            path: newPathStr,
            kind,
            oldPath: oldPathStr,
        });
        callback(null);
    }

    symlink(
        target: PathLike,
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ) {
        const pathStr = String(path);
        const targetStr = String(target);

        const result = this.findParent(pathStr);
        if (!result) {
            callback(
                new ErrnoException('No such file or directory', {
                    code: 'ENOENT',
                    errno: -2,
                    syscall: 'symlink',
                    path: pathStr,
                }),
            );
            return;
        }

        const { parent, name } = result;
        if (parent.children.has(name)) {
            callback(
                new ErrnoException('File exists', {
                    code: 'EEXIST',
                    errno: -17,
                    syscall: 'symlink',
                    path: pathStr,
                }),
            );
            return;
        }

        parent.children.set(name, {
            type: 'symlink',
            name,
            target: targetStr,
        });
        callback(null);
    }

    /**
     * Remove all entries except those under `node_modules/`.
     * Used when loading a new project to reset the filesystem.
     */
    clear() {
        for (const [name, _node] of this.rootDir.children) {
            if (name !== 'node_modules') {
                this.rootDir.children.delete(name);
            }
        }

        // Clean up volume entries
        for (const path in this.volume) {
            if (!path.startsWith('/node_modules/')) {
                delete this.volume[path];
            }
        }
    }
}
