import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';

import EventEmitter from 'eventemitter3';
import type * as lsp from 'vscode-languageserver-protocol';
import {
    Disposable,
    type MessageConnection,
    type RAL,
    ReadableStreamMessageReader,
    WriteableStreamMessageWriter,
    createMessageConnection,
} from 'vscode-languageserver-protocol/browser';

import type { FSAPI } from '#fs/fs-api';

import './wasm_exec'; // run for side-effects

const Go = globalThis.Go;

// The Client whose shims `globalThis` currently carries, or undefined when no
// runtime is running. See run().
let runningClient: Client | undefined;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

const enosys = () => {
    const err = new Error('not implemented');
    // @ts-ignore
    err.code = 'ENOSYS';
    return err;
};

class SimpleStream {
    private chunks: Array<Uint8Array>;
    private emitter: EventEmitter;

    constructor() {
        this.chunks = [];
        this.emitter = new EventEmitter();
    }

    on(event: string, listener: (chunk: Uint8Array) => void): Disposable {
        this.emitter.on(event, listener);
        return Disposable.create(() => this.emitter.off(event, listener));
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
 * Returns a Node `Buffer` view of chunk where `Buffer` exists, and chunk itself
 * elsewhere. vscode-jsonrpc's Node runtime, which a Node test run loads, slices
 * the bytes it reads with `Buffer.from(chunk, 0, length)`. Node ignores those
 * bounds for a plain `Uint8Array`, so a chunk holding more than one message
 * misreads the second. The browser runtime accepts either type.
 */
export function asNodeBuffer(chunk: Uint8Array): Uint8Array {
    if (typeof Buffer === 'undefined') {
        return chunk;
    }
    return Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
}

/** Subscribes to an event the in-memory streams never fire. */
const neverFires = () => Disposable.create(() => {});

/**
 * Returns stream as the readable stream a jsonrpc message reader takes. The reader
 * splits the bytes the server writes to stdout into messages.
 */
function readableOf(stream: SimpleStream): RAL.ReadableStream {
    return {
        onData: (listener) =>
            stream.on('data', (chunk) => listener(asNodeBuffer(chunk))),
        onClose: neverFires,
        onError: neverFires,
        onEnd: neverFires,
    };
}

/**
 * Returns stream as the writable stream a jsonrpc message writer takes. The writer
 * frames each message it writes, and the server reads them from stdin.
 */
function writableOf(stream: SimpleStream): RAL.WritableStream {
    return {
        onClose: neverFires,
        onError: neverFires,
        onEnd: neverFires,
        write(data: Uint8Array | string) {
            stream.write(
                typeof data === 'string' ? encoder.encode(data) : data,
            );
            return Promise.resolve();
        },
        end() {},
    };
}

/**
 * How long a server has to run before its crash restarts the next one at once. Crashes
 * that come sooner, one after another, wait longer each time.
 */
export const QUICK_CRASH_MS = 10_000;

/**
 * How long a server that replaced a crashed one has to run before its crash no longer
 * counts as a crash on the same input as the one before. A new server loads the standard
 * library before its first check, which can take tens of seconds on a slow machine, so
 * this is well past QUICK_CRASH_MS.
 */
export const SAME_INPUT_CRASH_MS = 60_000;

/** The longest the client waits before it starts a server again after a crash. */
export const MAX_RESTART_DELAY_MS = 30_000;

/** The facts about a crash that decide what the client does next. */
export type CrashFacts = {
    /** Whether the crashed server was started to replace one that crashed before it. */
    wasRestart: boolean;
    /** Whether the open documents changed while the crashed server ran. */
    inputChanged: boolean;
    /** How long the crashed server ran, in milliseconds. */
    ranForMs: number;
    /** How many crashes in a row, before this one, came within QUICK_CRASH_MS of a start. */
    quickCrashes: number;
};

/** What the client does after a crash. */
export type RestartDecision =
    | { kind: 'restart'; delayMs: number }
    | { kind: 'wait-for-edit' };

/**
 * Returns what the client does after a crash.
 * - A server that replaced a crashed one, and crashed within SAME_INPUT_CRASH_MS before
 *   any edit reached it, crashed on the input that killed the server before it. Another
 *   would crash the same way, so the client waits for an edit.
 * - Any other server that ran for QUICK_CRASH_MS or longer is started again at once. That
 *   includes a replacement that ran past SAME_INPUT_CRASH_MS, whose crash came from
 *   something other than the documents it opened, such as a request or running out of
 *   memory.
 * - A quicker crash is started again after a delay that doubles with each such crash in
 *   a row, up to MAX_RESTART_DELAY_MS.
 */
export function decideRestart(facts: CrashFacts): RestartDecision {
    if (
        facts.wasRestart &&
        !facts.inputChanged &&
        facts.ranForMs < SAME_INPUT_CRASH_MS
    ) {
        return { kind: 'wait-for-edit' };
    }
    if (facts.ranForMs >= QUICK_CRASH_MS) {
        return { kind: 'restart', delayMs: 0 };
    }
    return {
        kind: 'restart',
        delayMs: Math.min(1000 * 2 ** facts.quickCrashes, MAX_RESTART_DELAY_MS),
    };
}

/**
 * What the client reports when the server stops without being asked to.
 * - `restarted`: a new server is running and has the open documents.
 * - `waiting-for-edit`: the server crashed again on the same documents, and the client
 *   starts a new one at the next edit.
 *
 * `report` is what the crashed server printed about the crash, for a bug report. See
 * crashReportFrom.
 */
export type ServerCrashEvent = {
    kind: 'restarted' | 'waiting-for-edit';
    report: string;
};

/**
 * A panic the server recovered from, as its `escalier/panicReport` notification sends it.
 * The server keeps running after one.
 */
export type PanicReport = { message: string; stack: string };

/** How much of the server's stderr the client keeps for a crash report. */
const STDERR_TAIL_CHARS = 64 * 1024;

/** How much of the server's stderr a crash report holds when no panic header is found. */
const CRASH_REPORT_FALLBACK_CHARS = 4 * 1024;

/**
 * Returns the crash report in stderr, the tail of what a server printed before it exited.
 * The report runs from the last line that starts `panic: ` or `fatal error: `, which is
 * where the Go runtime begins what it prints when a panic ends the program. Without such
 * a line, the report is the last few kilobytes.
 */
export function crashReportFrom(stderr: string): string {
    const headers = [...stderr.matchAll(/^(?:panic|fatal error): /gm)];
    const start = headers.at(-1)?.index;
    const report =
        start === undefined
            ? stderr.slice(-CRASH_REPORT_FALLBACK_CHARS)
            : stderr.slice(start);
    return (
        report.trim() || 'The language server exited without printing an error.'
    );
}

/** The message a request rejects with while the client waits for an edit to restart. */
export const SERVER_DOWN_MESSAGE =
    'the language server crashed and restarts at the next edit';

/**
 * Holds requests while a server starts after a crash. Once it is known whether a server
 * will answer, the held requests either go ahead or fail.
 */
export class RequestGate {
    private gate: Promise<void> = Promise.resolve();
    private settle: ((err?: Error) => void) | undefined;

    /** Returns a promise that settles when requests may go, or rejects when they fail. */
    wait(): Promise<void> {
        return this.gate;
    }

    /** Makes later calls to wait() pend. A gate that is already closed stays as it is. */
    close() {
        if (this.settle) {
            return;
        }
        let resolve!: () => void;
        let reject!: (err: Error) => void;
        this.gate = new Promise<void>((res, rej) => {
            resolve = res;
            reject = rej;
        });
        // A gate can fail with no request waiting on it.
        this.gate.catch(() => {});
        this.settle = (err) => {
            this.settle = undefined;
            this.gate = Promise.resolve();
            if (err) {
                reject(err);
            } else {
                resolve();
            }
        };
    }

    /** Lets the waiting requests, and later ones, go. */
    open() {
        this.settle?.();
    }

    /**
     * Rejects the waiting requests with err. Later calls to wait() resolve until the gate
     * is closed again.
     */
    fail(err: Error) {
        this.settle?.(err);
    }
}

/** The state of the client's server. */
type ServerState =
    /** A server is running and takes messages. A client that hasn't run() yet is here too. */
    | 'running'
    /** A server is starting after a crash. Requests wait for it. */
    | 'restarting'
    /**
     * A server that replaced a crashed one crashed before any edit reached it. Requests
     * fail until the next edit starts another.
     */
    | 'waiting-for-edit';

/** Returns err as text, with its stack when it has one. */
function describeError(err: unknown): string {
    return err instanceof Error ? (err.stack ?? String(err)) : String(err);
}

export class Client {
    // The runtime's parts, assigned by createRuntime(). The fs shim reads the streams
    // through `this` on every call, so a restart's new streams serve the new runtime.
    private go: any;
    private stdin!: SimpleStream;
    private stdout!: SimpleStream;
    /** Sends requests and notifications over stdin and reads the replies from stdout. */
    private connection!: MessageConnection;
    private wasmBuf: ArrayBuffer;
    private errorBuffer: string;
    /**
     * What the current server wrote to stderr, at least the last STDERR_TAIL_CHARS of it
     * and at most twice that.
     */
    private stderrTail = '';
    /** Set by `stop()`. Silences the server's output and drops notifications from then on. */
    private stopped = false;
    /** Set when `stop()` begins, so the server's exit isn't taken for a crash. */
    private stopping = false;

    private env: Record<string, string>;
    /** The compiled server, kept so a restart doesn't compile it again. */
    private wasmModule: WebAssembly.Module | undefined;
    private state: ServerState = 'running';
    /** Holds requests while a server starts after a crash. */
    private requests = new RequestGate();
    /**
     * Notifications other than document changes, sent while a server starts after a crash.
     * The server gets them once it has opened the documents.
     */
    private queuedNotifications: Array<[string, unknown]> = [];
    /** The report on the last crash, which the crash event that follows it carries. */
    private crashReport = '';
    /** The answer to the `initialize` that a restart sends when it reopens. */
    private initializeResult: lsp.InitializeResult | undefined;
    /** Wakes run() while it waits for an edit, from the next edit or from stop(). */
    private wake: () => void = () => {};
    /** The params of the last `initialize`, which a restart sends again. */
    private initializeParams: lsp.InitializeParams | undefined;
    /** The text of every open document, which a restart opens again. */
    private documents = new Map<string, lsp.TextDocumentItem>();
    private crashEvents = new EventEmitter();

    /**
     * Calls every listener registered for a notification's method. A
     * MessageConnection keeps one handler per method, so the client registers
     * one handler that emits here.
     */
    private notifications = new EventEmitter();

    // Go resolves `globalThis.fs` and `globalThis.process` on every syscall,
    // so whichever value is installed last serves every runtime rather than
    // the one that installed it. These are built here and installed in run(),
    // which keeps constructing a Client from reaching into a running one.
    private fsShim: unknown;
    private processShim: unknown;

    constructor(
        wasmBuf: ArrayBuffer,
        cwd: string,
        fs: FSAPI,
        env: Record<string, string> = {},
    ) {
        this.wasmBuf = wasmBuf;
        this.env = env;
        this.errorBuffer = '';

        // const enosys = () => {
        //     const err = new Error('not implemented');
        //     // @ts-ignore
        //     err.code = 'ENOSYS';
        //     return err;
        // };

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
            // writeSync(fd, buf) {
            //     outputBuf += decoder.decode(buf);
            //     const nl = outputBuf.lastIndexOf("\n");
            //     if (nl != -1) {
            //         console.log(outputBuf.substring(0, nl));
            //         outputBuf = outputBuf.substring(nl + 1);
            //     }
            //     return buf.length;
            // },
            writeSync: (fd: number, buffer: Uint8Array) => {
                if (fd === 1) {
                    const value = decoder.decode(buffer);
                    console.log('writeSync:', value);
                } else if (fd === 2) {
                    const value = decoder.decode(buffer);
                    this.recordStderr(value);
                    if (value === '\n') {
                        console.error(this.errorBuffer);
                        this.errorBuffer = '';
                    } else {
                        this.errorBuffer += value;
                    }
                } else {
                    console.log('writeSync:', fd, buffer);
                }
            },
            // write(fd, buf, offset, length, position, callback) {
            //     if (offset !== 0 || length !== buf.length || position !== null) {
            //         callback(enosys());
            //         return;
            //     }
            //     const n = this.writeSync(fd, buf);
            //     callback(null, n);
            // },
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
                    this.stdout.write(buffer);
                    setTimeout(() => {
                        callback(null, length, buffer);
                    }, 0);
                } else if (fd === 2) {
                    const value = decoder.decode(buffer);
                    this.recordStderr(value);
                    if (!this.stopped) {
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
            // chmod(path, mode, callback) {callback(enosys())},
            // chown(path, uid, gid, callback) {callback(enosys())},
            close(fd: number, callback: (error: Error | null) => void) {
                if (fd > 2) {
                    return fs.close(fd, callback);
                }
                setTimeout(() => {
                    callback(null);
                }, 0);
            },
            // fchmod(fd, mode, callback) {callback(enosys())},
            // fchown(fd, uid, gid, callback) {callback(enosys())},
            fstat(
                fd: number,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    stats: Stats,
                ) => void,
            ) {
                return fs.fstat(fd, callback);
            },
            // fsync(fd, callback) {callback(null)},
            // ftruncate(fd, length, callback) {callback(enosys())},
            // lchown(path, uid, gid, callback) {callback(enosys())},
            // link(path, link, callback) {callback(enosys())},
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
                    const srcBuf = this.stdin.read(length);
                    if (srcBuf) {
                        buffer.set(srcBuf, offset);
                        callback(null, srcBuf.length, buffer);
                        return;
                    }

                    // We use setImmediate before calling the callback so that
                    // other async code can run before we call the callback.
                    // Calling the callback immediately prevents all promises
                    // from running because the server immediately tries to read
                    // again.
                    setTimeout(() => {
                        const error = new Error();
                        // @ts-ignore
                        error.code = 'EAGAIN';
                        callback(error, 0, buffer);
                    }, 0);
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
            // readSync(...args) {console.log('readFileSync:', args)},
            readdir(
                path: PathLike,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    files: string[],
                ) => void,
            ) {
                return fs.readdir(path, callback);
            },
            // readlink(path, callback) {callback(enosys())},
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
            // truncate(path, length, callback) {callback(enosys())},
            unlink(
                path: PathLike,
                callback: (err: NodeJS.ErrnoException | null) => void,
            ) {
                return fs.unlink(path, callback);
            },
            // utimes(path, atime, mtime, callback) {callback(enosys())},
        };

        // @ts-ignore
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

        // TODO: make this a proxy object so that we can trap other fs methods
        // globalThis.fs = {
        //     // ...fs,

        // };

        this.createRuntime();
    }

