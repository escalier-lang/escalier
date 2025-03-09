import * as fs from 'node:fs';
import * as path from 'node:path';
import { expect, test, vi, beforeEach, afterEach } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { Client } from './client';

let client: Client;

const buffer = fs.readFileSync(
    path.join(__dirname, '../../../bin/lsp-server.wasm'),
);

beforeEach(() => {
    client = new Client(buffer);
    client.run();
});

afterEach(() => {
    client.stop();
});

test('initialize', async () => {
    const initResult = await client.sendRequest('initialize', {
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });
    expect(initResult).toMatchInlineSnapshot(`
      Ok {
        "value": {
          "capabilities": {
            "textDocumentSync": 1,
          },
          "serverInfo": {
            "name": "escalier",
            "version": "0.0.1",
          },
        },
      }
    `);
    client.stop();
});

test('textDocument/didOpen', async () => {
    await client.sendRequest('initialize', {
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    // @ts-expect-error: method not supported
    const result = await client.sendRequest('textDocument/didOpen', {
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            languageId: 'escalier',
            version: 1,
            text: 'console.log("Hello, world!")\n',
        },
    });

    expect(result).toMatchInlineSnapshot(`
  Err {
    "error": {
      "code": -32601,
      "message": "method not supported: textDocument/didOpen",
    },
  }
`);
});

test('textDocument/didChange', async () => {
    let diagnostics: lsp.PublishDiagnosticsParams['diagnostics'] | null = null;

    client.on('textDocument/publishDiagnostics', (params) => {
        console.log('Received diagnostics');
        diagnostics = params.diagnostics;
    });

    await client.sendRequest('initialize', {
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    const didChangeParams: lsp.DidChangeTextDocumentParams = {
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    };
    await client.sendRequest('textDocument/didChange', didChangeParams);

    await vi.waitFor(() => {
        expect(diagnostics).not.toBeNull();
    });

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
      ]
    `);
});
