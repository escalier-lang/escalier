import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { Client } from './client';

let client: Client;

beforeEach(() => {
    client = new Client();
});

afterEach(() => {
    client.dispose();
});

test('initialize', async () => {
    const initResult = await client.initialize({
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
});

test('textDocument/didOpen', async () => {
    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    client.didChange({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    });

    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    const result = await client.didOpen({
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            languageId: 'escalier',
            version: 1,
            text: 'console.log("Hello, world!")\n',
        },
    });

    expect(result).toMatchInlineSnapshot(`
  Err {
    "error": [Error: method not supported: textDocument/didOpen],
  }
`);
});

test('textDocument/didChange', async () => {
    let diagnostics: lsp.PublishDiagnosticsParams['diagnostics'] | null = null;

    client.onPublishDiagnostics((params) => {
        console.log('Received diagnostics');
        diagnostics = params.diagnostics;
    });

    await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });

    await client.didChange({
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
