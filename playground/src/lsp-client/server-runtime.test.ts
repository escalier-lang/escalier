import * as fs from 'node:fs';
import * as path from 'node:path';
import { expect, test, vi } from 'vitest';

import {
    OutputStream,
    STDERR_TAIL_CHARS,
    ServerRuntime,
    SimpleStream,
    StderrTail,
} from './server-runtime';

const wasm = fs.readFileSync(
    path.join(__dirname, '../../../bin/lsp-server.wasm'),
);

/** Returns a runtime for module that no test starts. */
function idleRuntime(module: WebAssembly.Module): ServerRuntime {
    return new ServerRuntime(module, {
        cwd: process.cwd(),
        fs: {} as never,
        env: {},
        stdin: new SimpleStream(),
        stdout: new OutputStream(),
    });
}

test('a trap thrown out of _resume ends the server the way an exit does', async () => {
    const runtime = idleRuntime(await WebAssembly.compile(wasm));
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
        // @ts-expect-error: go is private
        const go = runtime.go;
        // The instance never ran, so resuming it throws, as a trap would.
        go._resume();

        expect(go.exited).toBe(true);
        expect(error).toHaveBeenCalledWith(
            '[Escalier LSP] the server trapped:',
            expect.any(TypeError),
        );
        expect(runtime.stderrText()).toContain('TypeError');
    } finally {
        error.mockRestore();
    }
});

test('a runtime refuses to run while another in the same thread is running', async () => {
    // The server waits on stdin, which keeps the first runtime in run() until the test
    // sends it `exit`.
    const module = await WebAssembly.compile(wasm);
    const first = idleRuntime(module);
    const second = idleRuntime(module);
    first.silence();
    const running = first.run();

    await expect(second.run()).rejects.toThrow(
        'an LSP server is already running in this thread: only one can run at a time',
    );

    // `exit` returns the server from its main, which ends first.run().
    // @ts-expect-error: stdin is private
    first.stdin.write(
        new TextEncoder().encode(
            'Content-Length: 33\r\n\r\n{"jsonrpc":"2.0","method":"exit"}',
        ),
    );
    await running;
});

test('OutputStream passes each chunk to its listeners', () => {
    const stream = new OutputStream();
    const heard: number[][] = [];
    const subscription = stream.on('data', (chunk) => heard.push([...chunk]));

    stream.write(new Uint8Array([1, 2]));
    subscription.dispose();
    stream.write(new Uint8Array([3]));

    expect(heard).toEqual([[1, 2]]);
});

test('StderrTail keeps the last STDERR_TAIL_CHARS of what it records', () => {
    const tail = new StderrTail();
    tail.record('a'.repeat(STDERR_TAIL_CHARS));
    tail.record('b'.repeat(STDERR_TAIL_CHARS));
    tail.record('end');

    const text = tail.text();

    expect(text).toHaveLength(STDERR_TAIL_CHARS);
    expect(text.endsWith('bend')).toBe(true);
});
