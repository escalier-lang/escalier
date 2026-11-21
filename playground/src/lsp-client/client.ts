import EventEmitter from 'eventemitter3';
import type * as lsp from 'vscode-languageserver-protocol';
import {
    Disposable,
    type MessageConnection,
    type RAL,
    ReadableStreamMessageReader,
    WriteableStreamMessageWriter,
    createMessageConnection,
} from 'vscode-languageserver-protocol/browser';

import type { ServerLauncher, ServerProcess } from './server-process';
import {
    OutputStream,
    STDERR_TAIL_CHARS,
    SimpleStream,
    describeError,
} from './server-runtime';

const encoder = new TextEncoder();

/**
 * Returns a Node `Buffer` view of chunk where `Buffer` exists, and chunk itself
 * elsewhere. vscode-jsonrpc's Node runtime, which a Node test run loads, slices
 * the bytes it reads with `Buffer.from(chunk, 0, length)`. Node ignores those
 * bounds for a plain `Uint8Array`, so a chunk holding more than one message
 * misreads the second. The browser runtime accepts either type.
 */
export function asNodeBuffer(chunk: Uint8Array): Uint8Array {
    if (typeof Buffer === 'undefined') {
        return chunk;
    }
    return Buffer.from(chunk.buffer, chunk.byteOffset, chunk.byteLength);
}

/** Subscribes to an event the in-memory streams never fire. */
const neverFires = () => Disposable.create(() => {});

/**
 * Returns stream as the readable stream a jsonrpc message reader takes. The reader
 * splits the bytes the server writes to stdout into messages.
 */
function readableOf(stream: OutputStream): RAL.ReadableStream {
    return {
        onData: (listener) =>
            stream.on('data', (chunk) => listener(asNodeBuffer(chunk))),
        onClose: neverFires,
        onError: neverFires,
        onEnd: neverFires,
    };
}

/**
 * Returns stream as the writable stream a jsonrpc message writer takes. The writer
 * frames each message it writes, and the server reads them from stdin.
 */
function writableOf(stream: SimpleStream): RAL.WritableStream {
    return {
        onClose: neverFires,
        onError: neverFires,
        onEnd: neverFires,
        write(data: Uint8Array | string) {
            stream.write(
                typeof data === 'string' ? encoder.encode(data) : data,
            );
            return Promise.resolve();
        },
        end() {},
    };
}

/**
 * How long a server has to run before its crash restarts the next one at once. Crashes
 * that come sooner, one after another, wait longer each time.
 */
export const QUICK_CRASH_MS = 10_000;

/**
 * How long a server that replaced a crashed one has to run before its crash no longer
 * counts as a crash on the same input as the one before. A new server loads the standard
 * library before its first check, which can take tens of seconds on a slow machine, so
 * this is well past QUICK_CRASH_MS.
 */
export const SAME_INPUT_CRASH_MS = 60_000;

/** The longest the client waits before it starts a server again after a crash. */
export const MAX_RESTART_DELAY_MS = 30_000;

/** The facts about a crash that decide what the client does next. */
export type CrashFacts = {
    /** Whether the crashed server was started to replace one that crashed before it. */
    wasRestart: boolean;
    /** Whether the open documents changed while the crashed server ran. */
    inputChanged: boolean;
    /** How long the crashed server ran, in milliseconds. */
    ranForMs: number;
    /** How many crashes in a row, before this one, came within QUICK_CRASH_MS of a start. */
    quickCrashes: number;
};

/** What the client does after a crash. */
export type RestartDecision =
    | { kind: 'restart'; delayMs: number }
    | { kind: 'wait-for-edit' };

/**
 * Returns what the client does after a crash.
 * - A server that replaced a crashed one, and crashed within SAME_INPUT_CRASH_MS before
 *   any edit reached it, crashed on the input that killed the server before it. Another
 *   would crash the same way, so the client waits for an edit.
 * - Any other server that ran for QUICK_CRASH_MS or longer is started again at once. That
 *   includes a replacement that ran past SAME_INPUT_CRASH_MS, whose crash came from
 *   something other than the documents it opened, such as a request or running out of
 *   memory.
 * - A quicker crash is started again after a delay that doubles with each such crash in
 *   a row, up to MAX_RESTART_DELAY_MS.
 */
