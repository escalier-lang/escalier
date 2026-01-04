import * as fs from 'node:fs';
import * as path from 'node:path';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { Client } from './client';

let client: Client;

const buffer = fs.readFileSync(
    path.join(__dirname, '../../../bin/lsp-server.wasm'),
);

beforeEach(() => {
    client = new Client(buffer, fs);
    client.run();
});

afterEach(() => {
    client.stop();
});

test('initialize', async () => {
    const initResult = await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    expect(initResult).toMatchInlineSnapshot(`
      {
        "capabilities": {
          "codeActionProvider": {
            "codeActionKinds": [
              "compile",
            ],
          },
          "declarationProvider": true,
          "definitionProvider": true,
          "executeCommandProvider": {
            "commands": [
              "compile",
            ],
          },
          "hoverProvider": true,
          "textDocumentSync": 1,
          "typeDefinitionProvider": true,
        },
        "serverInfo": {
          "name": "escalier",
          "version": "0.0.1",
        },
      }
    `);
});

test('foo/bar', async () => {
    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    // @ts-expect-error: sendRequest is private
    const promise = client.sendRequest('foo/bar', null);

    await expect(promise).rejects.toMatchInlineSnapshot(`
  {
    "code": -32601,
    "message": "method not supported: foo/bar",
  }
`);
});

test('textDocument/didOpen', async () => {
    let diagnostics: lsp.PublishDiagnosticsParams['diagnostics'] | null = null;

    client.onTextDocumentPublishDiagnostics((params) => {
        console.log('Received diagnostics');
        diagnostics = params.diagnostics;
    });

    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    client.textDocumentDidOpen({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
            languageId: 'escalier',
            text: 'console.log("Hello, world!")\nval x =\n',
        },
    });

    await vi.waitFor(
        () => {
            expect(diagnostics).not.toBeNull();
        },
        {
            timeout: 10000,
        },
    );

    expect(diagnostics).toMatchInlineSnapshot(`
      [
        {
          "message": "Expected an expression",
          "range": {
            "end": {
              "character": 0,
              "line": 2,
            },
            "start": {
              "character": 0,
              "line": 2,
            },
          },
          "severity": 1,
          "source": "escalier",
        },
        {
          "code": "ERR_CODE",
          "message": "Unimplemented: Infer expression type: *ast.EmptyExpr",
          "range": {
            "end": {
              "character": 0,
              "line": 2,
            },
            "start": {
              "character": 0,
              "line": 2,
            },
          },
          "severity": 1,
          "source": "escalier",
        },
      ]
    `);
});

test('textDocument/didChange', async () => {
    let diagnostics: lsp.PublishDiagnosticsParams['diagnostics'] | null = null;

    client.onTextDocumentPublishDiagnostics((params) => {
        console.log('Received diagnostics');
        diagnostics = params.diagnostics;
    });

    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    client.textDocumentDidOpen({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
            languageId: 'escalier',
            text: 'console.log("Hello, world!")\nval x = 5\n',
        },
    });

    client.textDocumentDidChange({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    });

    await vi.waitFor(
        () => {
            expect(diagnostics).not.toBeNull();
        },
        {
            timeout: 10000,
        },
    );

    expect(diagnostics).toMatchInlineSnapshot(`
      [
        {
          "message": "Expected an expression",
          "range": {
            "end": {
              "character": 0,
              "line": 2,
            },
            "start": {
              "character": 0,
              "line": 2,
            },
          },
          "severity": 1,
          "source": "escalier",
        },
        {
          "code": "ERR_CODE",
          "message": "Unimplemented: Infer expression type: *ast.EmptyExpr",
          "range": {
            "end": {
              "character": 0,
              "line": 2,
            },
            "start": {
              "character": 0,
              "line": 2,
            },
          },
          "severity": 1,
          "source": "escalier",
        },
      ]
    `);
});

test('textDocument/codeAction', async () => {
    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    const actions = await client.textDocumentCodeAction({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
        },
        range: {
            start: { line: 0, character: 0 },
            end: { line: 0, character: 1 },
        },
        context: {
            diagnostics: [],
        },
    });

    expect(actions).toMatchInlineSnapshot(`
  [
    {
      "command": {
        "command": "compile",
        "title": "Compile",
      },
      "kind": "compile",
      "title": "Compile",
    },
  ]
`);
});

// TODO: Re-enable once we can infer member expressions
test.skip('workspace/executeCommand', async () => {
    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    let diagnosticsPublished = false;

    client.onTextDocumentPublishDiagnostics((_params) => {
        diagnosticsPublished = true;
    });

    client.textDocumentDidOpen({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
            languageId: 'escalier',
            text: 'console.log("Hello, world!")\nval x = 5\n',
        },
    });

    await vi.waitFor(() => {
        expect(diagnosticsPublished).toBeTruthy();
    });

    const response = await client.workspaceExecuteCommand({
        command: 'compile',
        arguments: ['file:///home/user/project/foo.esc'],
    });

    expect(response).toMatchInlineSnapshot(`
  {
    "languageId": "javascript",
    "text": "console.log("Hello, world!");
  const x = 5;
  //# sourceMappingURL=./foo.esc.map
  ",
    "uri": "file:///home/user/project/foo.js",
    "version": 0,
  }
`);
});

test('multi-chunk message handling', async () => {
    const encoder = new TextEncoder();

    // Create a mock FSAPI
    const mockFS = {
        fstat: vi.fn(),
        lstat: vi.fn(),
        open: vi.fn(),
        read: vi.fn(),
    };

    const testClient = new Client(buffer, mockFS as any);

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

    const testClient = new Client(buffer, mockFS as any);

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
