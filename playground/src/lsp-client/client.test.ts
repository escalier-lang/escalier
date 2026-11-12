import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { Client, asNodeBuffer } from './client';

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
        // The type checker resolves `std:prelude` and the ambient builtins from
        // the same directory.
        ESCALIER_STDLIB_DIR: path.join(
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
              "quickfix",
              "refactor.rewrite",
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
          "semanticTokensProvider": {
            "full": true,
            "legend": {
              "tokenModifiers": [
                "mutable",
                "reassignable",
              ],
              "tokenTypes": [
                "variable",
                "parameter",
                "method",
                "property",
              ],
            },
          },
          "signatureHelpProvider": {
            "retriggerCharacters": [
              ")",
            ],
            "triggerCharacters": [
              "(",
              ",",
            ],
          },
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

    await expect(promise).rejects.toMatchObject({
        code: -32601,
        message: 'method not supported: foo/bar',
    });
});

// The server's first check loads the standard library, which takes several
// seconds in WebAssembly under coverage instrumentation. A test that waits for
// a check gets longer than Vitest's 5-second default to see it finish.
const CHECK_TEST_TIMEOUT_MS = 20_000;

test('textDocument/didOpen', { timeout: CHECK_TEST_TIMEOUT_MS }, async () => {
    let diagnostics: lsp.PublishDiagnosticsParams['diagnostics'] | null = null;

    client.onTextDocumentPublishDiagnostics((params) => {
        console.log('Received diagnostics');
        diagnostics = params.diagnostics;
    });

    const text =
        'import "std:console"\nconsole.log("Hello, world!")\nval x =\n';
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
              "line": 3,
            },
            "start": {
              "character": 0,
              "line": 3,
            },
          },
          "severity": 1,
          "source": "escalier",
        },
      ]
    `);
});

// A UTF-16 character count undercounts the bytes of non-ASCII text, so this test
// asserts that a document holding some still reaches the server whole and
// gets checked.
test(
    'textDocument/didOpen with non-ASCII text',
    { timeout: CHECK_TEST_TIMEOUT_MS },
    async () => {
        const published: string[][] = [];
        client.onTextDocumentPublishDiagnostics((params) => {
            published.push(params.diagnostics.map((d) => d.message));
        });

        const text = 'val greeting: number = "こんにちは, 世界"\n';
        fs.writeFileSync(filePath(), text);

        await client.initialize({
            processId: process.pid,
            rootUri,
            capabilities: {},
        });
        client.textDocumentDidOpen({
            textDocument: {
                uri: fileUri(),
                version: 1,
                languageId: 'escalier',
                text,
            },
        });

        await vi.waitFor(() => expect(published).not.toEqual([]), {
            timeout: 10000,
        });
        expect(published[0]).toEqual([
            'cannot constrain "こんにちは, 世界" <: number',
        ]);
    },
);

test('textDocument/didChange', { timeout: CHECK_TEST_TIMEOUT_MS }, async () => {
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
            text: 'import "std:console"\nconsole.log("Hello, world!")\nval x = 5\n',
        },
    });

    client.textDocumentDidChange({
        textDocument: {
            uri: fileUri(),
            version: 2,
        },
        contentChanges: [
            {
                text: 'import "std:console"\nconsole.log("Hello, world!")\nval x =\n',
            },
        ],
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
              "line": 3,
            },
            "start": {
              "character": 0,
              "line": 3,
            },
          },
          "severity": 1,
          "source": "escalier",
        },
      ]
    `);
});

test(
    'textDocument/signatureHelp',
    { timeout: CHECK_TEST_TIMEOUT_MS },
    async () => {
        const text =
            'fn add(a: number, b: number) -> number { return a + b }\nval x = add(1, )\n';
        fs.writeFileSync(filePath(), text);

        await client.initialize({
            processId: process.pid,
            rootUri,
            capabilities: {},
        });

        client.textDocumentDidOpen({
            textDocument: {
                uri: fileUri(),
                version: 1,
                languageId: 'escalier',
                text,
            },
        });

        // The cursor sits after `add(1, `, in the second argument.
        const help = await client.textDocumentSignatureHelp({
            textDocument: { uri: fileUri() },
            position: { line: 1, character: 15 },
        });

        expect(help).toMatchInlineSnapshot(`
          {
            "activeParameter": 1,
            "activeSignature": 0,
            "signatures": [
              {
                "activeParameter": 1,
                "label": "add(a: number, b: number) -> number",
                "parameters": [
                  {
                    "label": [
                      4,
                      13,
                    ],
                  },
                  {
                    "label": [
                      15,
                      24,
                    ],
                  },
                ],
              },
            ],
          }
        `);
    },
);

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
            text: 'import "std:console"\nconsole.log("Hello, world!")\nval x = 5\n',
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

