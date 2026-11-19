import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

import { afterEach, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { BrowserFS } from '#fs/browser-fs';

import { Client } from './client';
import { OutputStream, SimpleStream } from './server-runtime';
import { type WorkerLike, workerLauncher } from './worker-launcher';
import {
    type FromWorkerMessage,
    type ToWorkerMessage,
    runServerInWorker,
} from './worker-protocol';

const wasm = fs.readFileSync(
    path.join(__dirname, '../../../bin/lsp-server.wasm'),
);
const interopData = path.join(__dirname, '../../../internal/interop/data');

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/** A WorkerLike whose messages the test reads and sends by hand. */
class FakeWorker implements WorkerLike {
    received: ToWorkerMessage[] = [];
    terminated = false;
    onerror: ((event: ErrorEvent) => void) | null = null;
    onmessageerror: ((event: MessageEvent) => void) | null = null;
    private listeners = new Set<(event: MessageEvent) => void>();

    postMessage(message: unknown) {
        this.received.push(message as ToWorkerMessage);
    }

    addEventListener(
        _type: 'message',
        listener: (event: MessageEvent) => void,
    ) {
        this.listeners.add(listener);
    }

    removeEventListener(
        _type: 'message',
        listener: (event: MessageEvent) => void,
    ) {
        this.listeners.delete(listener);
    }

    terminate() {
        this.terminated = true;
    }

    /** Delivers message to the launcher as if the worker had sent it. */
    send(message: FromWorkerMessage) {
        for (const listener of this.listeners) {
            listener({ data: message } as MessageEvent);
        }
    }
}

/** Returns the text of each StdinMessage in messages, in order. */
function stdinText(messages: ToWorkerMessage[]): string[] {
    return messages.flatMap((m) =>
        m.kind === 'stdin' ? [decoder.decode(m.chunk)] : [],
    );
}

function launchFake() {
    const worker = new FakeWorker();
    const launcher = workerLauncher(
        { wasm, cwd: '/', env: { A: '1' } },
        new BrowserFS({}),
        () => worker,
    );
    const stdin = new SimpleStream();
    const stdout = new OutputStream();
    return { worker, launcher, stdin, stdout };
}

test('starts the worker with the compiled server, then forwards stdin written before and after', async () => {
    const { worker, launcher, stdin, stdout } = launchFake();
    stdin.write(encoder.encode('before'));

    await launcher.launch(stdin, stdout);
    stdin.write(encoder.encode('after'));

    expect(worker.received[0]).toEqual({
        kind: 'start',
        module: expect.any(WebAssembly.Module),
        cwd: '/',
        env: { A: '1' },
    });
    expect(stdinText(worker.received)).toEqual(['before', 'after']);
    // The launcher takes what it forwards off the stream.
    expect(stdin.read()).toBeUndefined();
});

test('writes what the worker sends as stdout to the stdout stream', async () => {
    const { worker, launcher, stdin, stdout } = launchFake();
    const heard: string[] = [];
    stdout.on('data', (chunk) => heard.push(decoder.decode(chunk)));
    await launcher.launch(stdin, stdout);

    worker.send({ kind: 'stdout', chunk: encoder.encode('reply') });

    expect(heard).toEqual(['reply']);
});

test('ends the server when the worker says it exited, and resolves with its stderr', async () => {
    const { worker, launcher, stdin, stdout } = launchFake();
    const server = await launcher.launch(stdin, stdout);

    worker.send({ kind: 'stderr', text: 'panic: boom\n' });
    worker.send({ kind: 'exited' });

    await expect(server.exited).resolves.toBe('panic: boom\n');
    expect(worker.terminated).toBe(true);
    // Nothing reaches a terminated worker.
    stdin.write(encoder.encode('late'));
    expect(stdinText(worker.received)).toEqual([]);
});

test('dispose terminates the worker and resolves exited', async () => {
    const { worker, launcher, stdin, stdout } = launchFake();
    const server = await launcher.launch(stdin, stdout);
    worker.send({ kind: 'stderr', text: 'partial' });

    server.dispose();

    expect(worker.terminated).toBe(true);
    await expect(server.exited).resolves.toBe('partial');
});

test('a worker that fails ends the server with the error in its stderr', async () => {
    const { worker, launcher, stdin, stdout } = launchFake();
    const server = await launcher.launch(stdin, stdout);
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
        worker.onerror?.({
            message: 'SyntaxError: bad script',
        } as ErrorEvent);
    } finally {
        error.mockRestore();
    }

    await expect(server.exited).resolves.toBe('\nSyntaxError: bad script\n');
    expect(worker.terminated).toBe(true);
});

