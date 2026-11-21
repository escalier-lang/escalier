import type { FSAPI } from '#fs/fs-api';

import { type MessageEndpoint, serveFS } from './fs-bridge';
import {
    type ServerConfig,
    type ServerLauncher,
    compileOnce,
} from './server-process';
import { StderrTail } from './server-runtime';
import type { FromWorkerMessage, ToWorkerMessage } from './worker-protocol';

/** The part of a Worker that workerLauncher uses. */
export interface WorkerLike extends MessageEndpoint {
    terminate(): void;
    /** Called when the worker's script fails to load, or throws an error nothing catches. */
    onerror: ((event: ErrorEvent) => void) | null;
    /** Called when a message from the worker can't be deserialized. */
    onmessageerror: ((event: MessageEvent) => void) | null;
}

/** Returns a new worker that runs server-worker.ts. */
export function createServerWorker(): WorkerLike {
    return new Worker(new URL('./server-worker.ts', import.meta.url), {
        type: 'module',
    });
}

/**
 * Returns a launcher that runs each server in its own worker, from createWorker. The
 * server reads and writes its files in fs, on this thread, through serveFS. Disposing a
 * server terminates its worker.
 */
export function workerLauncher(
    config: ServerConfig,
    fs: FSAPI,
    createWorker: () => WorkerLike = createServerWorker,
): ServerLauncher {
    const compile = compileOnce(config.wasm);
    return {
        async launch(stdin, stdout) {
            const module = await compile();
            const worker = createWorker();
            const send = (
                message: ToWorkerMessage,
                transfer: Transferable[] = [],
            ) => worker.postMessage(message, transfer);
            const stderr = new StderrTail();
            let resolveExited!: (stderr: string) => void;
            const exited = new Promise<string>((resolve) => {
                resolveExited = resolve;
            });

            const stopServing = serveFS(worker, fs);
            const forwardStdin = () => {
                for (let chunk = stdin.read(); chunk; chunk = stdin.read()) {
                    // A chunk can be a view into a larger buffer, which cloning would
                    // copy in full. A copy of just its bytes can be transferred. The
                    // copy is made with the constructor because a Node Buffer's slice()
                    // returns a view.
                    const copy = new Uint8Array(chunk);
                    send({ kind: 'stdin', chunk: copy }, [copy.buffer]);
                }
            };
            const stdinSubscription = stdin.on('data', forwardStdin);
            const onMessage = (event: MessageEvent) => {
                const message = event.data as FromWorkerMessage | undefined;
                switch (message?.kind) {
                    // A message with no kind is not one of the worker's. An FSRequest
                    // has its own kind, matches no case here, and is left to serveFS.
                    case undefined:
                        break;
                    case 'stdout':
                        stdout.write(message.chunk);
                        break;
                    case 'stderr':
                        stderr.record(message.text);
                        break;
                    case 'exited':
                        end();
                        break;
                }
            };
            let ended = false;
            const end = () => {
                if (ended) {
                    return;
                }
                ended = true;
                stdinSubscription.dispose();
                stopServing();
                worker.removeEventListener('message', onMessage);
                worker.onerror = null;
                worker.onmessageerror = null;
                worker.terminate();
                resolveExited(stderr.text());
            };
            worker.addEventListener('message', onMessage);
            worker.onerror = (event) => {
                console.error(
                    '[Escalier LSP] the server worker failed:',
                    event.message,
                );
                stderr.record(`\n${event.message}\n`);
                end();
            };
            // A lost message leaves the client and the server out of step, so the server
            // ends and the client starts another.
            worker.onmessageerror = () => {
                const message = 'a message from the server worker was lost';
                console.error(`[Escalier LSP] ${message}`);
                stderr.record(`\n${message}\n`);
                end();
            };

            send({
                kind: 'start',
                module,
                cwd: config.cwd,
                env: config.env ?? {},
            });
            // The client may have written messages before the server started.
            forwardStdin();
            return { exited, dispose: end };
        },
    };
}
