import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';
import EventEmitter from 'eventemitter3';

import type { FSAPI } from '../fs/fs-api';

import './wasm_exec'; // run for side-effects

const Go = globalThis.Go;

// Whether a ServerRuntime in this thread is running. See ServerRuntime.run().
let runtimeRunning = false;

const decoder = new TextDecoder();

/**
 * Removes the listener that a stream's `on` added. The worker imports this module, so
 * it declares the type rather than importing vscode-jsonrpc's.
 */
export type Disposable = { dispose(): void };

const enosys = () => {
    const err = new Error('not implemented');
    // @ts-ignore
    err.code = 'ENOSYS';
    return err;
};

/**
 * A queue of byte chunks. The server reads its stdin from one, a few bytes at a time,
 * and a listener on `data` hears each chunk as it is written.
 */
export class SimpleStream {
    private chunks: Array<Uint8Array>;
    private emitter: EventEmitter;

    constructor() {
        this.chunks = [];
        this.emitter = new EventEmitter();
    }

    on(event: string, listener: (chunk: Uint8Array) => void): Disposable {
        this.emitter.on(event, listener);
        return { dispose: () => this.emitter.off(event, listener) };
    }

    write(chunk: Uint8Array) {
        this.chunks.push(chunk);
        this.emitter.emit('data', chunk);
    }

    /**
     * Removes and returns up to maxLength bytes from the front of the stream, or
     * undefined when the stream is empty. A chunk longer than maxLength stays
     * queued with its first maxLength bytes removed.
     */
    read(maxLength = Number.POSITIVE_INFINITY): Uint8Array | undefined {
        const chunk = this.chunks.shift();
        if (chunk && chunk.length > maxLength) {
            this.chunks.unshift(chunk.subarray(maxLength));
            return chunk.subarray(0, maxLength);
        }
        return chunk;
    }
}

/**
 * Passes each byte chunk written to it to the listeners on `data`, and keeps none. The
 * server writes its stdout to one.
 */
export class OutputStream {
    private emitter = new EventEmitter();

    on(event: 'data', listener: (chunk: Uint8Array) => void): Disposable {
        this.emitter.on(event, listener);
        return { dispose: () => this.emitter.off(event, listener) };
    }

    write(chunk: Uint8Array) {
        this.emitter.emit('data', chunk);
    }
}

/** How much of a server's stderr a StderrTail keeps. */
export const STDERR_TAIL_CHARS = 64 * 1024;

/** The end of what a server wrote to stderr, at most STDERR_TAIL_CHARS long. */
export class StderrTail {
    /** At least the last STDERR_TAIL_CHARS of the text, and at most twice that. */
    private tail = '';

    /**
     * Appends text. The Go runtime prints a panic in many small writes, so the tail is
     * cut back to STDERR_TAIL_CHARS only once it reaches twice that, rather than copied
     * on every write.
     */
    record(text: string) {
        this.tail += text;
        if (this.tail.length > 2 * STDERR_TAIL_CHARS) {
            this.tail = this.tail.slice(-STDERR_TAIL_CHARS);
        }
    }

    /** Returns the last STDERR_TAIL_CHARS of the text recorded so far. */
    text(): string {
        return this.tail.slice(-STDERR_TAIL_CHARS);
    }
}

/** Returns err as text, with its stack when it has one. */
export function describeError(err: unknown): string {
    return err instanceof Error ? (err.stack ?? String(err)) : String(err);
}

/** What a ServerRuntime runs the server with. */
export type ServerRuntimeOptions = {
    /** The directory the server's `os.Getwd` returns. */
    cwd: string;
    /** Serves every file the server opens other than stdin, stdout and stderr. */
    fs: FSAPI;
    /** Environment variables the server's `os.Getenv` sees. */
    env: Record<string, string>;
    /** The bytes the server reads from stdin. */
    stdin: SimpleStream;
    /** Takes the bytes the server writes to stdout. */
    stdout: OutputStream;
    /** Called with each piece of text the server writes to stderr. */
    onStderr?: (text: string) => void;
};

/**
 * Runs the compiled language server on the Go runtime in the current thread. It wires
 * the server's stdin and stdout to the given streams and its file syscalls to an FSAPI.
 */
