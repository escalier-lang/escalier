import type { Stats } from 'node:fs';

export class SimpleStats implements Stats {
    // Basic file information
    size: number;
    private _isFile: boolean;
    private _isDirectory: boolean;
    private _isSymbolicLink: boolean;

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

    // Unix file type mode bits used by Go's WASM syscall layer.
    private static S_IFREG = 0o100000; // regular file
    private static S_IFDIR = 0o40000; // directory
    private static S_IFLNK = 0o120000; // symbolic link

    constructor(
        size: number,
        isFile: boolean,
        isDirectory: boolean,
        isSymbolicLink = false,
    ) {
        this.size = size;
        this._isFile = isFile;
        this._isDirectory = isDirectory;
        this._isSymbolicLink = isSymbolicLink;

        // Set the mode field so Go's WASM runtime can determine file type.
        // Go reads stat.mode and checks type bits rather than calling isDirectory().
        if (isDirectory) {
            // rwxr-xr-x: owner can read/write/traverse, others can read/traverse.
            this.mode = SimpleStats.S_IFDIR | 0o755;
        } else if (isSymbolicLink) {
            // rwxrwxrwx: symlink permissions are ignored — the target's
            // permissions are what matter, so they're conventionally all-on.
            this.mode = SimpleStats.S_IFLNK | 0o777;
        } else if (isFile) {
            // rw-r--r--: owner can read/write, others can only read.
            this.mode = SimpleStats.S_IFREG | 0o644;
        }
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
        return this._isSymbolicLink;
    }
    isFIFO(): boolean {
        return false;
    }
    isSocket(): boolean {
        return false;
    }
}
