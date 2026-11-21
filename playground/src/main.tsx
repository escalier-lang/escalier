import ReactDOM from 'react-dom/client';
import {
    FileChangeType,
    type FileEvent,
    type InitializeResult,
} from 'vscode-languageserver-protocol';

import wasmUrl from '../../bin/lsp-server.wasm?url';
import { showServerCrashes } from './bug-report/crash-notification';
import type { LoadingStatus } from './components/loading-screen';
import { monitorChecks } from './diagnostics/check-monitor';
import { forgetRemovedFiles } from './diagnostics/project-diagnostics-store';
import { useEditorStore } from './editor/editor-store';
import { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { setupLanguage } from './language/language';
import { Client } from './lsp-client/client';
import { workerLauncher } from './lsp-client/worker-launcher';
import { Playground } from './playground';
import { capturePermalink } from './project/capture-permalink';
import {
    openInitialProject,
    reloadOnPastedPermalink,
} from './project/initial-project';
import { downloadStartupFiles, runStartup } from './startup';
import {
    localStorageIfAvailable,
    prefersLightQuery,
    syncTheme,
} from './theme/theme';

import './theme/theme.css';

import './user-worker'; // sets up the monaco editor worker

async function start(
    reactRoot: ReactDOM.Root,
    showStatus: (status: LoadingStatus) => void,
) {
    const baseUrl = import.meta.env.BASE_URL;

    const manifest: Manifest = await fetch(
        `${baseUrl}types/manifest.json`,
    ).then((res) => res.json());

    // Download the language server and the whole standard library up front. The
    // filesystem fetches a file it doesn't hold with a synchronous request when the
    // language server first reads it, which blocks the page for a round trip per file.
    const { wasm, volume } = await downloadStartupFiles(
        manifest,
        baseUrl,
        wasmUrl,
        showStatus,
    );

    // BrowserFS copies each file's content out of the volume when it's constructed,
    // so the volume must hold the downloads first.
    const fs = new BrowserFS(volume);

    // Load the initial project into the filesystem BEFORE initializing the
    // LSP server. The LSP scans the workspace during initialize — if no
    // project files exist yet (escalier.toml, package.json, *.esc), it won't
    // find a valid project and diagnostics will never be published.
    const layoutRestored = await openInitialProject(manifest, baseUrl, fs);
    reloadOnPastedPermalink(window);

    // The language server runs in a worker, so a long check leaves the page responsive.
    // It reads and writes this thread's filesystem by sending the page messages.
    const launcher = workerLauncher(
        {
            wasm,
            cwd: '/',
            env: {
                // The interop loader looks for `escalier.toml` beside
                // `internal/interop/data` to find its built-in override files. That
                // search fails in the playground's virtual filesystem, so this points
                // it at the path where copy-files.js mirrors the data directory (see
                // volume.ts).
                ESCALIER_BUILTINS_DIR: '/node_modules/escalier-builtins',
                // The same directory holds the standard library's `.esc` files, which the
                // type checker resolves `std:prelude` and the ambient builtins from.
                ESCALIER_STDLIB_DIR: '/node_modules/escalier-builtins',
                // The dev server, which the e2e tests run against, lets marker comments
                // crash or slow down the language server, so the tests can exercise
                // recovery and a slow check. See runTestHooks in
                // cmd/lsp-server/recover.go.
                ...(import.meta.env.DEV
                    ? { ESCALIER_LSP_TEST_HOOKS: '1' }
                    : {}),
            },
        },
        fs,
    );
    const client = new Client(launcher);
    // A crash report links to the project as it was when the server crashed, which
    // holds the code that crashed it. capturePermalink reads the project before its
    // first await, so the link shows the project as the event found it.
    showServerCrashes(
        client,
        navigator.userAgent,
        () => capturePermalink(fs, window.location.href),
        (notification) =>
            useEditorStore
                .getState()
                .dispatch({ type: 'showNotification', notification }),
    );
    // The toolbar shows how long checks take.
    monitorChecks(client);
    void client.run();
    const initResponse: InitializeResult = await client.initialize({
        // LSP's value for a client with no process for the server to watch. The page
        // has no process ID, and the server doesn't read this field.
        processId: null,
        workspaceFolders: [{ uri: 'file:///', name: 'playground' }],
        // provideCompletionItems maps a snippet item to Monaco's InsertAsSnippet rule,
        // so the server may send items such as `throws ${1:_}`.
        capabilities: {
            textDocument: {
                completion: { completionItem: { snippetSupport: true } },
            },
        },
    });
    console.log('initialize response', initResponse);

    // Forward filesystem change events to the LSP server.
    fs.events.on((event) => {
        let changes: FileEvent[];
        switch (event.type) {
            case 'rename':
                // LSP's didChangeWatchedFiles has no "rename" type — only Created,
                // Changed, and Deleted — so we translate a rename into a Deleted
                // event for the old path plus a Created event for the new path.
                changes = [
                    {
                        uri: `file://${event.oldPath}`,
                        type: FileChangeType.Deleted,
                    },
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Created,
                    },
                ];
                break;
            case 'create':
                changes = [
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Created,
                    },
                ];
                break;
            case 'change':
                changes = [
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Changed,
                    },
                ];
                break;
            case 'delete':
                changes = [
                    {
                        uri: `file://${event.path}`,
                        type: FileChangeType.Deleted,
                    },
                ];
                break;
        }
        void client.workspaceDidChangeWatchedFiles({ changes });
    });

    setupLanguage(client, initResponse.capabilities);
    forgetRemovedFiles(fs.events);

    reactRoot.render(
        <Playground
            fs={fs}
            manifest={manifest}
            baseUrl={baseUrl}
            layoutRestored={layoutRestored}
        />,
    );
}

async function main() {
    const root = document.getElementById('root');

    if (!root) {
        throw new Error('Root element not found');
    }

    syncTheme(
        document.documentElement,
        localStorageIfAvailable(window),
        window.matchMedia(prefersLightQuery),
    );

    const reactRoot = ReactDOM.createRoot(root);
    await runStartup(
        (node) => reactRoot.render(node),
        (showStatus) => start(reactRoot, showStatus),
    );
}

void main();
