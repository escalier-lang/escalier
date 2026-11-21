import { type MessageEndpoint, remoteFS } from './fs-bridge';
import { OutputStream, ServerRuntime, SimpleStream } from './server-runtime';

/** Starts the server in the worker. The page sends it once, first. */
export type StartMessage = {
    kind: 'start';
    module: WebAssembly.Module;
    cwd: string;
    env: Record<string, string>;
};

/** Bytes for the server's stdin. */
export type StdinMessage = { kind: 'stdin'; chunk: Uint8Array };

/** A message the page sends the worker, besides the replies to its FSRequests. */
export type ToWorkerMessage = StartMessage | StdinMessage;

/** Bytes the server wrote to stdout. */
export type StdoutMessage = { kind: 'stdout'; chunk: Uint8Array };

/** Text the server wrote to stderr. */
export type StderrMessage = { kind: 'stderr'; text: string };

/** Says the server has exited or trapped, or failed to start. */
export type ExitedMessage = { kind: 'exited' };

/** A message the worker sends the page, besides its FSRequests. */
export type FromWorkerMessage = StdoutMessage | StderrMessage | ExitedMessage;

/**
 * Runs the language server in the worker whose global scope is endpoint, once the page
 * sends a StartMessage. The server's files are read and written on the page through
 * remoteFS. Its stdin, stdout and stderr cross as StdinMessage, StdoutMessage and
 * StderrMessage, and an ExitedMessage follows its exit.
 */
export function runServerInWorker(endpoint: MessageEndpoint) {
    const send = (message: FromWorkerMessage, transfer: Transferable[] = []) =>
        endpoint.postMessage(message, transfer);
    const stdin = new SimpleStream();
    const stdout = new OutputStream();
    stdout.on('data', (chunk) =>
        send({ kind: 'stdout', chunk }, [chunk.buffer]),
    );
    const fs = remoteFS(endpoint);

    endpoint.addEventListener('message', (event: MessageEvent) => {
        const message = event.data as ToWorkerMessage | undefined;
        switch (message?.kind) {
            // A message with no kind is not one of the page's. An FSReply has its own
            // kind, matches no case here, and is left to remoteFS's listener.
            case undefined:
                break;
            case 'stdin':
                stdin.write(message.chunk);
                break;
            case 'start': {
                const runtime = new ServerRuntime(message.module, {
                    cwd: message.cwd,
                    env: message.env,
                    fs,
                    stdin,
                    stdout,
                    onStderr: (text) => send({ kind: 'stderr', text }),
                });
                void runtime.runToEnd().then(() => send({ kind: 'exited' }));
                break;
            }
        }
    });
}