export function decideRestart(facts: CrashFacts): RestartDecision {
    if (
        facts.wasRestart &&
        !facts.inputChanged &&
        facts.ranForMs < SAME_INPUT_CRASH_MS
    ) {
        return { kind: 'wait-for-edit' };
    }
    if (facts.ranForMs >= QUICK_CRASH_MS) {
        return { kind: 'restart', delayMs: 0 };
    }
    return {
        kind: 'restart',
        delayMs: Math.min(1000 * 2 ** facts.quickCrashes, MAX_RESTART_DELAY_MS),
    };
}

/**
 * What the client reports when the server stops without being asked to.
 * - `restarted`: a new server is running and has the open documents.
 * - `waiting-for-edit`: the server crashed again on the same documents, and the client
 *   starts a new one at the next edit.
 *
 * `report` is what the crashed server printed about the crash, for a bug report. See
 * crashReportFrom.
 */
export type ServerCrashEvent = {
    kind: 'restarted' | 'waiting-for-edit';
    report: string;
};

/**
 * A panic the server recovered from, as its `escalier/panicReport` notification sends it.
 * The server keeps running after one.
 */
export type PanicReport = { message: string; stack: string };

/** How much of the server's stderr a crash report holds when no panic header is found. */
const CRASH_REPORT_FALLBACK_CHARS = 4 * 1024;

/**
 * Returns the crash report in stderr, the tail of what a server printed before it exited.
 * The report runs from the last line that starts `panic: ` or `fatal error: `, which is
 * where the Go runtime begins what it prints when a panic ends the program. Without such
 * a line, the report is the last few kilobytes.
 */
export function crashReportFrom(stderr: string): string {
    const headers = [...stderr.matchAll(/^(?:panic|fatal error): /gm)];
    const start = headers.at(-1)?.index;
    const report =
        start === undefined
            ? stderr.slice(-CRASH_REPORT_FALLBACK_CHARS)
            : stderr.slice(start);
    return (
        report.trim() || 'The language server exited without printing an error.'
    );
}

/** The message a request rejects with while the client waits for an edit to restart. */
export const SERVER_DOWN_MESSAGE =
    'the language server crashed and restarts at the next edit';

/**
 * The params of the server's `escalier/checkStarted` and `escalier/checkFinished`
 * notifications, which bracket each check. It names the document version whose change
 * started the check.
 */
export type CheckEvent = { uri: string; version: number };

/**
 * Holds requests while a server starts after a crash. Once it is known whether a server
 * will answer, the held requests either go ahead or fail.
 */
export class RequestGate {
    private gate: Promise<void> = Promise.resolve();
    private settle: ((err?: Error) => void) | undefined;

    /** Returns a promise that settles when requests may go, or rejects when they fail. */
    wait(): Promise<void> {
        return this.gate;
    }

    /** Makes later calls to wait() pend. A gate that is already closed stays as it is. */
    close() {
        if (this.settle) {
            return;
        }
        let resolve!: () => void;
        let reject!: (err: Error) => void;
        this.gate = new Promise<void>((res, rej) => {
            resolve = res;
            reject = rej;
        });
        // A gate can fail with no request waiting on it.
        this.gate.catch(() => {});
        this.settle = (err) => {
            this.settle = undefined;
            this.gate = Promise.resolve();
            if (err) {
                reject(err);
            } else {
                resolve();
            }
        };
    }

    /** Lets the waiting requests, and later ones, go. */
    open() {
        this.settle?.();
    }

    /**
     * Rejects the waiting requests with err. Later calls to wait() resolve until the gate
     * is closed again.
     */
    fail(err: Error) {
        this.settle?.(err);
    }
}

/** The state of the client's server. */
type ServerState =
    /** A server is running and takes messages. A client that hasn't run() yet is here too. */
    | 'running'
    /** A server is starting after a crash. Requests wait for it. */
    | 'restarting'
    /**
     * A server that replaced a crashed one crashed before any edit reached it. Requests
     * fail until the next edit starts another.
     */
    | 'waiting-for-edit';

