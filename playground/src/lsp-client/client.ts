import type { Mode, OpenMode, PathLike, Stats } from 'node:fs';
import EventEmitter from 'eventemitter3';
import type * as lsp from 'vscode-languageserver-protocol';

import type { FSAPI } from '../fs/fs-api';

import { Deferred } from './deferred';

import './wasm_exec'; // run for side-effects

const Go = globalThis.Go;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

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
    private contentLength: number;
    private messageBuffer: string;

    constructor(wasmBuf: ArrayBuffer, fs: FSAPI) {
        this.stdin = new SimpleStream();
        this.stdout = new SimpleStream();
        this.emitter = new EventEmitter();
        this.deferreds = new Map();
        this.requestID = 0;
        this.wasmBuf = wasmBuf;
        this.errorBuffer = '';
        this.contentLength = 0;
        this.messageBuffer = '';

        this.stdout.on('data', (chunk) => {
            const message = decoder.decode(chunk);

            // If we don't have a content length yet, we're starting a new message
            if (this.contentLength === 0) {
                const headerRegex = /Content-Length: (\d+)\r?\n\r?\n/;
                const match = message.match(headerRegex);

                if (match) {
                    this.contentLength = Number.parseInt(match[1], 10);
                    // Extract payload after the header
                    const headerEndIndex = (match.index ?? 0) + match[0].length;
                    this.messageBuffer = message.substring(headerEndIndex);
                } else {
                    console.error('No Content-Length header found in message');
                    return;
                }
            } else {
                // Continuing to accumulate chunks for the current message
                this.messageBuffer += message;
            }

            // Check if we have received the complete message
            if (this.messageBuffer.length >= this.contentLength) {
                const payload = this.messageBuffer.substring(
                    0,
                    this.contentLength,
                );

                try {
                    const object = JSON.parse(payload);

                    // TODO: validate the the object being returned is a valid RPC JSON response
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
                        }
                    } else {
                        // Handle server initiated message
                        this.emitter.emit(object.method, object.params);
                    }
                } catch (e) {
                    console.log('Error parsing JSON:', e);
                }

                // Reset state for next message
                this.contentLength = 0;
                this.messageBuffer = '';
            }
        });

        // const enosys = () => {
        //     const err = new Error('not implemented');
        //     // @ts-ignore
        //     err.code = 'ENOSYS';
        //     return err;
        // };

        globalThis.fs = {
            constants: {
                O_WRONLY: -1,
                O_RDWR: -1,
                O_CREAT: -1,
                O_TRUNC: -1,
                O_APPEND: -1,
                O_EXCL: -1,
                O_DIRECTORY: -1,
            }, // unused
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
                    console.warn('[Escalier LSP] -', decoder.decode(buffer));
                    setTimeout(() => {
                        callback(null, length, buffer);
                    }, 0);
                } else {
                    console.error(
                        'Attempted to write to unknown file descriptor:',
                        fd,
                    );
                }
            },
            // chmod(path, mode, callback) {callback(enosys())},
            // chown(path, uid, gid, callback) {callback(enosys())},
            close(_fd: number, callback: (error: Error | null) => void) {
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
            // mkdir(path, perm, callback) {callback(enosys())},
            // open(path, flags, mode, callback) {callback(enosys()},
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
            // readdir(path, callback) {callback(enosys())},
            // readlink(path, callback) {callback(enosys())},
            // rename(from, to, callback) {callback(enosys())},
            // rmdir(path, callback) {callback(enosys())},
            // stat(path, callback) {callback(enosys())},
            // symlink(path, link, callback) {callback(enosys())},
            // truncate(path, length, callback) {callback(enosys())},
            // unlink(path, callback) {callback(enosys())},
            // utimes(path, atime, mtime, callback) {callback(enosys())},
        };

        // TODO: make this a proxy object so that we can trap other fs methods
        // globalThis.fs = {
        //     // ...fs,

        // };

        this.go = new Go();
    }

    async run() {
        const { instance } = await WebAssembly.instantiate(
            this.wasmBuf,
            this.go.importObject,
        );
        return this.go.run(instance);
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

    // Go to type definition

    // Go to implementation

    workspaceExecuteCommand(
        params: lsp.ExecuteCommandParams,
    ): Promise<lsp.LSPAny> {
        return this.sendRequest('workspace/executeCommand', params);
    }

    private fireAndForget(method: string, params: any) {
        const id = this.requestID++;
        const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
        const message = `Content-Length: ${payload.length}\r\n\r\n${payload}`;
        this.stdin.write(encoder.encode(message));
    }

    private async sendRequest(method: string, params: any): Promise<any> {
        const id = this.requestID++;
        const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
        const message = `Content-Length: ${payload.length}\r\n\r\n${payload}`;
        this.stdin.write(encoder.encode(message));

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
