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

stdout.on('data', (chunk) => {
    console.log("stdout.on('data') - chunk =", chunk.toString());
});

const fsWrapper = {
    ...fs,
    read: (
        fd: number,
        buffer: Uint8Array,
        offset: number,
        length: number,
        position: number | null | undefined,
        callback,
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
        console.log("fs.write - fd =", fd);
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

async function sleep(duration: number) {
    return new Promise((resolve) => {
        setTimeout(resolve, duration);
    });
}

WebAssembly.instantiate(buffer, go.importObject).then(async (result) => {
    go.run(result.instance);

    console.log("running...");

    let requestID = 0;
    const initParams: lsp.InitializeParams = {
        processId: process.pid,
        rootUri: "file:///home/user/project",
        capabilities: {},
    };
    const initPaylod = JSON.stringify({
        jsonrpc: "2.0",
        id: requestID++,
        method: "initialize",
        params: initParams,
    });
    const initMessage = `Content-Length: ${initPaylod.length}\r\n\r\n${initPaylod}`;
    stdin.emit("data", Buffer.from(initMessage, 'utf-8'));

    await sleep(500);

    const initResponse = stdout.read();
    console.log("initialize response =", initResponse.toString());

    const didChangeParams: lsp.DidChangeTextDocumentParams = {
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [
            { text: 'console.log("Hello, world!")\nval x =\n' }
        ]
    };
    const didChangePayload = JSON.stringify({
        jsonrpc: "2.0",
        id: requestID++,
        method: "textDocument/didChange",
        params: didChangeParams,
    });
    console.log("didChangePayload =", didChangePayload);
    const didChangeMessage = `Content-Length: ${didChangePayload.length}\r\n\r\n${didChangePayload}`;

    stdin.emit("data", Buffer.from(didChangeMessage, 'utf-8'));
   
    await sleep(1000);
    
    console.log("---- RESPONSE ----");
    while (true) {
        const response = stdout.read();
        if (!response) {
            break;
        }
        console.log(response.toString());
    }
    // const didChangeResponse = stdout.read();
    // console.log("didChangeResponse response =", didChangeResponse);
    // console.log("didChangeResponse response =", didChangeResponse.toString());

    // Each request and respone have matching IDs
    // We'll need a way to wait for a response for a given request ID so that we
    // can have a nice API for sending requests and getting responses.

    // const endpoint = new JSONRPCEndpoint(stdin, stdout);

    // const value = await endpoint.send('initialize', {
    //     processId: process.pid,
    //     rootUri: 'file:///home/user/project',
    //     capabilities: {},
    // });

    // console.log("initialize result =", value);
});