test(
    'textDocument/semanticTokens/full',
    { timeout: CHECK_TEST_TIMEOUT_MS },
    async () => {
        let diagnosticsPublished = false;
        client.onTextDocumentPublishDiagnostics((_params) => {
            diagnosticsPublished = true;
        });

        const text = 'val mut p = {x: 0}\np.x = 1\n';
        fs.writeFileSync(filePath(), text);

        await client.initialize({
            processId: process.pid,
            rootUri,
            capabilities: {},
        });

        client.textDocumentDidOpen({
            textDocument: {
                uri: fileUri(),
                version: 1,
                languageId: 'escalier',
                text,
            },
        });

        await vi.waitFor(
            () => {
                expect(diagnosticsPublished).toBeTruthy();
            },
            { timeout: 10_000 },
        );

        const tokens = await client.textDocumentSemanticTokensFull({
            textDocument: { uri: fileUri() },
        });

        // Each token is five integers: line delta, start column delta, length, type
        // index, and modifier bits. Both tokens are `p`, a variable (type 0) with the
        // mutable modifier (bit 0, value 1).
        expect(tokens?.data).toEqual([0, 8, 1, 0, 1, 1, 0, 1, 0, 1]);
    },
);

test(
    'textDocument/semanticTokens/full encodes reassignable modifier bits',
    { timeout: CHECK_TEST_TIMEOUT_MS },
    async () => {
        let diagnosticsPublished = false;
        client.onTextDocumentPublishDiagnostics((_params) => {
            diagnosticsPublished = true;
        });

        const text = 'var n = 0\nvar mut q = {x: 0}\nq.x = n\n';
        fs.writeFileSync(filePath(), text);

        const initResult = await client.initialize({
            processId: process.pid,
            rootUri,
            capabilities: {},
        });
        const legend = initResult.capabilities.semanticTokensProvider?.legend;
        expect(legend?.tokenModifiers).toEqual(['mutable', 'reassignable']);

        client.textDocumentDidOpen({
            textDocument: {
                uri: fileUri(),
                version: 1,
                languageId: 'escalier',
                text,
            },
        });

        await vi.waitFor(
            () => {
                expect(diagnosticsPublished).toBeTruthy();
            },
            { timeout: 10_000 },
        );

        const tokens = await client.textDocumentSemanticTokensFull({
            textDocument: { uri: fileUri() },
        });

        // Each token is five integers, and the last holds its modifier bits in legend
        // order. `n` is reassignable (bit 1, value 2). `q` is mutable and
        // reassignable (bits 0 and 1, value 3).
        const data = tokens?.data ?? [];
        const perToken: number[][] = [];
        for (let i = 0; i < data.length; i += 5) {
            perToken.push(data.slice(i, i + 5));
        }
        expect(perToken).toEqual([
            [0, 4, 1, 0, 2], // n
            [1, 8, 1, 0, 3], // q
            [1, 0, 1, 0, 3], // q read
            [0, 6, 1, 0, 2], // n read
        ]);
    },
);

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

// The tests below drive a Client's stdin and stdout directly. They build their own
// Client and never call run(), so no LSP server is involved.

/** Returns a Client that no server reads from or writes to. */
function standaloneClient(): Client {
    const mockFS = {
        fstat: vi.fn(),
        lstat: vi.fn(),
        open: vi.fn(),
        read: vi.fn(),
    };
    return new Client(buffer, process.cwd(), mockFS as any);
}

/** Returns payload framed with a Content-Length header that counts its UTF-8 bytes. */
function framed(payload: string): Uint8Array {
    const encoder = new TextEncoder();
    const body = encoder.encode(payload);
    const header = encoder.encode(`Content-Length: ${body.length}\r\n\r\n`);
    const message = new Uint8Array(header.length + body.length);
    message.set(header);
    message.set(body, header.length);
    return message;
}

/** Returns the JSON of a window/logMessage notification carrying message. */
function logMessage(message: string): string {
    return JSON.stringify({
        jsonrpc: '2.0',
        method: 'window/logMessage',
        params: { type: 3, message },
    });
}

/** Returns the text the client has written to the server's stdin so far. */
function readStdin(testClient: Client): string {
    // @ts-expect-error: stdin is private
    const stdin = testClient.stdin;
    const chunks: Uint8Array[] = [];
    for (let chunk = stdin.read(); chunk; chunk = stdin.read()) {
        chunks.push(chunk);
    }
    const bytes = new Uint8Array(chunks.reduce((n, c) => n + c.length, 0));
    let offset = 0;
    for (const chunk of chunks) {
        bytes.set(chunk, offset);
        offset += chunk.length;
    }
    return new TextDecoder().decode(bytes);
}

