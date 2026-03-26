import { FileChangeType } from 'vscode-languageserver-protocol';
import ReactDOM from 'react-dom/client';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { BrowserFS } from './fs/browser-fs';
import type { FSEvent } from './fs/fs-events';
import { createVolume } from './fs/volume';
import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';

import './user-worker'; // sets up the monaco editor worker

function fsEventToFileChangeType(event: FSEvent): FileChangeType {
    switch (event.type) {
        case 'create':
            return FileChangeType.Created;
        case 'delete':
            return FileChangeType.Deleted;
        case 'rename':
            return FileChangeType.Deleted; // rename emits delete for old + create for new
    }
}

async function main() {
    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());
    const baseUrl = import.meta.env.BASE_URL;

    const manifest: string[] = await fetch(
        `${baseUrl}types/manifest.json`,
    ).then((res) => res.json());

    const fs = new BrowserFS(createVolume(manifest, baseUrl));

    // Create a new client for the language server and
    // initialize it with the process ID and root URI.
    const client = new Client(wasmBuffer, '/', fs);
    client.run();
    const initResponse = await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });
    console.log('initialize response', initResponse);

    // Forward filesystem change events to the LSP server
    fs.events.on((event) => {
        const changes = [
            {
                uri: `file://${event.path}`,
                type: fsEventToFileChangeType(event),
            },
        ];
        // For renames, also emit a Created event for the new path
        if (event.type === 'rename' && event.oldPath) {
            changes[0] = {
                uri: `file://${event.oldPath}`,
                type: FileChangeType.Deleted,
            };
            changes.push({
                uri: `file://${event.path}`,
                type: FileChangeType.Created,
            });
        }
        client.workspaceDidChangeWatchedFiles({ changes });
    });

    setupLanguage(client);

    const root = document.getElementById('root');

    if (!root) {
        throw new Error('Root element not found');
    }

    ReactDOM.createRoot(root).render(<Playground />);
}

main().then(() => {
    console.log('App loaded');
});