/**
 * The params of an `initialize` request, without the deprecated rootUri. The caller
 * names the workspace in workspaceFolders.
 */
export type InitializeParams = Omit<lsp.InitializeParams, 'rootUri'>;

export class Client {
    /** Starts each server, the first one and every one that replaces a crashed server. */
    private launcher: ServerLauncher;
    /** The running server, or undefined before the first one starts. */
    private process: ServerProcess | undefined;
    // The streams the connection writes to and reads from, which createRuntime()
    // replaces for each server. A launched server reads stdin and writes stdout.
    private stdin!: SimpleStream;
    private stdout!: OutputStream;
    /** Sends requests and notifications over stdin and reads the replies from stdout. */
    private connection!: MessageConnection;
    /** Set by `stop()`. Drops notifications from then on. */
    private stopped = false;
    /** Set when `stop()` begins, so the server's exit isn't taken for a crash. */
    private stopping = false;

    private state: ServerState = 'running';
    /** Holds requests while a server starts after a crash. */
    private requests = new RequestGate();
    /**
     * Notifications other than document changes, sent while a server starts after a crash.
     * The server gets them once it has opened the documents.
     */
    private queuedNotifications: Array<[string, unknown]> = [];
    /** The report on the last crash, which the crash event that follows it carries. */
    private crashReport = '';
    /** The answer to the `initialize` that a restart sends when it reopens. */
    private initializeResult: lsp.InitializeResult | undefined;
    /** Wakes run() while it waits for an edit, from the next edit or from stop(). */
    private wake: () => void = () => {};
    /** The params of the last `initialize`, which a restart sends again. */
    private initializeParams: InitializeParams | undefined;
    /** The text of every open document, which a restart opens again. */
    private documents = new Map<string, lsp.TextDocumentItem>();
    private crashEvents = new EventEmitter();
    /** Emits `exit` each time a server ends, other than through stop(). */
    private serverEvents = new EventEmitter();

    /**
     * Calls every listener registered for a notification's method. A
     * MessageConnection keeps one handler per method, so the client registers
     * one handler that emits here.
     */
    private notifications = new EventEmitter();

    /**
     * Creates a client whose servers launcher starts. The first one starts in run(), and
     * messages sent before then wait for it.
     */
    constructor(launcher: ServerLauncher) {
        this.launcher = launcher;
        this.createRuntime();
    }

    /**
     * Creates the streams and the connection for a server, either the first one or one
     * that replaces a crashed server.
     */
    private createRuntime() {
        this.stdin = new SimpleStream();
        this.stdout = new OutputStream();
        this.connection = createMessageConnection(
            new ReadableStreamMessageReader(readableOf(this.stdout)),
            new WriteableStreamMessageWriter(writableOf(this.stdin)),
        );
        // A MessageConnection keeps its handlers, so the new one needs every method a
        // listener was registered for.
        for (const method of this.notifications.eventNames()) {
            this.connection.onNotification(String(method), (params) =>
                this.notifications.emit(method, params),
            );
        }
        this.connection.listen();
    }

    /**
     * Starts the server and keeps one running until stop(). Resolves once stop() has
     * ended the last server.
     */
    async run() {
        let quickCrashes = 0;
        for (let restart = false; ; restart = true) {
            const startedAt = Date.now();
            const input = this.documentsKey();
            let stderr: string;
            try {
                stderr = await this.runServer(restart);
            } catch (err) {
                // The server couldn't be started, such as when its WebAssembly doesn't
                // compile. That is a crash like any other.
                console.error('[Escalier LSP] the server failed:', err);
                stderr = `\n${describeError(err)}\n`;
            }
            if (this.stopping) {
                return;
            }
            this.serverEvents.emit('exit');
            this.crashReport = crashReportFrom(
                stderr.slice(-STDERR_TAIL_CHARS),
            );

            const decision = decideRestart({
                wasRestart: restart,
                inputChanged: this.documentsKey() !== input,
                ranForMs: Date.now() - startedAt,
                quickCrashes,
            });
            this.beginRestart();
            if (decision.kind === 'wait-for-edit') {
                this.state = 'waiting-for-edit';
                this.queuedNotifications = [];
                this.requests.fail(new Error(SERVER_DOWN_MESSAGE));
                this.crashEvents.emit('crash', {
                    kind: 'waiting-for-edit',
                    report: this.crashReport,
                });
                await new Promise<void>((resolve) => {
                    this.wake = resolve;
                });
                this.requests.close();
                quickCrashes = 0;
            } else {
                quickCrashes = decision.delayMs === 0 ? 0 : quickCrashes + 1;
                await new Promise((resolve) =>
                    setTimeout(resolve, decision.delayMs),
                );
            }
            if (this.stopping) {
                return;
            }
            this.state = 'restarting';
            this.createRuntime();
        }
    }