    /**
     * Creates the streams, the connection and the Go instance for a server, either the
     * first one or one that replaces a crashed server.
     */
    private createRuntime() {
        this.stdin = new SimpleStream();
        this.stdout = new SimpleStream();
        this.errorBuffer = '';
        this.stderrTail = '';
        this.connection = createMessageConnection(
            new ReadableStreamMessageReader(readableOf(this.stdout)),
            new WriteableStreamMessageWriter(writableOf(this.stdin)),
        );
        // A MessageConnection keeps its handlers, so the new one needs every method a
        // listener was registered for.
        for (const method of this.notifications.eventNames()) {
            this.connection.onNotification(String(method), (params) =>
                this.notifications.emit(method, params),
            );
        }
        this.connection.listen();

        this.go = new Go();
        // Forward caller-supplied env vars to the Go runtime so
        // `os.Getenv` sees them. The playground passes
        // ESCALIER_BUILTINS_DIR to point the interop loader at the
        // virtual-filesystem path where the compiler's
        // `internal/interop/data` directory has been mirrored.
        this.go.env = { ...this.go.env, ...this.env };

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

    async run() {
        // One set of globals serves every runtime, so a second one started
        // while the first is alive would take over the first's streams and
        // the first would never read another message. Refusing says so at the
        // point of the mistake.
        if (runningClient !== undefined) {
            throw new Error(
                'an LSP server is already running: only one Client can run at a time',
            );
        }
        // oxlint-disable-next-line typescript/no-this-alias -- runningClient records the one Client that may run, not an alias for this method
        runningClient = this;

        globalThis.fs = this.fsShim as typeof globalThis.fs;
        globalThis.process = this.processShim as typeof globalThis.process;

        try {
            let quickCrashes = 0;
            for (let restart = false; ; restart = true) {
                const startedAt = Date.now();
                const input = this.documentsKey();
                try {
                    await this.runServer(restart);
                } catch (err) {
                    // Compiling or instantiating the server failed, or it trapped before
                    // its first callback. That is a crash like any other.
                    console.error('[Escalier LSP] the server failed:', err);
                    this.recordStderr(`\n${describeError(err)}\n`);
                }
                if (this.stopping) {
                    return;
                }
                // Read before createRuntime() starts the next server's stderr.
                this.crashReport = crashReportFrom(
                    this.stderrTail.slice(-STDERR_TAIL_CHARS),
                );

                const decision = decideRestart({
                    wasRestart: restart,
                    inputChanged: this.documentsKey() !== input,
                    ranForMs: Date.now() - startedAt,
                    quickCrashes,
                });
                this.beginRestart();
                if (decision.kind === 'wait-for-edit') {
                    this.state = 'waiting-for-edit';
                    this.queuedNotifications = [];
                    this.requests.fail(new Error(SERVER_DOWN_MESSAGE));
                    this.crashEvents.emit('crash', {
                        kind: 'waiting-for-edit',
                        report: this.crashReport,
                    });
                    await new Promise<void>((resolve) => {
                        this.wake = resolve;
                    });
                    this.requests.close();
                    quickCrashes = 0;
                } else {
                    quickCrashes =
                        decision.delayMs === 0 ? 0 : quickCrashes + 1;
                    await new Promise((resolve) =>
                        setTimeout(resolve, decision.delayMs),
                    );
                }
                if (this.stopping) {
                    return;
                }
                this.state = 'restarting';
                this.createRuntime();
            }
        } finally {
            runningClient = undefined;
        }
    }

    /**
     * Runs the current Go instance until it exits. When restart is true, the instance
     * replaces a crashed server, so it is sent the last `initialize` and opens every
     * document, and the client reports `restarted` once it has them.
     */
    private async runServer(restart: boolean) {
        this.wasmModule ??= await WebAssembly.compile(this.wasmBuf);
        const instance = await WebAssembly.instantiate(
            this.wasmModule,
            this.go.importObject,
        );
        const exited = this.go.run(instance).then(() => 'exited' as const);
        if (restart) {
            const reopened = this.reopen().then(
                () => 'reopened' as const,
                // The server crashed before it answered, and the exit below reports it.
                () => 'exited' as const,
            );
            if ((await Promise.race([reopened, exited])) === 'reopened') {
                this.state = 'running';
                this.requests.open();
                this.crashEvents.emit('crash', {
                    kind: 'restarted',
                    report: this.crashReport,
                });
            }
        }
        await exited;
    }

    /**
     * Sends the new server the last `initialize`, opens every open document, and then
     * sends the notifications queued while it started.
     */
    private async reopen() {
        if (this.initializeParams) {
            this.initializeResult = await this.connection.sendRequest(
                'initialize',
                this.initializeParams,
            );
        }
        // An edit can land while a didOpen is being written. Each pass opens the documents
        // that changed since the pass before and closes the ones that were closed, until a
        // pass finds nothing to send. The caller marks the server running in the same
        // microtask chain as the last pass, so no edit lands in between.
        const sent = new Map<string, lsp.TextDocumentItem>();
        for (;;) {
            const opened = [...this.documents.values()].filter(
                (doc) => sent.get(doc.uri) !== doc,
            );
            const closed = [...sent.keys()].filter(
                (uri) => !this.documents.has(uri),
            );
            if (opened.length === 0 && closed.length === 0) {
                break;
            }
            for (const uri of closed) {
                sent.delete(uri);
                await this.connection.sendNotification(
                    'textDocument/didClose',
                    { textDocument: { uri } },
                );
            }
            for (const textDocument of opened) {
                sent.set(textDocument.uri, textDocument);
                await this.connection.sendNotification('textDocument/didOpen', {
                    textDocument,
                });
            }
        }
        for (
            let next = this.queuedNotifications.shift();
            next;
            next = this.queuedNotifications.shift()
        ) {
            await this.connection.sendNotification(...next);
        }
    }

    /**
     * Makes requests wait for the next server. A request pending on the crashed server's
     * connection rejects, since nothing will answer it.
     */
    private beginRestart() {
        this.state = 'restarting';
        this.initializeResult = undefined;
        this.requests.close();
        this.connection.dispose();
    }

    /**
     * Appends text to stderrTail. The Go runtime prints a panic in many small writes, so
     * the tail is cut back to STDERR_TAIL_CHARS only once it reaches twice that, rather
     * than copied on every write.
     */
    private recordStderr(text: string) {
        this.stderrTail += text;
        if (this.stderrTail.length > 2 * STDERR_TAIL_CHARS) {
            this.stderrTail = this.stderrTail.slice(-STDERR_TAIL_CHARS);
        }
    }

    /** Returns a key that changes whenever an open document's text does. */
    private documentsKey(): string {
        return JSON.stringify(
            [...this.documents.values()].map((doc) => [doc.uri, doc.text]),
        );
    }

    /**
     * Calls callback when the server stops without being asked to, once the client has
     * decided what to do about it.
     */
    onServerCrash(callback: (event: ServerCrashEvent) => void) {
        this.crashEvents.on('crash', callback);
    }

    //
    // Lifecycle methods
    //

    /**
     * Sends the server `initialize`. The caller names the workspace in
     * workspaceFolders, and the request's rootUri is the first folder's URI, or null
     * when there is none.
     */
    async initialize(
        params: Omit<lsp.InitializeParams, 'rootUri'>,
    ): Promise<lsp.InitializeResult> {
        const request: lsp.InitializeParams = {
            ...params,
            // oxlint-disable-next-line typescript/no-deprecated -- the protocol still requires rootUri, and servers that predate workspaceFolders read only it
            rootUri: params.workspaceFolders?.[0]?.uri ?? null,
        };
        this.initializeParams = request;
        if (this.state !== 'running') {
            // The server starting after a crash is sent these params when it reopens, and
            // a second `initialize` would break its lifecycle. The reopen's answer serves,
            // unless the reopen began before the params were recorded.
            await this.requests.wait();
            if (this.initializeResult) {
                return this.initializeResult;
            }
        }
        return this.sendRequest('initialize', request);
    }

    async setTrace(params: lsp.SetTraceParams) {
        return this.sendRequest('$/setTrace', params);
    }

    async shutdown() {
        return this.sendRequest('shutdown', null);
    }

    async stop() {
        this.stopping = true;
        // run() may be waiting for an edit, with no server to send `exit` to.
        this.wake();
        // A server that is stopping may never answer `shutdown`, or may reject it
        // before `initialize`, so stop() sends it without waiting on the reply.
        this.shutdown().catch(() => {});
        await this.exit();
        // `stopped` silences the server's output from here on, and makes later
        // notifications no-ops.
        //
        // The Go runtime keeps running past this point. `exit` is a
        // notification, and the callbacks that drain the server's writes are
        // scheduled on timers that outlive this call. Whatever it prints from
        // here on describes a server nobody reads. Under Vitest that output
        // reaches the main process as an `onUserConsoleLog` message. One that
        // arrives after the worker has begun closing its rpc channel fails the
        // whole run with an EnvironmentTeardownError, while every test passes.
        //
        // Editor teardown can still send didChange or didClose after stop(), and
        // the disposed connection would reject them with no one to catch it.
        this.stopped = true;
        this.connection.dispose();
    }

    async exit() {
        if (this.stopped) {
            return;
        }
        // Sent whatever the state, so a server that is starting after a crash still ends.
        // Between a crash and the next server the connection is disposed and throws, and
        // there is no server to end.
        try {
            await this.connection.sendNotification('exit', null);
        } catch {}
    }

    //
    // Document Synchronization methods
    //

    textDocumentDidOpen(params: lsp.DidOpenTextDocumentParams) {
        this.documents.set(params.textDocument.uri, params.textDocument);
        return this.sendDocumentNotification('textDocument/didOpen', params);
    }

    textDocumentDidChange(params: lsp.DidChangeTextDocumentParams) {
        const doc = this.documents.get(params.textDocument.uri);
        const last = params.contentChanges.at(-1);
        // The server takes whole-document changes only, so the last change holds the text.
        if (doc && last && !('range' in last)) {
            this.documents.set(doc.uri, {
                ...doc,
                version: params.textDocument.version,
                text: last.text,
            });
        }
        return this.sendDocumentNotification('textDocument/didChange', params);
    }

    textDocumentWillSave(params: lsp.WillSaveTextDocumentParams) {
        return this.sendNotification('textDocument/willSave', params);
    }

    // textDocumentWillSaveWaitUntil

    textDocumentDidSave(params: lsp.DidSaveTextDocumentParams) {
        return this.sendNotification('textDocument/didSave', params);
    }

    textDocumentDidClose(params: lsp.DidCloseTextDocumentParams) {
        this.documents.delete(params.textDocument.uri);
        return this.sendDocumentNotification('textDocument/didClose', params);
    }

    workspaceDidChangeWatchedFiles(params: lsp.DidChangeWatchedFilesParams) {
        return this.sendNotification('workspace/didChangeWatchedFiles', params);
    }

    //
    // Language Features
    //

    textDocumentDeclaration(
        params: lsp.DeclarationParams,
    ): Promise<lsp.Location | lsp.Location[] | lsp.LocationLink[] | null> {
        return this.sendRequest('textDocument/declaration', params);
    }

    textDocumentDefinition(
        params: lsp.DefinitionParams,
    ): Promise<lsp.Location | lsp.Location[] | lsp.LocationLink[] | null> {
        return this.sendRequest('textDocument/definition', params);
    }

    textDocumentCodeAction(
        params: lsp.CodeActionParams,
    ): Promise<lsp.Command[] | lsp.CodeAction[]> {
        return this.sendRequest('textDocument/codeAction', params);
    }

    textDocumentHover(params: lsp.HoverParams): Promise<lsp.Hover | null> {
        return this.sendRequest('textDocument/hover', params);
    }

    textDocumentSemanticTokensFull(
        params: lsp.SemanticTokensParams,
    ): Promise<lsp.SemanticTokens | null> {
        return this.sendRequest('textDocument/semanticTokens/full', params);
    }

    textDocumentCompletion(
        params: lsp.CompletionParams,
    ): Promise<lsp.CompletionList | lsp.CompletionItem[] | null> {
        return this.sendRequest('textDocument/completion', params);
    }

    completionItemResolve(
        params: lsp.CompletionItem,
    ): Promise<lsp.CompletionItem> {
        return this.sendRequest('completionItem/resolve', params);
    }

    textDocumentSignatureHelp(
        params: lsp.SignatureHelpParams,
    ): Promise<lsp.SignatureHelp | null> {
        return this.sendRequest('textDocument/signatureHelp', params);
    }

    // Go to type definition

    // Go to implementation

    workspaceExecuteCommand(
        params: lsp.ExecuteCommandParams,
    ): Promise<lsp.LSPAny> {
        return this.sendRequest('workspace/executeCommand', params);
    }

    /**
     * Sends a notification, or does nothing once stop() has run, since the
     * connection it would write to is gone. Callers don't wait on notifications.
     */
    private sendNotification(method: string, params: unknown): Promise<void> {
        if (this.stopped) {
            return Promise.resolve();
        }
        switch (this.state) {
            case 'running':
                return this.connection.sendNotification(method, params);
            case 'restarting':
                this.queuedNotifications.push([method, params]);
                return Promise.resolve();
            case 'waiting-for-edit':
                // The next server rescans the workspace when it initializes, so it learns
                // about the files this notification describes.
                return Promise.resolve();
        }
    }

    /**
     * Sends a notification that opens, changes or closes a document, after the caller has
     * recorded the change in `documents`. While a server is starting after a crash, the
     * notification is dropped, since the server opens every document from `documents`
     * once it is up. An edit while the client waits for one starts the new server.
     */
    private sendDocumentNotification(
        method: string,
        params: unknown,
    ): Promise<void> {
        switch (this.state) {
            case 'running':
                return this.sendNotification(method, params);
            case 'waiting-for-edit':
                this.wake();
                return Promise.resolve();
            case 'restarting':
                return Promise.resolve();
        }
    }

    private async sendRequest(method: string, params: unknown): Promise<any> {
        if (this.state === 'waiting-for-edit') {
            throw new Error(SERVER_DOWN_MESSAGE);
        }
        await this.requests.wait();
        return this.connection.sendRequest(method, params);
    }

    onTextDocumentPublishDiagnostics(
        callback: (params: lsp.PublishDiagnosticsParams) => void,
    ) {
        this.on('textDocument/publishDiagnostics', callback);
    }

    /** Calls callback with each panic the server recovers from. */
    onServerPanic(callback: (report: PanicReport) => void) {
        this.on('escalier/panicReport', callback);
    }

    onWindowLogMessage(callback: (params: lsp.LogMessageParams) => void) {
        this.on('window/logMessage', callback);
    }

    private on(method: string, callback: (params: any) => void) {
        if (this.notifications.listenerCount(method) === 0) {
            this.connection.onNotification(method, (params) =>
                this.notifications.emit(method, params),
            );
        }
        this.notifications.on(method, callback);
    }
}