export class ServerRuntime {
    private go: any;
    private module: WebAssembly.Module;
    private stdin: SimpleStream;
    private stdout: OutputStream;
    private stderr = new StderrTail();
    private onStderr: (text: string) => void;
    /** The stderr text since the last newline, which writeSync prints as one line. */
    private errorBuffer = '';
    /** Set by silence(). Keeps the server's stderr out of the console. */
    private silenced = false;

    // Go resolves `globalThis.fs` and `globalThis.process` on every syscall,
    // so whichever value is installed last serves every runtime in the thread
    // rather than the one that installed it. These are built here and installed
    // in run(), which keeps constructing a runtime from reaching into a running one.
    private fsShim: unknown;
    private processShim: unknown;

    constructor(module: WebAssembly.Module, options: ServerRuntimeOptions) {
        this.module = module;
        this.stdin = options.stdin;
        this.stdout = options.stdout;
        this.onStderr = options.onStderr ?? (() => {});
        const { fs, cwd } = options;

        this.fsShim = {
            // Defined in src/syscall/syscall_js.go in https://github.com/golang
            constants: {
                O_WRONLY: 1,
                O_RDWR: 2,
                O_CREAT: 64,
                O_TRUNC: 512,
                O_APPEND: 1024,
                O_EXCL: 128,
                O_DIRECTORY: 8192,
            },
            // The Go runtime calls writeSync for what `print` and a panic write.
            writeSync: (fd: number, buffer: Uint8Array) => {
                if (fd === 1) {
                    const value = decoder.decode(buffer);
                    console.log('writeSync:', value);
                } else if (fd === 2) {
                    const value = decoder.decode(buffer);
                    this.recordStderr(value);
                    if (value === '\n') {
                        if (!this.silenced) {
                            console.error(this.errorBuffer);
                        }
                        this.errorBuffer = '';
                    } else {
                        this.errorBuffer += value;
                    }
                } else {
                    console.log('writeSync:', fd, buffer);
                }
            },
            write: (
                fd: number,
                buffer: Uint8Array,
                _offset: number,
                length: number,
                _position: number | null | undefined,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    written: number,
                    buffer: Uint8Array,
                ) => void,
            ) => {
                if (fd === 1) {
                    // The caller reuses buffer once the callback runs, so the stream
                    // gets a copy.
                    this.stdout.write(buffer.slice(0, length));
                    setTimeout(() => {
                        callback(null, length, buffer);
                    }, 0);
                } else if (fd === 2) {
                    const value = decoder.decode(buffer);
                    this.recordStderr(value);
                    if (!this.silenced) {
                        console.warn('[Escalier LSP] -', value);
                    }
                    setTimeout(() => {
                        callback(null, length, buffer);
                    }, 0);
                } else {
                    fs.write(
                        fd,
                        buffer,
                        0,
                        length,
                        null,
                        (err, bytesWritten, buf) => {
                            setTimeout(() => {
                                callback(err, bytesWritten, buf);
                            }, 0);
                        },
                    );
                }
            },
            close(fd: number, callback: (error: Error | null) => void) {
                if (fd > 2) {
                    return fs.close(fd, callback);
                }
                setTimeout(() => {
                    callback(null);
                }, 0);
            },
            fstat(
                fd: number,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    stats: Stats,
                ) => void,
            ) {
                return fs.fstat(fd, callback);
            },
            lstat(
                path: PathLike,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    stats: Stats,
                ) => void,
            ) {
                return fs.lstat(path, callback);
            },
            mkdir(
                path: PathLike,
                _perm: number,
                callback: (err: NodeJS.ErrnoException | null) => void,
            ) {
                return fs.mkdir(path, callback);
            },
            open(
                path: PathLike,
                flags: OpenMode | undefined,
                mode: Mode | undefined,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    fd: number,
                ) => void,
            ) {
                return fs.open(path, flags, mode, callback);
            },
            read: (
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
            ) => {
                if (fd === 0) {
                    // The server reads stdin through the 4,096-byte bufio.Reader
                    // that sourcegraph/jsonrpc2 creates, and a message can be longer.
                    // Reading at most length bytes leaves the rest of the chunk
                    // queued for the next call.
                    //
                    // An empty stdin answers once the next chunk arrives, so an idle
                    // server waits without polling. That answer goes on a timer
                    // because a chunk arrives inside the writer's call, and the
                    // callback runs the server.
                    const answer = () => {
                        const srcBuf = this.stdin.read(length);
                        if (!srcBuf) {
                            const subscription = this.stdin.on('data', () => {
                                subscription.dispose();
                                setTimeout(answer, 0);
                            });
                            return;
                        }
                        buffer.set(srcBuf, offset);
                        callback(null, srcBuf.length, buffer);
                    };
                    answer();
                } else {
                    return fs.read(
                        fd,
                        buffer,
                        offset,
                        length,
                        position,
                        callback,
                    );
                }
            },
            readdir(
                path: PathLike,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    files: string[],
                ) => void,
            ) {
                return fs.readdir(path, callback);
            },
            rename(
                from: PathLike,
                to: PathLike,
                callback: (err: NodeJS.ErrnoException | null) => void,
            ) {
                return fs.rename(from, to, callback);
            },
            rmdir(
                path: PathLike,
                callback: (err: NodeJS.ErrnoException | null) => void,
            ) {
                return fs.rmdir(path, callback);
            },
            stat(
                path: PathLike,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    stats: Stats,
                ) => void,
            ) {
                return fs.stat(path, callback);
            },
            symlink(
                target: PathLike,
                path: PathLike,
                callback: (err: NodeJS.ErrnoException | null) => void,
            ) {
                return fs.symlink(target, path, callback);
            },
            unlink(
                path: PathLike,
                callback: (err: NodeJS.ErrnoException | null) => void,
            ) {
                return fs.unlink(path, callback);
            },
        };

        this.processShim = {
            getuid() {
                return -1;
            },
            getgid() {
                return -1;
            },
            geteuid() {
                return -1;
            },
            getegid() {
                return -1;
            },
            getgroups() {
                throw enosys();
            },
            pid: -1,
            ppid: -1,
            umask() {
                throw enosys();
            },
            cwd() {
                return cwd;
            },
            chdir() {
                throw enosys();
            },
        };

        this.go = new Go();
        this.go.env = { ...(this.go.env ?? {}), ...options.env };

        // A Go panic ends the program through `wasmExit`, which settles `go.run()`. A
        // WebAssembly trap, such as running out of memory, instead throws out of
        // `_resume()` into whichever callback called it, and `go.run()` never settles.
        // Marking the instance exited and settling `go.run()` here reports a trap the
        // way an exit is reported. wasm_exec.js is vendored, so its private `_resume` is
        // safe to wrap.
        const go = this.go;
        const resume = go._resume.bind(go);
        go._resume = () => {
            try {
                resume();
            } catch (err) {
                // A callback scheduled before the program exited can still call
                // `_resume()`, which then throws on the torn-down instance. Only an error
                // from a running instance is a trap.
                if (!go.exited) {
                    console.error('[Escalier LSP] the server trapped:', err);
                    this.recordStderr(`\n${describeError(err)}\n`);
                    go.exited = true;
                }
                go._resolveExitPromise();
            }
        };
    }

    /**
     * Instantiates the server and runs it until it exits or traps. Rejects when the
     * instance can't be created, or when another runtime in this thread is running.
     */
    async run(): Promise<void> {
        // One set of globals serves every runtime in a thread, so a second one started
        // while the first is alive would take over the first's streams and the first
        // would never read another message. Refusing says so at the point of the
        // mistake.
        if (runtimeRunning) {
            throw new Error(
                'an LSP server is already running in this thread: only one can run at a time',
            );
        }
        runtimeRunning = true;
        try {
            globalThis.fs = this.fsShim as typeof globalThis.fs;
            globalThis.process = this.processShim as typeof globalThis.process;
            const instance = await WebAssembly.instantiate(
                this.module,
                this.go.importObject,
            );
            await this.go.run(instance);
        } finally {
            runtimeRunning = false;
        }
    }

    /**
     * Runs the server as run() does, and resolves with the end of its stderr once it has
     * ended. A failure to start is logged and appended to that text rather than rejected.
     */
    async runToEnd(): Promise<string> {
        try {
            await this.run();
        } catch (err) {
            console.error('[Escalier LSP] the server failed:', err);
            this.recordStderr(`\n${describeError(err)}\n`);
        }
        return this.stderrText();
    }

    /** Returns the end of what the server has written to stderr. */
    stderrText(): string {
        return this.stderr.text();
    }

    /**
     * Keeps the server's stderr out of the console from now on. It is still recorded
     * and passed to onStderr.
     */
    silence() {
        this.silenced = true;
    }

    private recordStderr(text: string) {
        this.stderr.record(text);
        this.onStderr(text);
    }
}
