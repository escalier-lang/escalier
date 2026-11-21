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

test('a stdin read still waiting when the server ends stops listening to stdin', async () => {
    const { runtime, shim, stdin } = await shimmedRuntime();
    // @ts-expect-error: emitter is private
    const listeners = () => stdin.emitter.listenerCount('data');
    shim.read(0, new Uint8Array(4), 0, 4, null, vi.fn());
    expect(listeners()).toBe(1);

    // A server that ends at once, as one that panics in another goroutine would
    // while its stdin read waits.
    // @ts-expect-error: go is private
    runtime.go.run = async () => {};
    await runtime.run();

    expect(listeners()).toBe(0);
});

test('the fs shim answers a read of no bytes from stdin at once', async () => {
    const { shim } = await shimmedRuntime();
    const buffer = new Uint8Array(4);
    const callback = vi.fn();

    shim.read(0, buffer, 0, 0, null, callback);

    expect(callback).toHaveBeenCalledWith(null, 0, buffer);
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

/** Returns an FSAPI whose every method is a mock that calls back with no error. */
function mockFS() {
    const callBack = (...args: unknown[]) => {
        const callback = args.at(-1) as (err: null, value?: unknown) => void;
        callback(null, 'value');
    };
    return {
        fstat: vi.fn(callBack),
        lstat: vi.fn(callBack),
        stat: vi.fn(callBack),
        open: vi.fn(callBack),
        close: vi.fn(callBack),
        read: vi.fn(callBack),
        write: vi.fn(callBack),
        readdir: vi.fn(callBack),
        writeFile: vi.fn(callBack),
        mkdir: vi.fn(callBack),
        unlink: vi.fn(callBack),
        rmdir: vi.fn(callBack),
        rename: vi.fn(callBack),
        symlink: vi.fn(callBack),
    };
}

test.each<[string, unknown[], string, unknown[]]>([
    ['fstat', [3], 'fstat', [3]],
    ['lstat', ['/a'], 'lstat', ['/a']],
    ['stat', ['/a'], 'stat', ['/a']],
    ['open', ['/a', 0, 0o644], 'open', ['/a', 0, 0o644]],
    ['close', [3], 'close', [3]],
    // Go passes a permission to mkdir, which the FSAPI doesn't take.
    ['mkdir', ['/a', 0o755], 'mkdir', ['/a']],
    ['readdir', ['/a'], 'readdir', ['/a']],
    ['rename', ['/a', '/b'], 'rename', ['/a', '/b']],
    ['rmdir', ['/a'], 'rmdir', ['/a']],
    ['symlink', ['/a', '/b'], 'symlink', ['/a', '/b']],
    ['unlink', ['/a'], 'unlink', ['/a']],
])(
    'the fs shim forwards %s to the FSAPI',
    async (method, args, fsMethod, fsArgs) => {
        const fsMock = mockFS();
        const runtime = new ServerRuntime(await WebAssembly.compile(wasm), {
            cwd: '/',
            fs: fsMock,
            env: {},
            stdin: new SimpleStream(),
            stdout: new OutputStream(),
        });
        // @ts-expect-error: fsShim is private
        const shim = runtime.fsShim as Record<
            string,
            (...a: unknown[]) => void
        >;
        const callback = vi.fn();

        shim[method](...args, callback);

        expect(fsMock[fsMethod as keyof typeof fsMock]).toHaveBeenCalledWith(
            ...fsArgs,
            expect.any(Function),
        );
        expect(callback).toHaveBeenCalledWith(null, 'value');
    },
);

/** Returns a runtime over a mock FSAPI, with its fs shim and the streams it uses. */
async function shimmedRuntime() {
    const fsMock = mockFS();
    const stdin = new SimpleStream();
    const stdout = new OutputStream();
    const stderr: string[] = [];
    const runtime = new ServerRuntime(await WebAssembly.compile(wasm), {
        cwd: '/',
        fs: fsMock,
        env: {},
        stdin,
        stdout,
        onStderr: (text) => stderr.push(text),
    });
    // @ts-expect-error: fsShim is private
    const shim = runtime.fsShim as Record<string, (...a: any[]) => any>;
    return { runtime, shim, fsMock, stdin, stdout, stderr };
}

const bytes = (text: string) => new TextEncoder().encode(text);

test('the fs shim reads stdin from the stream, and waits for a chunk when it is empty', async () => {
    vi.useFakeTimers();
    try {
        const { shim, stdin } = await shimmedRuntime();
        const buffer = new Uint8Array(8);
        const callback = vi.fn();

        shim.read(0, buffer, 2, 4, null, callback);
        expect(callback).not.toHaveBeenCalled();
        stdin.write(bytes('abcdef'));
        vi.runAllTimers();

        expect(callback).toHaveBeenCalledWith(null, 4, buffer);
        expect(new TextDecoder().decode(buffer.subarray(2, 6))).toBe('abcd');
        // The rest of the chunk stays queued for the next read.
        shim.read(0, buffer, 0, 8, null, callback);
        expect(callback).toHaveBeenLastCalledWith(null, 2, buffer);
    } finally {
        vi.useRealTimers();
    }
});

test('the fs shim reads other files through the FSAPI', async () => {
    const { shim, fsMock } = await shimmedRuntime();
    const buffer = new Uint8Array(4);
    const callback = vi.fn();

    shim.read(3, buffer, 0, 4, 0, callback);

    expect(fsMock.read).toHaveBeenCalledWith(3, buffer, 0, 4, 0, callback);
});

test('the fs shim sends stdout to the stream, stderr to onStderr, and other files to the FSAPI', async () => {
    vi.useFakeTimers();
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    try {
        const { runtime, shim, fsMock, stdout, stderr } =
            await shimmedRuntime();
        const heard: string[] = [];
        stdout.on('data', (chunk) =>
            heard.push(new TextDecoder().decode(chunk)),
        );
        const callback = vi.fn();

        shim.write(1, bytes('out'), 0, 3, null, callback);
        shim.write(2, bytes('err'), 0, 3, null, callback);
        runtime.silence();
        shim.write(2, bytes('quiet'), 0, 5, null, callback);
        shim.write(3, bytes('file'), 0, 4, null, callback);
        vi.runAllTimers();

        expect(heard).toEqual(['out']);
        expect(stderr).toEqual(['err', 'quiet']);
        // A silenced runtime keeps its stderr out of the console.
        expect(warn).toHaveBeenCalledOnce();
        expect(fsMock.write).toHaveBeenCalledWith(
            3,
            bytes('file'),
            0,
            4,
            null,
            expect.any(Function),
        );
        expect(callback).toHaveBeenCalledTimes(4);
    } finally {
        warn.mockRestore();
        vi.useRealTimers();
    }
});

test('writeSync prints stderr a line at a time and records it', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
        const { runtime, shim } = await shimmedRuntime();

        shim.writeSync(2, bytes('panic: boom'));
        shim.writeSync(2, bytes('\n'));

        expect(error).toHaveBeenCalledWith('panic: boom');
        expect(runtime.stderrText()).toBe('panic: boom\n');
    } finally {
        error.mockRestore();
    }
});

