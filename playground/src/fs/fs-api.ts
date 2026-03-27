import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';

export interface FSAPI {
    fstat(
        fd: number,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ): void;
    lstat(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ): void;
    stat(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ): void;
    open(
        path: PathLike,
        flags: OpenMode | undefined,
        mode: Mode | undefined,
        callback: (err: NodeJS.ErrnoException | null, fd: number) => void,
    ): void;
    close(_fd: number, callback: (error: Error | null) => void): void;
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
    ): void;
    write(
        fd: number,
        buffer: Uint8Array,
        offset: number,
        length: number,
        position: number | null,
        callback: (
            err: NodeJS.ErrnoException | null,
            bytesWritten: number,
            buffer: Uint8Array,
        ) => void,
    ): void;
    readdir(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, files: string[]) => void,
    ): void;
    writeFile(
        path: PathLike,
        data: Uint8Array,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ): void;
    mkdir(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ): void;
    unlink(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ): void;
    rmdir(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ): void;
    rename(
        oldPath: PathLike,
        newPath: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ): void;
    symlink(
        target: PathLike,
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null) => void,
    ): void;
}
