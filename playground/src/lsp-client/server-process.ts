import type { FSAPI } from '#fs/fs-api';

import {
    type OutputStream,
    ServerRuntime,
    type SimpleStream,
} from './server-runtime';

/** A language server that a ServerLauncher started. */
export interface ServerProcess {
    /**
     * Resolves once the server has ended, with the last STDERR_TAIL_CHARS of what it
     * wrote to stderr. It never rejects.
     */
    readonly exited: Promise<string>;
    /**
     * Stops listening to the server. A server in a worker ends at once and `exited`
     * resolves. A server in this thread can't be ended from outside, so it only stops
     * printing to the console, and `exited` resolves when it exits by itself.
     */
    dispose(): void;
}

/** Starts language servers, one for each call to launch(). */
export interface ServerLauncher {
    /**
     * Starts a server that reads the bytes written to stdin and writes its replies to
     * stdout. Rejects when the server can't be started, such as when its WebAssembly
     * doesn't compile.
     */
    launch(stdin: SimpleStream, stdout: OutputStream): Promise<ServerProcess>;
}

/** What a launcher passes the server besides its streams. */
export type ServerConfig = {
    /** The compiled server's WebAssembly. */
    wasm: ArrayBuffer;
    /** The directory the server's `os.Getwd` returns. */
    cwd: string;
    /** Environment variables the server's `os.Getenv` sees. */
    env?: Record<string, string>;
};

/** Returns a function that compiles wasm on its first call and returns that module after. */
export function compileOnce(
    wasm: ArrayBuffer,
): () => Promise<WebAssembly.Module> {
    let compiled: Promise<WebAssembly.Module> | undefined;
    return () => {
        if (!compiled) {
            compiled = WebAssembly.compile(wasm);
            // A module that fails to compile is compiled again on the next call.
            compiled.catch(() => {
                compiled = undefined;
            });
        }
        return compiled;
    };
}

/**
 * Returns a launcher that runs each server in this thread, with its files served by fs.
 * Only one such server can run in a thread at a time.
 */
export function inProcessLauncher(
    config: ServerConfig,
    fs: FSAPI,
): ServerLauncher {
    const compile = compileOnce(config.wasm);
    return {
        async launch(stdin, stdout) {
            const runtime = new ServerRuntime(await compile(), {
                cwd: config.cwd,
                fs,
                env: config.env ?? {},
                stdin,
                stdout,
            });
            return {
                exited: runtime.runToEnd(),
                dispose: () => runtime.silence(),
            };
        },
    };
}
