import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';
import EventEmitter from 'eventemitter3';
import type * as lsp from 'vscode-languageserver-protocol';

import type { FSAPI } from '../fs/fs-api';

import { Deferred } from './deferred';

import './wasm_exec'; // run for side-effects

const Go = globalThis.Go;

// The Client whose shims `globalThis` currently carries, or undefined when no
// runtime is running. See run().
let runningClient: Client | undefined;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/** The bytes of `\r\n\r\n`, which ends an LSP message's header. */
const headerEnd = encoder.encode('\r\n\r\n');

/**
 * Returns payload as an LSP message: a `Content-Length` header that counts the
 * payload's UTF-8 bytes, followed by the payload.
 */
export function frameMessage(payload: string): Uint8Array {
    const body = encoder.encode(payload);
    const header = encoder.encode(`Content-Length: ${body.byteLength}\r\n\r\n`);
    const message = new Uint8Array(header.byteLength + body.byteLength);
    message.set(header);
    message.set(body, header.byteLength);
    return message;
}

/** Returns the index where needle first occurs in haystack, or -1. */
function indexOfBytes(haystack: Uint8Array, needle: Uint8Array): number {
    outer: for (let i = 0; i + needle.length <= haystack.length; i++) {
        for (let j = 0; j < needle.length; j++) {
            if (haystack[i + j] !== needle[j]) {
                continue outer;
            }
        }
        return i;
    }
    return -1;
}

/**
 * Splits buffer into the payloads of the complete LSP messages it starts with, and
 * returns them with the bytes left over, which begin an incomplete message. A
 * payload is decoded only once all of its bytes have arrived, so a character split
 * across two chunks decodes whole. A header without a `Content-Length` is reported
 * in malformed and skipped.
 */
export function takeMessages(buffer: Uint8Array): {
    payloads: string[];
    malformed: string[];
    rest: Uint8Array;
} {
    const payloads: string[] = [];
    const malformed: string[] = [];
    let start = 0;
    for (;;) {
        const end = indexOfBytes(buffer.subarray(start), headerEnd);
        if (end === -1) {
            break;
        }
        const header = decoder.decode(buffer.subarray(start, start + end));
        const bodyStart = start + end + headerEnd.length;
        const match = header.match(/Content-Length: (\d+)/);
        if (!match) {
            malformed.push(header);
            start = bodyStart;
            continue;
        }
        const bodyEnd = bodyStart + Number.parseInt(match[1], 10);
        if (bodyEnd > buffer.length) {
            break;
        }
        payloads.push(decoder.decode(buffer.subarray(bodyStart, bodyEnd)));
        start = bodyEnd;
    }
    return { payloads, malformed, rest: buffer.slice(start) };
}

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

    on(event: string, listener: (chunk: Uint8Array) => void) {
        this.emitter.on(event, listener);
    }

    write(chunk: Uint8Array) {
        this.chunks.push(chunk);
        this.emitter.emit('data', chunk);
    }

    read() {
        return this.chunks.shift();
    }
}

export class Client {
    private go: any;
    private stdin: SimpleStream;
    private stdout: SimpleStream;
    private emitter: EventEmitter;
    private deferreds: Map<number, Deferred>;
    private requestID: number;
    private wasmBuf: ArrayBuffer;
    private errorBuffer: string;
    /** Bytes from stdout that begin a message whose bytes have not all arrived. */
    private pending: Uint8Array;
    /** Set by `stop()`. Silences the server's output from then on. */
    private stopped = false;

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
        this.stdin = new SimpleStream();
        this.stdout = new SimpleStream();
        this.emitter = new EventEmitter();
        this.deferreds = new Map();
        this.requestID = 0;
        this.wasmBuf = wasmBuf;
        this.errorBuffer = '';
        this.pending = new Uint8Array(0);

