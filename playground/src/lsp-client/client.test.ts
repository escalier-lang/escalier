import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { Client, frameMessage, takeMessages } from './client';

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

// The two tests below exercise how the client reassembles a response that
// arrives split across several stdout chunks. A message is delimited by a
// `Content-Length` header rather than by the chunk it came in, so the client
// has to buffer until it holds the whole body. They build their own Client and
// never call run(), so they drive its stdout directly and no LSP server is
// involved.

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

test('frameMessage counts the payload in UTF-8 bytes', () => {
    const decoder = new TextDecoder();

    expect(decoder.decode(frameMessage('{"text":"°C"}'))).toBe(
        'Content-Length: 14\r\n\r\n{"text":"°C"}',
    );
});

test('takeMessages splits complete messages from the bytes left over', () => {
    const first = frameMessage('{"id":1,"text":"こんにちは"}');
    const second = frameMessage('{"id":2}');
    // The buffer holds both messages and the start of a third. The third message
    // ends in `世界"}`, so cutting it 7 bytes before the end splits the three
    // bytes of `世`.
    const third = frameMessage('{"id":3,"text":"世界"}');
    const split = third.length - 7;
    const buffer = new Uint8Array(first.length + second.length + split);
    buffer.set(first);
    buffer.set(second, first.length);
    buffer.set(third.subarray(0, split), first.length + second.length);

    const { payloads, malformed, rest } = takeMessages(buffer);

    expect(payloads).toEqual(['{"id":1,"text":"こんにちは"}', '{"id":2}']);
    expect(malformed).toEqual([]);
    expect(rest).toEqual(third.subarray(0, split));

    // The rest of the third message completes it, and its split character
    // decodes whole.
    const completed = new Uint8Array(third.length);
    completed.set(rest);
    completed.set(third.subarray(split), split);
    expect(takeMessages(completed).payloads).toEqual([
        '{"id":3,"text":"世界"}',
    ]);
});

test('takeMessages reads the Content-Length header in any case', () => {
    const encoder = new TextEncoder();

    expect(
        takeMessages(encoder.encode('content-length: 8\r\n\r\n{"id":5}'))
            .payloads,
    ).toEqual(['{"id":5}']);
});

test('takeMessages skips the body of a header without a Content-Length', () => {
    const encoder = new TextEncoder();
    // The skipped body holds a blank line, which would read as the end of a header.
    const bad = encoder.encode(
        'Content-Type: text/plain\r\n\r\nline one\r\n\r\nline two',
    );
    const good = frameMessage('{"id":6}');
    const buffer = new Uint8Array(bad.length + good.length);
    buffer.set(bad);
    buffer.set(good, bad.length);

    expect(takeMessages(buffer)).toEqual({
        payloads: ['{"id":6}'],
        malformed: ['Content-Type: text/plain'],
        rest: new Uint8Array(0),
    });
});

test('takeMessages drops a malformed message when no Content-Length follows', () => {
    const encoder = new TextEncoder();

    expect(
        takeMessages(encoder.encode('Content-Type: text/plain\r\n\r\n{"a":1}')),
    ).toEqual({
        payloads: [],
        malformed: ['Content-Type: text/plain'],
        rest: new Uint8Array(0),
    });
});

test('takeMessages skips a header without a Content-Length', () => {
    const encoder = new TextEncoder();
    const bad = encoder.encode('Content-Type: text/plain\r\n\r\n');
    const good = frameMessage('{"id":4}');
    const buffer = new Uint8Array(bad.length + good.length);
    buffer.set(bad);
    buffer.set(good, bad.length);

    expect(takeMessages(buffer)).toEqual({
        payloads: ['{"id":4}'],
        malformed: ['Content-Type: text/plain'],
        rest: new Uint8Array(0),
    });
});
