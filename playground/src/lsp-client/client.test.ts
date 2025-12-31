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

    expect(promise).rejects.toMatchInlineSnapshot(`
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
