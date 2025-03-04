import * as fs from "fs";
import { EventEmitter } from "stream";
import * as lsp from 'vscode-languageserver-protocol'

import './wasm_exec_prelude'; // run for side-effects
import "./wasm_exec"; // run for side-effects

import { Result, type AsyncResult } from './result'

declare global {
    var Go: any;
}

const Go = globalThis.Go;

class SimpleStream {
    private chunks: Array<Buffer>;
    private emitter: EventEmitter;

    constructor() {
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

export class WasmClient {
    private go: any;
    private stdin: SimpleStream;
    private stdout: SimpleStream;
    private emitter: EventEmitter;
    private resolvers: Map<number, (value: Result<any, Error>) => void>;
    private requestID: number;
    private wasmBuf: Buffer;
    
    constructor(wasmBuf: Buffer) {
        this.stdin = new SimpleStream();
        this.stdout = new SimpleStream();
        this.emitter = new EventEmitter();
        this.resolvers = new Map();
        this.requestID = 0;
        this.wasmBuf = wasmBuf;

        this.stdout.on('data', (chunk) => {
            const message = chunk.toString("utf-8");
            const headerRegex = /Content-Length: (\d+)/;
            const payload = message.replace(headerRegex, "").trim();
            const object = JSON.parse(payload);
        
            // TODO: validate the the object being returned is a valid RPC JSON response
            if (object.id != null) {
                const resolver = this.resolvers.get(object.id);
                if (resolver) {
                    resolver(object.result);
                }
            } else {
                this.emitter.emit(object.method, object.params);
            }
        });

        this.go = new Go({
            ...fs,
            read: (
                fd: number,
                buffer: Uint8Array,
                offset: number,
                length: number,
                position: fs.ReadPosition | null,
                callback: (err: NodeJS.ErrnoException | null, bytesRead: number, buffer: Uint8Array<ArrayBufferLike>) => void,
            ) => {
                if (fd === 0) {
                    const srcBuf = this.stdin.read();
                    if (srcBuf) {
                        // TODO: handle the case where srcBuffer is larger than buffer
                        srcBuf.copy(buffer, offset, 0, length);
                        callback(null, srcBuf.length, srcBuf);
                        return;
                    }
                }
                fs.read(fd, buffer, offset, length, position, (err, bytesRead, buffer) => {
                    callback(err, bytesRead, buffer);
                });
            },
        
            write: (
                fd: number,
                buffer: Uint8Array,
                offset: number,
                length: number,
                position: number | null | undefined,
                callback: (err: NodeJS.ErrnoException | null, written: number, buffer: Uint8Array<ArrayBufferLike>) => void,
            ) => {
                // TODO: also handle stderr
                if (fd === 1) {
                    this.stdout.write(Buffer.from(buffer));
                    callback(null, length, buffer);
                    return;
                }
                return fs.write(fd, buffer, offset, length, position, callback);
            },
        });
    }

    async run() {
        const {instance} = await WebAssembly.instantiate(this.wasmBuf, this.go.importObject);
        return this.go.run(instance);
    }

    async stop() {
        await this.sendRequest("shutdown", null);
        await this.sendRequest("exit", null);
    }

    async sendRequest(method: "initialize", params: lsp.InitializeParams): AsyncResult<lsp.InitializeResult, Error>;
    async sendRequest(method: "$/setTrace", params: lsp.SetTraceParams): AsyncResult<lsp.InitializeResult, Error>;
    async sendRequest(method: "shutdown", params: null): AsyncResult<void, Error>;
    async sendRequest(method: "exit", params: null): AsyncResult<void, Error>;
    async sendRequest(method: "textDocument/didChange", params: lsp.DidChangeTextDocumentParams): AsyncResult<void, Error>;
    async sendRequest(method: string, params: any): AsyncResult<any, Error> {
        const id = this.requestID++;
        const payload = JSON.stringify({jsonrpc: "2.0", id, method, params});
        const message = `Content-Length: ${payload.length}\r\n\r\n${payload}`;

        this.stdin.write(Buffer.from(message, 'utf-8'));
        return new Promise((resolve) => {
            this.resolvers.set(id, resolve);
        });
    }

    on(method: "textDocument/publishDiagnostics", callback: (params: lsp.PublishDiagnosticsParams) => void): void;
    on(method: "window/logMessage", callback: (params: lsp.LogMessageParams) => void): void;
    on(method: string, callback: (params: any) => void) {
        this.emitter.on(method, callback);
    }
}
