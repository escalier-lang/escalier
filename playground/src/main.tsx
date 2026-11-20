import ReactDOM from 'react-dom/client';
import {
    FileChangeType,
    type FileEvent,
    type InitializeResult,
} from 'vscode-languageserver-protocol';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { capturePermalink } from './capture-permalink';
import type { LoadingStatus } from './components/loading-screen';
import { showServerCrashes } from './crash-notification';
import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import type { Manifest } from './fs/volume';
import { openInitialProject, reloadOnPastedPermalink } from './initial-project';
import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';
import { forgetRemovedFiles } from './project-errors-store';
import { downloadStartupFiles, runStartup } from './startup';
import { localStorageIfAvailable, prefersLightQuery, syncTheme } from './theme';

import './theme.css';

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

    // Create a new client for the language server and
    // initialize it with the process ID and root URI.
    const client = new Client(wasm, '/', fs, {
        // The interop loader walks for `escalier.toml`+`internal/interop/data`
        // to find its built-in override files; that walk can't succeed in
        // the playground's virtual filesystem, so point it at the path
        // where copy-files.js mirrors the data dir (see volume.ts).
        ESCALIER_BUILTINS_DIR: '/node_modules/escalier-builtins',
        // The same directory holds the standard library's `.esc` files, which the
        // type checker resolves `std:prelude` and the ambient builtins from.
        ESCALIER_STDLIB_DIR: '/node_modules/escalier-builtins',
        // The dev server, which the e2e tests run against, lets a marker comment crash
        // the language server so the tests can exercise recovery. See runTestHooks in
        // cmd/lsp-server/recover.go.
        ...(import.meta.env.DEV ? { ESCALIER_LSP_TEST_HOOKS: '1' } : {}),
    });
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
    client.run();
    const initResponse: InitializeResult = await client.initialize({
        processId: process.pid,
        rootUri: 'file:///',
        capabilities: {},
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
        client.workspaceDidChangeWatchedFiles({ changes });
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

main();
