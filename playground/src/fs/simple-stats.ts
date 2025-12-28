import type { Stats } from 'node:fs';

export class SimpleStats implements Stats {
    // Basic file information
    size: number;
    private _isFile: boolean;
    private _isDirectory: boolean;

    // Additional Stats fields required by Node's Stats interface
    dev = 0;
    ino = 0;
    mode = 0;
    nlink = 1;
    uid = 0;
    gid = 0;
    rdev = 0;
    blksize = 0;
    blocks = 0;
    atimeMs: number = Date.now();
    mtimeMs: number = Date.now();
    ctimeMs: number = Date.now();
    birthtimeMs: number = Date.now();
    atime: Date = new Date(this.atimeMs);
    mtime: Date = new Date(this.mtimeMs);
    ctime: Date = new Date(this.ctimeMs);
    birthtime: Date = new Date(this.birthtimeMs);

    constructor(size: number, isFile: boolean, isDirectory: boolean) {
        this.size = size;
        this._isFile = isFile;
        this._isDirectory = isDirectory;
    }

    isFile(): boolean {
        return this._isFile;
    }

    isDirectory(): boolean {
        return this._isDirectory;
    }

    // Stub methods to satisfy the Node.js Stats interface
    isBlockDevice(): boolean {
        return false;
    }
    isCharacterDevice(): boolean {
        return false;
    }
    isSymbolicLink(): boolean {
        return false;
    }
    isFIFO(): boolean {
        return false;
    }
    isSocket(): boolean {
        return false;
    }
}
