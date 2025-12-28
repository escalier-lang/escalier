import type { PathLike, Stats, Mode, OpenMode } from 'node:fs';

export interface FSAPI {
    fstat(
        fd: number,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ): void;
    lstat(
        path: PathLike,
        callback: (err: NodeJS.ErrnoException | null, stats: Stats) => void,
    ): void;
    open(
        path: PathLike,
        flags: OpenMode | undefined,
        mode: Mode | undefined,
        callback: (err: NodeJS.ErrnoException | null, fd: number) => void,
    ): void;
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
}