test('a message from the worker that is lost ends the server', async () => {
    const { worker, launcher, stdin, stdout } = launchFake();
    const server = await launcher.launch(stdin, stdout);
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
        worker.onmessageerror?.({} as MessageEvent);
    } finally {
        error.mockRestore();
    }

    await expect(server.exited).resolves.toBe(
        '\na message from the server worker was lost\n',
    );
    expect(worker.terminated).toBe(true);
});

test('rejects when the server does not compile', async () => {
    const launcher = workerLauncher(
        { wasm: new Uint8Array([0, 1, 2, 3]).buffer, cwd: '/' },
        new BrowserFS({}),
        () => new FakeWorker(),
    );

    await expect(
        launcher.launch(new SimpleStream(), new OutputStream()),
    ).rejects.toThrow(WebAssembly.CompileError);
});

// The test below runs the real server on the worker side of a MessageChannel in this
// thread, so the server's stdio and file syscalls all cross the bridge.

/** Returns a WorkerLike whose other end runs runServerInWorker in this thread. */
function inThreadWorker(): WorkerLike {
    const channel = new MessageChannel();
    channel.port1.start();
    channel.port2.start();
    runServerInWorker(channel.port2);
    return Object.assign(channel.port1, {
        onerror: null,
        onmessageerror: null,
        terminate: () => {
            channel.port1.close();
            channel.port2.close();
        },
    });
}

let tmpDir: string | undefined;

afterEach(() => {
    if (tmpDir) {
        fs.rmSync(tmpDir, { recursive: true, force: true });
        tmpDir = undefined;
    }
});

test(
    'a client checks a document through a server behind the bridge',
    { timeout: 60_000 },
    async () => {
        tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'escalier-worker-'));
        fs.mkdirSync(path.join(tmpDir, 'bin'));
        const uri = `file://${tmpDir}/bin/foo.esc`;
        const text = 'val y: number = "a"\n';
        fs.writeFileSync(path.join(tmpDir, 'bin', 'foo.esc'), text);

        const client = new Client(
            workerLauncher(
                {
                    wasm,
                    cwd: process.cwd(),
                    env: {
                        ESCALIER_BUILTINS_DIR: interopData,
                        ESCALIER_STDLIB_DIR: interopData,
                    },
                },
                fs,
                inThreadWorker,
            ),
        );
        const published: lsp.PublishDiagnosticsParams[] = [];
        client.onTextDocumentPublishDiagnostics((params) =>
            published.push(params),
        );
        const run = client.run();

        const init = await client.initialize({
            processId: null,
            rootUri: `file://${tmpDir}`,
            capabilities: {},
        });
        void client.textDocumentDidOpen({
            textDocument: { uri, version: 1, languageId: 'escalier', text },
        });

        expect(init.capabilities.hoverProvider).toBe(true);
        await vi.waitFor(
            () => expect(published.find((p) => p.uri === uri)).toBeDefined(),
            { timeout: 30_000 },
        );
        expect(
            published
                .find((p) => p.uri === uri)
                ?.diagnostics.map((d) => d.message),
        ).toEqual(['cannot constrain "a" <: number']);

        await client.stop();
        await run;
    },
);