test('the process shim reports the cwd it was given, and no user or group', async () => {
    const { runtime } = await shimmedRuntime();
    // @ts-expect-error: processShim is private
    const shim = runtime.processShim as Record<string, () => unknown>;

    expect(shim.cwd()).toBe('/');
    expect([
        shim.getuid(),
        shim.getgid(),
        shim.geteuid(),
        shim.getegid(),
    ]).toEqual([-1, -1, -1, -1]);
});

test.each(['getgroups', 'umask', 'chdir'])(
    'the process shim throws ENOSYS from %s',
    async (method) => {
        const { runtime } = await shimmedRuntime();
        // @ts-expect-error: processShim is private
        const shim = runtime.processShim as Record<string, () => unknown>;

        expect(() => shim[method]()).toThrow(
            expect.objectContaining({ code: 'ENOSYS' }),
        );
    },
);

test('writeSync logs what the server prints to stdout or another fd', async () => {
    const log = vi.spyOn(console, 'log').mockImplementation(() => {});
    try {
        const { shim } = await shimmedRuntime();
        const other = bytes('x');

        shim.writeSync(1, bytes('hello'));
        shim.writeSync(3, other);

        expect(log.mock.calls).toEqual([
            ['writeSync:', 'hello'],
            ['writeSync:', 3, other],
        ]);
    } finally {
        log.mockRestore();
    }
});

test('runToEnd resolves with the error when the server fails to start', async () => {
    // The smallest valid module, which has none of the exports Go's run() needs.
    const empty = await WebAssembly.compile(
        new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0]),
    );
    const runtime = idleRuntime(empty);
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
        const stderr = await runtime.runToEnd();

        expect(error).toHaveBeenCalledWith(
            '[Escalier LSP] the server failed:',
            expect.any(Error),
        );
        expect(stderr).toMatch(/^\n\w*Error/);
    } finally {
        error.mockRestore();
    }
});
