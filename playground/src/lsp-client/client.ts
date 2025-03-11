import EventEmitter from 'eventemitter3';
import type * as lsp from 'vscode-languageserver-protocol';

import './wasm_exec'; // run for side-effects

import { type AsyncResult, Result } from './result';

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
    private resolvers: Map<number, (value: Result<any, Error>) => void>;
    private requestID: number;
    private wasmBuf: ArrayBuffer;

    constructor(wasmBuf: ArrayBuffer) {
        this.stdin = new SimpleStream();
        this.stdout = new SimpleStream();
        this.emitter = new EventEmitter();
        this.resolvers = new Map();
        this.requestID = 0;
        this.wasmBuf = wasmBuf;

        this.stdout.on('data', (chunk) => {
            const message = decoder.decode(chunk);
            const headerRegex = /Content-Length: (\d+)/;
            const payload = message.replace(headerRegex, '').trim();
            const object = JSON.parse(payload);

            // TODO: validate the the object being returned is a valid RPC JSON response
            if (object.id != null) {
                // Handle response to a client request
                const resolve = this.resolvers.get(object.id);
                if (resolve) {
                    if (object.error) {
                        resolve(Result.Err(object.error));
                    }
                    if ('result' in object) {
                        resolve(Result.Ok(object.result));
                    }
                }
            } else {
                // Handle server initiated message
                this.emitter.emit(object.method, object.params);
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
                    console.error('writeSync:', value);
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
                    console.log('[Escalier LSP] -', decoder.decode(buffer));
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
            // fstat(fd, callback) {callback(enosys())},
            // fsync(fd, callback) {callback(null)},
            // ftruncate(fd, length, callback) {callback(enosys())},
            // lchown(path, uid, gid, callback) {callback(enosys())},
            // link(path, link, callback) {callback(enosys())},
            // lstat(path, callback) {callback(enosys())},
            // mkdir(path, perm, callback) {callback(enosys())},
            // open(path, flags, mode, callback) {callback(enosys())},
            // read(fd, buffer, offset, length, position, callback) { callback(enosys()); },
            read: (
                fd: number,
                buffer: Uint8Array,
                _offset: number,
                _length: number,
                _position: number | bigint | null,
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
                    console.error(
                        'Attempted to read from unknown file descriptor:',
                        fd,
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

    async stop() {
        await this.sendRequest('shutdown', null);
        await this.sendRequest('exit', null);
    }

    async sendRequest(
        method: 'initialize',
        params: lsp.InitializeParams,
    ): AsyncResult<lsp.InitializeResult, Error>;
    async sendRequest(
        method: '$/setTrace',
        params: lsp.SetTraceParams,
    ): AsyncResult<lsp.InitializeResult, Error>;
    async sendRequest(
        method: 'shutdown',
        params: null,
    ): AsyncResult<void, Error>;
    async sendRequest(method: 'exit', params: null): AsyncResult<void, Error>;
    async sendRequest(
        method: 'textDocument/didChange',
        params: lsp.DidChangeTextDocumentParams,
    ): AsyncResult<void, Error>;
    async sendRequest(method: string, params: any): AsyncResult<any, Error> {
        const id = this.requestID++;
        const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
        const message = `Content-Length: ${payload.length}\r\n\r\n${payload}`;
        this.stdin.write(encoder.encode(message));

        // Some methods don't have a response so we don't need to wait for them
        if (method === 'textDocument/didChange') {
            return Promise.resolve(Result.Ok(null));
        }

        return new Promise((resolve) => {
            this.resolvers.set(id, resolve);
        });
    }

    on(
        method: 'textDocument/publishDiagnostics',
        callback: (params: lsp.PublishDiagnosticsParams) => void,
    ): void;
    on(
        method: 'window/logMessage',
        callback: (params: lsp.LogMessageParams) => void,
    ): void;
    on(method: string, callback: (params: any) => void) {
        this.emitter.on(method, callback);
    }
}