        this.stdout.on('data', (chunk) => {
            const buffer = new Uint8Array(this.pending.length + chunk.length);
            buffer.set(this.pending);
            buffer.set(chunk, this.pending.length);
            const { payloads, malformed, rest } = takeMessages(buffer);
            this.pending = rest;
            for (const header of malformed) {
                console.error(
                    'No Content-Length header found in message',
                    header,
                );
            }
            for (const payload of payloads) {
                this.dispatch(payload);
            }
        });

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
                    if (!this.stopped) {
                        console.warn(
                            '[Escalier LSP] -',
                            decoder.decode(buffer),
                        );
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
                    const srcBuf = this.stdin.read();
                    if (srcBuf) {
                        // TODO: handle the case where srcBuffer is larger than buffer
                        buffer.set(srcBuf, 0);
                        callback(null, srcBuf.length, srcBuf);
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

        this.go = new Go();
        // Forward caller-supplied env vars to the Go runtime so
        // `os.Getenv` sees them. The playground passes
        // ESCALIER_BUILTINS_DIR to point the interop loader at the
        // virtual-filesystem path where the compiler's
        // `internal/interop/data` directory has been mirrored.
        this.go.env = { ...(this.go.env ?? {}), ...env };
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
        runningClient = this;

        globalThis.fs = this.fsShim as typeof globalThis.fs;
        globalThis.process = this.processShim as typeof globalThis.process;

        try {
            const { instance } = await WebAssembly.instantiate(
                this.wasmBuf,
                this.go.importObject,
            );
            return await this.go.run(instance);
        } finally {
            runningClient = undefined;
        }
    }

    //
    // Lifecycle methods
    //

    async initialize(params: lsp.InitializeParams) {
        return this.sendRequest('initialize', params);
    }

    async setTrace(params: lsp.SetTraceParams) {
        return this.sendRequest('$/setTrace', params);
    }

    async shutdown() {
        return this.fireAndForget('shutdown', null);
    }

    async stop() {
        await this.shutdown();
        await this.exit();
        // The Go runtime keeps running past this point. `exit` is a
        // notification, and the callbacks that drain the server's writes are
        // scheduled on timers that outlive this call. Whatever it prints from
        // here on describes a server nobody reads.
        //
        // Under Vitest that output reaches the main process as an
        // `onUserConsoleLog` message. One that arrives after the worker has
        // begun closing its rpc channel fails the whole run with an
        // EnvironmentTeardownError, while every test passes.
        this.stopped = true;
    }

    async exit() {
        return this.fireAndForget('exit', null);
    }

    //
    // Document Synchronization methods
    //

    textDocumentDidOpen(params: lsp.DidOpenTextDocumentParams) {
        return this.fireAndForget('textDocument/didOpen', params);
    }

    textDocumentDidChange(params: lsp.DidChangeTextDocumentParams) {
        return this.fireAndForget('textDocument/didChange', params);
    }

    textDocumentWillSave(params: lsp.WillSaveTextDocumentParams) {
        return this.fireAndForget('textDocument/willSave', params);
    }

    // textDocumentWillSaveWaitUntil

    textDocumentDidSave(params: lsp.DidSaveTextDocumentParams) {
        return this.fireAndForget('textDocument/didSave', params);
    }

    textDocumentDidClose(params: lsp.DidCloseTextDocumentParams) {
        return this.fireAndForget('textDocument/didClose', params);
    }

    workspaceDidChangeWatchedFiles(params: lsp.DidChangeWatchedFilesParams) {
        return this.fireAndForget('workspace/didChangeWatchedFiles', params);
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

    // Go to type definition

    // Go to implementation

    workspaceExecuteCommand(
        params: lsp.ExecuteCommandParams,
    ): Promise<lsp.LSPAny> {
        return this.sendRequest('workspace/executeCommand', params);
    }

    /** Resolves the request a response answers, or emits a server notification. */
    private dispatch(payload: string) {
        try {
            const object = JSON.parse(payload);

            // TODO: validate that the object being returned is a valid RPC JSON response
            if (object.id != null) {
                // Handle response to a client request
                const deferred = this.deferreds.get(object.id);
                if (deferred) {
                    if (object.error) {
                        deferred.reject(object.error);
                    }
                    if ('result' in object) {
                        deferred.resolve(object.result);
                    }
                    // After resolving/rejecting the promise, remove it from the map
                    // to prevent memory leaks.
                    this.deferreds.delete(object.id);
                }
            } else {
                // Handle server initiated message
                this.emitter.emit(object.method, object.params);
            }
        } catch (e) {
            console.log('Error parsing JSON:', e);
        }
    }

    private fireAndForget(method: string, params: any) {
        const id = this.requestID++;
        const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
        this.stdin.write(frameMessage(payload));
    }

    private async sendRequest(method: string, params: any): Promise<any> {
        const id = this.requestID++;
        const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
        this.stdin.write(frameMessage(payload));

        const deferred = new Deferred();
        this.deferreds.set(id, deferred);

        return deferred.promise;
    }

    onTextDocumentPublishDiagnostics(
        callback: (params: lsp.PublishDiagnosticsParams) => void,
    ) {
        this.on('textDocument/publishDiagnostics', callback);
    }

    onWindowLogMessage(callback: (params: lsp.LogMessageParams) => void) {
        this.on('window/logMessage', callback);
    }

    private on(method: string, callback: (params: any) => void) {
        this.emitter.on(method, callback);
    }
}
