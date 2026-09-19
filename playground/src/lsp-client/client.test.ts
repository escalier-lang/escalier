import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { Client } from './client';

let client: Client;
let runPromise: Promise<unknown>;
let tmpDir: string;
let rootUri: string;

const buffer = fs.readFileSync(
    path.join(__dirname, '../../../bin/lsp-server.wasm'),
);

const fileUri = () => `${rootUri}/bin/foo.esc`;
const filePath = () => path.join(tmpDir, 'bin', 'foo.esc');

beforeEach(() => {
    // Create a temp workspace with bin/ so the LSP server can discover files.
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'escalier-test-'));
    fs.mkdirSync(path.join(tmpDir, 'bin'), { recursive: true });
    rootUri = `file://${tmpDir}`;

    const cwd = process.cwd();
    client = new Client(buffer, cwd, fs, {
        // The WASM Go runtime needs ESCALIER_BUILTINS_DIR to find the
        // interop override directory; in production this is set by
        // main.tsx, but here we point directly at the on-disk source.
        ESCALIER_BUILTINS_DIR: path.join(
            __dirname,
            '../../../internal/interop/data',
        ),
    });
    runPromise = client.run();
});

// A server that never exits would park afterEach until Vitest's own hook
// timeout, which reports no cause. Failing first, and sooner, names it.
async function awaitServerExit() {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const expired = new Promise<never>((_resolve, reject) => {
        timer = setTimeout(
            () => reject(new Error('the LSP server did not exit')),
            5_000,
        );
    });
    try {
        await Promise.race([runPromise, expired]);
    } finally {
        clearTimeout(timer);
    }
}

afterEach(async () => {
    await client.stop();
    // `stop()` sends `exit`, which returns the server from its main and
    // resolves the run promise. Awaiting it puts the Go runtime's shutdown
    // inside the test that started it. Without this the runtime outlives the
    // file, and Vitest fails the run when one of its console messages is still
    // in flight as the worker closes its rpc channel.
    await awaitServerExit();
    fs.rmSync(tmpDir, { recursive: true, force: true });
});

test('initialize', async () => {
    const initResult = await client.initialize({
        processId: process.pid,
        rootUri,
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
          "completionProvider": {
            "resolveProvider": true,
            "triggerCharacters": [
              ".",
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
          "renameProvider": true,
          "textDocumentSync": 1,
          "typeDefinitionProvider": true,
          "workspace": {
            "fileOperations": {
              "didCreate": {
                "filters": [
                  {
                    "pattern": {
                      "glob": "lib/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "lib/**/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "bin/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "bin/**/*.esc",
                    },
                  },
                ],
              },
              "didDelete": {
                "filters": [
                  {
                    "pattern": {
                      "glob": "lib/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "lib/**/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "bin/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "bin/**/*.esc",
                    },
                  },
                ],
              },
              "didRename": {
                "filters": [
                  {
                    "pattern": {
                      "glob": "lib/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "lib/**/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "bin/*.esc",
                    },
                  },
                  {
                    "pattern": {
                      "glob": "bin/**/*.esc",
                    },
                  },
                ],
              },
            },
          },
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
        rootUri,
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

    const text = 'console.log("Hello, world!")\nval x =\n';
    fs.writeFileSync(filePath(), text);

    await client.initialize({
        processId: process.pid,
        rootUri,
        capabilities: {},
    });

    client.textDocumentDidOpen({
        textDocument: {
            uri: fileUri(),
            version: 2,
            languageId: 'escalier',
            text,
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
      ]
    `);
});

test('textDocument/didChange', async () => {
    let diagnostics: lsp.PublishDiagnosticsParams['diagnostics'] | null = null;

    client.onTextDocumentPublishDiagnostics((params) => {
        console.log('Received diagnostics');
        diagnostics = params.diagnostics;
    });

    fs.writeFileSync(filePath(), '');

    await client.initialize({
        processId: process.pid,
        rootUri,
        capabilities: {},
    });

    client.textDocumentDidOpen({
        textDocument: {
            uri: fileUri(),
            version: 2,
            languageId: 'escalier',
            text: 'console.log("Hello, world!")\nval x = 5\n',
        },
    });

    client.textDocumentDidChange({
        textDocument: {
            uri: fileUri(),
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    });

    // Wait for the diagnostics from didChange (not didOpen).
    // didOpen with valid code may publish diagnostics as [] first,
    // so we wait until we see the actual error diagnostic.
    await vi.waitFor(
        () => {
            expect(diagnostics?.length).toBeGreaterThan(0);
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
      ]
    `);
});

test('textDocument/codeAction', async () => {
    await client.initialize({
        processId: process.pid,
        rootUri,
        capabilities: {},
    });

    const actions = await client.textDocumentCodeAction({
        textDocument: {
            uri: fileUri(),
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
    fs.writeFileSync(filePath(), '');

    await client.initialize({
        processId: process.pid,
        rootUri,
        capabilities: {},
    });

    let diagnosticsPublished = false;

    client.onTextDocumentPublishDiagnostics((_params) => {
        diagnosticsPublished = true;
    });

    client.textDocumentDidOpen({
        textDocument: {
            uri: fileUri(),
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
        arguments: [fileUri()],
    });

    const resp = response as Record<string, unknown>;
    expect(resp.languageId).toBe('javascript');
    expect(resp.version).toBe(0);
    expect(resp.uri).toBe(`${rootUri}/bin/foo.js`);
    expect(resp.text).toContain('const x = 5;');
});

test('constructing a second client leaves the running one usable', async () => {
    // Go resolves `globalThis.fs` on every syscall. Building a Client used to
    // install its shim there, which repointed this already-running server's
    // stdin at the new streams, and it never read another message. This
    // initialize would hang.
    const second = new Client(buffer, process.cwd(), fs);
    expect(second).toBeDefined();

    const initResult = await client.initialize({
        processId: process.pid,
        rootUri,
        capabilities: {},
    });
    expect(initResult).toBeDefined();
});

test('a second client refuses to run while one is already running', async () => {
    // One set of globals serves every runtime, so the second would take over
    // the first's streams. Refusing says so where the mistake is.
    const second = new Client(buffer, process.cwd(), fs);
    await expect(second.run()).rejects.toThrow(
        'an LSP server is already running: only one Client can run at a time',
    );
});