    /**
     * Launches a server and waits for it to end, then returns the end of what it wrote to
     * stderr. When restart is true, the server replaces a crashed one, so it is sent the
     * last `initialize` and opens every document, and the client reports `restarted`
     * once it has them.
     */
    private async runServer(restart: boolean): Promise<string> {
        // Messages the client sent before the server started wait on stdin for it to
        // read.
        this.process = await this.launcher.launch(this.stdin, this.stdout);
        if (this.stopping) {
            // stop() ran while the server started, so its dispose() found no server.
            this.process.dispose();
        }
        const exited = this.process.exited;
        if (restart) {
            const reopened = this.reopen().then(
                () => 'reopened' as const,
                // The server crashed before it answered, and the exit below reports it.
                () => 'exited' as const,
            );
            const first = await Promise.race([
                reopened,
                exited.then(() => 'exited' as const),
            ]);
            if (first === 'reopened') {
                this.state = 'running';
                this.requests.open();
                this.crashEvents.emit('crash', {
                    kind: 'restarted',
                    report: this.crashReport,
                });
            }
        }
        return exited;
    }

    /**
     * Sends the new server the last `initialize`, opens every open document, and then
     * sends the notifications queued while it started.
     */
    private async reopen() {
        if (this.initializeParams) {
            this.initializeResult = await this.connection.sendRequest(
                'initialize',
                this.initializeParams,
            );
        }
        // An edit can land while a didOpen is being written. Each pass opens the documents
        // that changed since the pass before and closes the ones that were closed, until a
        // pass finds nothing to send. The caller marks the server running in the same
        // microtask chain as the last pass, so no edit lands in between.
        const sent = new Map<string, lsp.TextDocumentItem>();
        for (;;) {
            const opened = [...this.documents.values()].filter(
                (doc) => sent.get(doc.uri) !== doc,
            );
            const closed = [...sent.keys()].filter(
                (uri) => !this.documents.has(uri),
            );
            if (opened.length === 0 && closed.length === 0) {
                break;
            }
            for (const uri of closed) {
                sent.delete(uri);
                await this.connection.sendNotification(
                    'textDocument/didClose',
                    { textDocument: { uri } },
                );
            }
            for (const textDocument of opened) {
                sent.set(textDocument.uri, textDocument);
                await this.connection.sendNotification('textDocument/didOpen', {
                    textDocument,
                });
            }
        }
        for (
            let next = this.queuedNotifications.shift();
            next;
            next = this.queuedNotifications.shift()
        ) {
            await this.connection.sendNotification(...next);
        }
    }

    /**
     * Makes requests wait for the next server. A request pending on the crashed server's
     * connection rejects, since nothing will answer it.
     */
    private beginRestart() {
        this.state = 'restarting';
        this.initializeResult = undefined;
        this.requests.close();
        this.connection.dispose();
    }

    /** Returns a key that changes whenever an open document's text does. */
    private documentsKey(): string {
        return JSON.stringify(
            [...this.documents.values()].map((doc) => [doc.uri, doc.text]),
        );
    }

    /**
     * Calls callback each time a server ends, before the client starts the next one. Any
     * check the server was running ends with it, and no `escalier/checkFinished` follows.
     */
    onServerExit(callback: () => void) {
        this.serverEvents.on('exit', callback);
    }

