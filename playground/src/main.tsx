import ReactDOM from 'react-dom/client';
import { FileChangeType, type FileEvent } from 'vscode-languageserver-protocol';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { useEditorStore } from './editor-store';
import { BrowserFS } from './fs/browser-fs';
import { type Manifest, createVolume } from './fs/volume';
import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';
import { loadProject } from './project-loader';

import './user-worker'; // sets up the monaco editor worker

async function main() {
    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());
    const baseUrl = import.meta.env.BASE_URL;

    const manifest: Manifest = await fetch(
        `${baseUrl}types/manifest.json`,
    ).then((res) => res.json());

    const fs = new BrowserFS(createVolume(manifest, baseUrl));

    // Load the initial project into the filesystem BEFORE initializing the
    // LSP server. The LSP scans the workspace during initialize — if no
    // project files exist yet (escalier.toml, package.json, *.esc), it won't
    // find a valid project and diagnostics will never be published.
    const params = new URLSearchParams(window.location.search);
    const exampleParam = params.get('example');

    let slug = 'hello-world';
    const kind: 'example' | 'template' = 'example';

    if (exampleParam && manifest.examples[exampleParam]) {
        slug = exampleParam;
    } else if (exampleParam) {
        console.warn(`Unknown example "${exampleParam}", falling back to hello-world`);
        useEditorStore.getState().dispatch({
            type: 'showNotification',
            notification: {
                message: `Unknown example "${exampleParam}". Loading Hello World instead.`,
                type: 'warning',
            },
        });
    }

    try {
        const primaryFile = await loadProject(
            slug,
            kind,
            manifest,
            baseUrl,
            fs,
        );
        useEditorStore.getState().dispatch({
            type: 'resetTabs',
            primaryFile,
        });
    } catch (err) {
        console.error('Failed to load initial project:', err);
    }

    // Create a new client for the language server and
    // initialize it with the process ID and root URI.
    const client = new Client(wasmBuffer, '/', fs);
    client.run();
    const initResponse = await client.initialize({
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

    setupLanguage(client);

    const root = document.getElementById('root');

    if (!root) {
        throw new Error('Root element not found');
    }

    ReactDOM.createRoot(root).render(
        <Playground
            fs={fs}
            manifest={manifest}
            baseUrl={baseUrl}
        />,
    );
}

main().then(() => {
    console.log('App loaded');
});
