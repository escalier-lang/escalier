import ReactDOM from 'react-dom/client';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { BrowserFS } from './fs/browser-fs';
import type { Volume } from './fs/volume';
import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';

import './user-worker'; // sets up the monaco editor worker

async function main() {
    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());
    const libES5Text = await fetch('/types/lib.es5.d.ts').then((res) =>
        res.bytes(),
    );

    const vol: Volume = {
        '/node_modules/typescript/lib/lib.es5.d.ts': libES5Text,
    };
    const fs = new BrowserFS(vol);

    // Create a new client for the language server and
    // initialize it with the process ID and root URI.
    const client = new Client(wasmBuffer, fs);
    client.run();
    const initResponse = await client.initialize({
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    });
    console.log('initialize response', initResponse);

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
