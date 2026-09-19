import * as fs from 'node:fs';
import * as path from 'node:path';
import { expect, test, vi } from 'vitest';

import { Client } from './client';

// These exercise the client's Content-Length framing: whether it reassembles a
// response that arrives split across several stdout chunks. They build their
// own Client and never call run(), so no LSP server is involved.
//
// They live apart from client.test.ts because constructing a Client installs
// its filesystem shim on `globalThis.fs`. Doing that beside a running server
// would repoint that server's stdin at this file's streams, and it would then
// never read another message.
const buffer = fs.readFileSync(
    path.join(__dirname, '../../../bin/lsp-server.wasm'),
);

test('multi-chunk message handling', async () => {
    const encoder = new TextEncoder();

    // Create a mock FSAPI
    const mockFS = {
        fstat: vi.fn(),
        lstat: vi.fn(),
        open: vi.fn(),
        read: vi.fn(),
    };

    const cwd = process.cwd();
    const testClient = new Client(buffer, cwd, mockFS as any);

    // Don't call run() or initialize() to avoid interference from the real server

    // Create a large response that will be split across chunks
    const largeResult = {
        capabilities: {
            textDocumentSync: 1,
            hoverProvider: true,
            definitionProvider: true,
            // Add lots of data to make it large
            data: 'x'.repeat(5000),
        },
    };

    const responseObject = {
        jsonrpc: '2.0',
        id: 999,
        result: largeResult,
    };

    const payload = JSON.stringify(responseObject);
    const header = `Content-Length: ${payload.length}\r\n\r\n`;
    const fullMessage = header + payload;

    // Split the message into multiple chunks
    const chunkSize = 100;
    const chunks: Uint8Array[] = [];
    for (let i = 0; i < fullMessage.length; i += chunkSize) {
        const chunkStr = fullMessage.substring(i, i + chunkSize);
        chunks.push(encoder.encode(chunkStr));
    }

    console.log(`Split message into ${chunks.length} chunks`);

    // Set up a deferred for request ID 999
    // @ts-expect-error: deferreds is private
    const deferreds = testClient.deferreds;
    const deferred = {
        promise: null as any,
        resolve: null as any,
        reject: null as any,
    };
    deferred.promise = new Promise((resolve, reject) => {
        deferred.resolve = resolve;
        deferred.reject = reject;
    });
    deferreds.set(999, deferred);

    // Simulate receiving chunks through stdout
    // @ts-expect-error: stdout is private
    const stdout = testClient.stdout;

    // Send chunks one at a time
    for (const chunk of chunks) {
        stdout.write(chunk);
    }

    // Wait for the promise to resolve
    const result = await deferred.promise;

    expect(result).toEqual(largeResult);
});

test('multi-chunk message with exact boundary split', async () => {
    const encoder = new TextEncoder();

    // Create a mock FSAPI
    const mockFS = {
        fstat: vi.fn(),
        lstat: vi.fn(),
        open: vi.fn(),
        read: vi.fn(),
    };

    const cwd = process.cwd();
    const testClient = new Client(buffer, cwd, mockFS as any);

    const responseObject = {
        jsonrpc: '2.0',
        id: 888,
        result: { message: 'test response with specific length' },
    };

    const payload = JSON.stringify(responseObject);
    const header = `Content-Length: ${payload.length}\r\n\r\n`;

    // Split at the header/payload boundary
    const chunk1 = encoder.encode(header);
    const halfPayload = Math.floor(payload.length / 2);
    const chunk2 = encoder.encode(payload.substring(0, halfPayload));
    const chunk3 = encoder.encode(payload.substring(halfPayload));

    console.log('Sending message in 3 chunks: header, first half, second half');

    // Set up a deferred for request ID 888
    // @ts-expect-error: deferreds is private
    const deferreds = testClient.deferreds;
    const deferred = {
        promise: null as any,
        resolve: null as any,
        reject: null as any,
    };
    deferred.promise = new Promise((resolve, reject) => {
        deferred.resolve = resolve;
        deferred.reject = reject;
    });
    deferreds.set(888, deferred);

    // @ts-expect-error: stdout is private
    const stdout = testClient.stdout;

    stdout.write(chunk1);
    stdout.write(chunk2);
    stdout.write(chunk3);

    const result = await deferred.promise;

    expect(result).toEqual({ message: 'test response with specific length' });
});
