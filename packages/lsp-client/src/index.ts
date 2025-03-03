import * as fs from "fs";
import * as path from "path";
import { EventEmitter } from "stream";

import './wasm_exec_prelude'; // run for side-effects
import "./wasm_exec"; // run for side-effects

import * as lsp from 'vscode-languageserver-protocol'

const Go = globalThis.Go;

class SimpleStream {
    chunks: Array<Buffer>;
    emitter: EventEmitter;

    constructor() {
        this.chunks = [];
        this.emitter = new EventEmitter();
    }

    emit(event: string, chunk: Buffer) {
        this.chunks.push(chunk);
        this.emitter.emit(event, chunk);
    }

    on(event: string, listener: (chunk: Buffer) => void) {
        this.emitter.on(event, listener);
    }

    read() {
        return this.chunks.shift();
    }
}

const stdin = new SimpleStream();
const stdout = new SimpleStream();

const fsWrapper = {
    ...fs,
    read: (
        fd: number,
        buffer: Uint8Array,
        offset: number,
        length: number,
        position: number | null | undefined,
        callback: (err: NodeJS.ErrnoException | null, bytesRead: number, buffer: Uint8Array<ArrayBufferLike>) => void,
    ) => {
        if (fd === 0) {
            const srcBuffer: Buffer = stdin.read();
            if (srcBuffer) {
                // TODO: handle the case where srcBuffer is larger than buffer
                srcBuffer.copy(buffer, offset, 0, length);
                callback(null, srcBuffer.length, srcBuffer);
                return;
            }
        }
        return fs.read(fd, buffer, offset, length, position, callback);
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
            stdout.emit("data", Buffer.from(buffer));
            callback(null, length, buffer);
            return;
        }
        return fs.write(fd, buffer, offset, length, position, callback);
    },
};

const go = new Go(fsWrapper);
const buffer = fs.readFileSync(path.join(__dirname, '../../../bin/lsp-server.wasm'));

let requestID = 0;

const serverEmitter = new EventEmitter();
const resolvers = new Map<number, (value: any) => void>();

// Receives messages from the server via STDOUT
stdout.on('data', (chunk) => {
    const message = chunk.toString("utf-8");
    const headerRegex = /Content-Length: (\d+)/;
    const payload = message.replace(headerRegex, "").trim();
    const object = JSON.parse(payload);

    // TODO: validate the the object being returned is a valid RPC JSON response
    if (object.id != null) {
        const resolver = resolvers.get(object.id);
        if (resolver) {
            resolver(object.result);
        }
    } else {
        serverEmitter.emit(object.method, object.params);
    }
});

async function sendRequest(method: "initialize", params: lsp.InitializeParams): Promise<lsp.InitializeResult>;
async function sendRequest(method: "shutdown", params: null): Promise<void>;
async function sendRequest(method: "exit", params: null): Promise<void>;
async function sendRequest(method: "textDocument/didChange", params: lsp.DidChangeTextDocumentParams): Promise<void>;
async function sendRequest(method: string, params: any) {
    const initPaylod = JSON.stringify({
        jsonrpc: "2.0",
        id: requestID,
        method,
        params,
    });
    const initMessage = `Content-Length: ${initPaylod.length}\r\n\r\n${initPaylod}`;

    stdin.emit("data", Buffer.from(initMessage, 'utf-8'));
    const promise1 = new Promise((resolve) => {
        resolvers.set(requestID, resolve);
    });
    const response = await promise1;
    requestID += 1;
    return response;
}

async function main() {
    const result = await WebAssembly.instantiate(buffer, go.importObject);
    go.run(result.instance);

    console.log("running...");

    serverEmitter.on("textDocument/publishDiagnostics", (params: lsp.PublishDiagnosticsParams) => {
        console.log("Received diagnostics");
        console.log(JSON.stringify(params, null, 4));
    });

    const initParams: lsp.InitializeParams = {
        processId: process.pid,
        rootUri: "file:///home/user/project",
        capabilities: {},
    };
    const initResponse = await sendRequest("initialize", initParams);
    console.log("Initial response");
    console.log(JSON.stringify(initResponse, null, 4));

    const didChangeParams: lsp.DidChangeTextDocumentParams = {
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [
            { text: 'console.log("Hello, world!")\nval x =\n' }
        ]
    };
    await sendRequest("textDocument/didChange", didChangeParams);

    await sendRequest("shutdown", null);
    await sendRequest("exit", null);
}

main();