    /**
     * Calls callback when the server stops without being asked to, once the client has
     * decided what to do about it.
     */
    onServerCrash(callback: (event: ServerCrashEvent) => void) {
        this.crashEvents.on('crash', callback);
    }

    //
    // Lifecycle methods
    //

    /** Sends the server `initialize` with params. */
    async initialize(params: InitializeParams): Promise<lsp.InitializeResult> {
        this.initializeParams = params;
        if (this.state !== 'running') {
            // The server starting after a crash is sent these params when it reopens, and
            // a second `initialize` would break its lifecycle. The reopen's answer serves,
            // unless the reopen began before the params were recorded.
            await this.requests.wait();
            if (this.initializeResult) {
                return this.initializeResult;
            }
        }
        return this.sendRequest('initialize', params);
    }

    async setTrace(params: lsp.SetTraceParams) {
        return this.sendRequest('$/setTrace', params);
    }

    async shutdown() {
        return this.sendRequest('shutdown', null);
    }

    async stop() {
        this.stopping = true;
        // run() may be waiting for an edit, with no server to send `exit` to.
        this.wake();
        // A server that is stopping may never answer `shutdown`, or may reject it
        // before `initialize`, so stop() sends it without waiting on the reply.
        this.shutdown().catch(() => {});
        await this.exit();
        // Disposing ends a server in a worker at once. A server in this thread keeps
        // running past this point. `exit` is a notification, so nothing waits for the
        // server to act on it. The callbacks that drain its writes run on timers that
        // outlive this call. Disposing silences it, because whatever it prints from
        // here on describes a server nobody reads. Under Vitest that output reaches
        // the main process as an `onUserConsoleLog` message. One that arrives after
        // the worker has begun closing its rpc channel fails the whole run with an
        // EnvironmentTeardownError, while every test passes.
        this.process?.dispose();
        // `stopped` makes later notifications no-ops. Editor teardown can still send
        // didChange or didClose after stop(), and the disposed connection would reject
        // them with no one to catch it.
        this.stopped = true;
        this.connection.dispose();
    }

    async exit() {
        if (this.stopped) {
            return;
        }
        // Sent whatever the state, so a server that is starting after a crash still ends.
        // Between a crash and the next server the connection is disposed and throws, and
        // there is no server to end.
        try {
            await this.connection.sendNotification('exit', null);
        } catch {}
    }

    //
    // Document Synchronization methods
    //

    textDocumentDidOpen(params: lsp.DidOpenTextDocumentParams) {
        this.documents.set(params.textDocument.uri, params.textDocument);
        return this.sendDocumentNotification('textDocument/didOpen', params);
    }

    textDocumentDidChange(params: lsp.DidChangeTextDocumentParams) {
        const doc = this.documents.get(params.textDocument.uri);
        const last = params.contentChanges.at(-1);
        // The server takes whole-document changes only, so the last change holds the text.
        if (doc && last && !('range' in last)) {
            this.documents.set(doc.uri, {
                ...doc,
                version: params.textDocument.version,
                text: last.text,
            });
        }
        return this.sendDocumentNotification('textDocument/didChange', params);
    }

    textDocumentWillSave(params: lsp.WillSaveTextDocumentParams) {
        return this.sendNotification('textDocument/willSave', params);
    }

    // textDocumentWillSaveWaitUntil

    textDocumentDidSave(params: lsp.DidSaveTextDocumentParams) {
        return this.sendNotification('textDocument/didSave', params);
    }

    textDocumentDidClose(params: lsp.DidCloseTextDocumentParams) {
        this.documents.delete(params.textDocument.uri);
        return this.sendDocumentNotification('textDocument/didClose', params);
    }

    workspaceDidChangeWatchedFiles(params: lsp.DidChangeWatchedFilesParams) {
        return this.sendNotification('workspace/didChangeWatchedFiles', params);
    }

    //
    // Language Features
    //

    textDocumentDeclaration(
        params: lsp.DeclarationParams,
    ): Promise<lsp.Location | lsp.Location[] | lsp.LocationLink[] | null> {
        return this.sendRequest('textDocument/declaration', params);
    }

    textDocumentDefinition(
        params: lsp.DefinitionParams,
    ): Promise<lsp.Location | lsp.Location[] | lsp.LocationLink[] | null> {
        return this.sendRequest('textDocument/definition', params);
    }

