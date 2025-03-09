import * as fs from 'node:fs';
import { EventEmitter } from 'node:stream';
import type * as lsp from 'vscode-languageserver-protocol';

import '../wasm_exec'; // run for side-effects

import { Deferred } from './deferred';
import { type AsyncResult, Result } from './result';

const Go = globalThis.Go;

class SimpleStream {
    private chunks: Array<Buffer>;
    private emitter: EventEmitter;
    private name: string;

    constructor(name: string) {
        this.name = name;
        this.chunks = [];
        this.emitter = new EventEmitter();
    }

    on(event: string, listener: (chunk: Buffer) => void) {
        this.emitter.on(event, listener);
    }

    write(chunk: Buffer) {
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
    private deferreds: Map<number, Deferred<any, Error>>;
    private requestID: number;
    private wasmBuf: Buffer;

    constructor(wasmBuf: Buffer) {
        this.stdin = new SimpleStream('stdin');
        this.stdout = new SimpleStream('stdout');
        this.emitter = new EventEmitter();
        this.deferreds = new Map();
        this.requestID = 0;
        this.wasmBuf = wasmBuf;

        this.stdout.on('data', (chunk) => {
            const message = chunk.toString('utf-8');
            const headerRegex = /Content-Length: (\d+)/;
            const payload = message.replace(headerRegex, '').trim();
            const object = JSON.parse(payload);

            // TODO: validate the the object being returned is a valid RPC JSON response
            if (object.id != null) {
                const defferred = this.deferreds.get(object.id);
                if (defferred) {
                    if (object.error) {
                        defferred.resolve(Result.Err(object.error));
                    }
                    if ('result' in object) {
                        defferred.resolve(Result.Ok(object.result));
                    }
                }
            } else {
                this.emitter.emit(object.method, object.params);
            }
        });

        globalThis.fs = {
            ...fs,
            read: (
                fd: number,
                buffer: Uint8Array,
                offset: number,
                length: number,
                position: fs.ReadPosition | null,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    bytesRead: number,
                    buffer: Uint8Array<ArrayBufferLike>,
                ) => void,
            ) => {
                if (fd === 0) {
                    const srcBuf = this.stdin.read();
                    if (srcBuf) {
                        // TODO: handle the case where srcBuffer is larger than buffer
                        srcBuf.copy(buffer, offset, 0, length);
                        callback(null, srcBuf.length, srcBuf);
                        return;
                    }

                    setImmediate(() => {
                        const error = new Error();
                        // @ts-ignore
                        error.code = 'EAGAIN';
                        callback(error, 0, buffer);
                    });
                    return;
                }
                fs.read(
                    fd,
                    buffer,
                    offset,
                    length,
                    position,
                    (err, bytesRead, buffer) => {
                        callback(err, bytesRead, buffer);
                    },
                );
            },

            write: (
                fd: number,
                buffer: Uint8Array,
                offset: number,
                length: number,
                position: number | null | undefined,
                callback: (
                    err: NodeJS.ErrnoException | null,
                    written: number,
                    buffer: Uint8Array<ArrayBufferLike>,
                ) => void,
            ) => {
                // TODO: also handle stderr
                if (fd === 1) {
                    this.stdout.write(Buffer.from(buffer));
                    callback(null, length, buffer);
                    return;
                }
                return fs.write(fd, buffer, offset, length, position, callback);
            },
        };

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

        this.stdin.write(Buffer.from(message, 'utf-8'));

        // Some methods don't have a response so we don't need to wait for them
        if (method === 'textDocument/didChange') {
            return Promise.resolve(Result.Ok(null));
        }

        const deferred = new Deferred<any, Error>();
        this.deferreds.set(id, deferred);

        return deferred.promise;
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
