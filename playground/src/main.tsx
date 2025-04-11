import ReactDOM from 'react-dom/client';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';
import './user-worker';

async function main() {
    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());

    // Create a new client for the language server and
    // initialize it with the process ID and root URI.
    const client = new Client(wasmBuffer);
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