    textDocumentCodeAction(
        params: lsp.CodeActionParams,
    ): Promise<lsp.Command[] | lsp.CodeAction[]> {
        return this.sendRequest('textDocument/codeAction', params);
    }

    textDocumentHover(params: lsp.HoverParams): Promise<lsp.Hover | null> {
        return this.sendRequest('textDocument/hover', params);
    }

    textDocumentSemanticTokensFull(
        params: lsp.SemanticTokensParams,
    ): Promise<lsp.SemanticTokens | null> {
        return this.sendRequest('textDocument/semanticTokens/full', params);
    }

    textDocumentCompletion(
        params: lsp.CompletionParams,
    ): Promise<lsp.CompletionList | lsp.CompletionItem[] | null> {
        return this.sendRequest('textDocument/completion', params);
    }

    completionItemResolve(
        params: lsp.CompletionItem,
    ): Promise<lsp.CompletionItem> {
        return this.sendRequest('completionItem/resolve', params);
    }

    textDocumentSignatureHelp(
        params: lsp.SignatureHelpParams,
    ): Promise<lsp.SignatureHelp | null> {
        return this.sendRequest('textDocument/signatureHelp', params);
    }

    // Go to type definition

    // Go to implementation

    workspaceExecuteCommand(
        params: lsp.ExecuteCommandParams,
    ): Promise<lsp.LSPAny> {
        return this.sendRequest('workspace/executeCommand', params);
    }

    /**
     * Sends a notification, or does nothing once stop() has run, since the
     * connection it would write to is gone. Callers don't wait on notifications.
     */
    private sendNotification(method: string, params: unknown): Promise<void> {
        if (this.stopped) {
            return Promise.resolve();
        }
        switch (this.state) {
            case 'running':
                return this.connection.sendNotification(method, params);
            case 'restarting':
                this.queuedNotifications.push([method, params]);
                return Promise.resolve();
            case 'waiting-for-edit':
                // The next server rescans the workspace when it initializes, so it learns
                // about the files this notification describes.
                return Promise.resolve();
        }
    }

    /**
     * Sends a notification that opens, changes or closes a document, after the caller has
     * recorded the change in `documents`. While a server is starting after a crash, the
     * notification is dropped, since the server opens every document from `documents`
     * once it is up. An edit while the client waits for one starts the new server.
     */
    private sendDocumentNotification(
        method: string,
        params: unknown,
    ): Promise<void> {
        switch (this.state) {
            case 'running':
                return this.sendNotification(method, params);
            case 'waiting-for-edit':
                this.wake();
                return Promise.resolve();
            case 'restarting':
                return Promise.resolve();
        }
    }

    private async sendRequest(method: string, params: unknown): Promise<any> {
        if (this.state === 'waiting-for-edit') {
            throw new Error(SERVER_DOWN_MESSAGE);
        }
        await this.requests.wait();
        return this.connection.sendRequest(method, params);
    }

    onTextDocumentPublishDiagnostics(
        callback: (params: lsp.PublishDiagnosticsParams) => void,
    ) {
        this.on('textDocument/publishDiagnostics', callback);
    }

    /** Calls callback with each panic the server recovers from. */
    onServerPanic(callback: (report: PanicReport) => void) {
        this.on('escalier/panicReport', callback);
    }

    /** Calls callback as the server starts each check. */
    onCheckStarted(callback: (event: CheckEvent) => void) {
        this.on('escalier/checkStarted', callback);
    }

    /** Calls callback as the server finishes each check that it started. */
    onCheckFinished(callback: (event: CheckEvent) => void) {
        this.on('escalier/checkFinished', callback);
    }

    onWindowLogMessage(callback: (params: lsp.LogMessageParams) => void) {
        this.on('window/logMessage', callback);
    }

    private on(method: string, callback: (params: any) => void) {
        if (this.notifications.listenerCount(method) === 0) {
            this.connection.onNotification(method, (params) =>
                this.notifications.emit(method, params),
            );
        }
        this.notifications.on(method, callback);
    }
}
