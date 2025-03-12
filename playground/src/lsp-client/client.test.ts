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
    client = new Client(buffer);
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
          "declarationProvider": true,
          "definitionProvider": true,
          "textDocumentSync": 1,
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

    expect(promise).rejects.toMatchInlineSnapshot(`
  {
    "code": -32601,
    "message": "method not supported: foo/bar",
  }
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

    client.textDocumentDidChange({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    });

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
