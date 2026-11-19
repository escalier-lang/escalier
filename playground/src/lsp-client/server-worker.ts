// The entry point of the worker that runs the language server. See workerLauncher.
import { runServerInWorker } from './worker-protocol';

runServerInWorker(self as unknown as Parameters<typeof runServerInWorker>[0]);