test('reassembles a message split across stdout chunks', async () => {
    const testClient = standaloneClient();
    const received: string[] = [];
    testClient.onWindowLogMessage((params) => received.push(params.message));

    // 100-byte chunks split some of the three-byte characters between chunks.
    const text = `こんにちは ${'x'.repeat(5000)} 世界`;
    const message = framed(logMessage(text));
    // @ts-expect-error: stdout is private
    const stdout = testClient.stdout;
    for (let i = 0; i < message.length; i += 100) {
        stdout.write(message.subarray(i, i + 100));
    }

    await vi.waitFor(() => expect(received).toEqual([text]));
    await testClient.stop();
});

test('reads every message that arrives in one stdout chunk', async () => {
    const testClient = standaloneClient();
    const received: string[] = [];
    testClient.onWindowLogMessage((params) => received.push(params.message));

    const first = framed(logMessage('°C'));
    const second = framed(logMessage('second'));
    const chunk = new Uint8Array(first.length + second.length);
    chunk.set(first);
    chunk.set(second, first.length);
    // @ts-expect-error: stdout is private
    testClient.stdout.write(chunk);

    await vi.waitFor(() => expect(received).toEqual(['°C', 'second']));
    await testClient.stop();
});

test('calls every listener registered for a notification', async () => {
    const testClient = standaloneClient();
    const first: string[] = [];
    const second: string[] = [];
    testClient.onWindowLogMessage((params) => first.push(params.message));
    testClient.onWindowLogMessage((params) => second.push(params.message));

    // @ts-expect-error: stdout is private
    testClient.stdout.write(framed(logMessage('hello')));

    await vi.waitFor(() => expect(second).toEqual(['hello']));
    expect(first).toEqual(['hello']);
    await testClient.stop();
});

test.each<[string, (client: Client) => Promise<void>]>([
    [
        'textDocument/willSave',
        (c) =>
            c.textDocumentWillSave({
                textDocument: { uri: 'file:///a.esc' },
                reason: 1,
            }),
    ],
    [
        'textDocument/didSave',
        (c) =>
            c.textDocumentDidSave({ textDocument: { uri: 'file:///a.esc' } }),
    ],
    [
        'textDocument/didClose',
        (c) =>
            c.textDocumentDidClose({ textDocument: { uri: 'file:///a.esc' } }),
    ],
    [
        'workspace/didChangeWatchedFiles',
        (c) => c.workspaceDidChangeWatchedFiles({ changes: [] }),
    ],
    ['exit', (c) => c.exit()],
])('sends %s as a notification', async (method, send) => {
    const testClient = standaloneClient();

    await send(testClient);

    const body = JSON.parse(readStdin(testClient).split('\r\n\r\n')[1]);
    expect(body.method).toBe(method);
    expect(body).not.toHaveProperty('id');
    await testClient.stop();
});

test('asNodeBuffer views the same bytes as a Buffer where Buffer exists', () => {
    const bytes = new Uint8Array([0, 1, 2, 3, 4]).subarray(1, 4);

    const view = asNodeBuffer(bytes);

    expect(Buffer.isBuffer(view)).toBe(true);
    expect([...view]).toEqual([1, 2, 3]);
    expect(view.buffer).toBe(bytes.buffer);
});

test('asNodeBuffer returns the chunk itself where Buffer is undefined', () => {
    const bytes = new Uint8Array([1, 2, 3]);
    vi.stubGlobal('Buffer', undefined);
    try {
        expect(asNodeBuffer(bytes)).toBe(bytes);
    } finally {
        vi.unstubAllGlobals();
    }
});

test('drops a notification sent after stop()', async () => {
    const testClient = standaloneClient();
    await testClient.stop();
    // @ts-expect-error: stdin is private
    const stdin = testClient.stdin;
    while (stdin.read()) {}

    await expect(
        testClient.textDocumentDidClose({
            textDocument: { uri: 'file:///lib/index.esc' },
        }),
    ).resolves.toBeUndefined();
    expect(stdin.read()).toBeUndefined();
});

test('frames a message it sends with its length in UTF-8 bytes', async () => {
    const testClient = standaloneClient();
    const params = {
        textDocument: {
            uri: 'file:///lib/index.esc',
            languageId: 'escalier',
            version: 1,
            text: 'val greeting = "こんにちは"',
        },
    };

    await testClient.textDocumentDidOpen(params);

    const text = readStdin(testClient);
    const [header, body] = text.split('\r\n\r\n');

    expect(header).toBe(
        `Content-Length: ${new TextEncoder().encode(body).length}`,
    );
    expect(JSON.parse(body)).toEqual({
        jsonrpc: '2.0',
        method: 'textDocument/didOpen',
        params,
    });
    await testClient.stop();
});
