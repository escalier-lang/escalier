import ReactDOM from 'react-dom/client';

import wasmUrl from '../../bin/lsp-server.wasm?url';

import { BrowserFS } from './fs/browser-fs';
import type { Volume } from './fs/volume';
import { setupLanguage } from './language';
import { Client } from './lsp-client/client';
import { Playground } from './playground';

import './user-worker'; // sets up the monaco editor worker

const es2015Libs = [
    'lib.es2015.collection.d.ts',
    'lib.es2015.core.d.ts',
    'lib.es2015.d.ts',
    'lib.es2015.generator.d.ts',
    'lib.es2015.iterable.d.ts',
    'lib.es2015.promise.d.ts',
    'lib.es2015.proxy.d.ts',
    'lib.es2015.reflect.d.ts',
    'lib.es2015.symbol.d.ts',
    'lib.es2015.symbol.wellknown.d.ts',
];

async function main() {
    const wasmBuffer = await fetch(wasmUrl).then((res) => res.arrayBuffer());
    const libES5Text = await fetch(
        `${import.meta.env.BASE_URL}types/lib.es5.d.ts`,
    ).then((res) => res.bytes());
    const libDOMText = await fetch(
        `${import.meta.env.BASE_URL}types/lib.dom.d.ts`,
    ).then((res) => res.bytes());

    const es2015LibContents = await Promise.all(
        es2015Libs.map((lib) =>
            fetch(`${import.meta.env.BASE_URL}types/${lib}`).then((res) =>
                res.bytes(),
            ),
        ),
    );

    const vol: Volume = {
        '/package.json': new TextEncoder().encode(
            JSON.stringify({
                name: 'my-project',
                version: '1.0.0',
                main: 'index.js',
            }),
        ),
        // findRepoRoot looks for go.mod
        // TODO: come up with a better plan since most projects won't have a
        // go.mod file
        '/go.mod': new TextEncoder().encode(
            `module my-project

go 1.26
`,
        ),
        '/node_modules/typescript/lib/lib.es5.d.ts': libES5Text,
        '/node_modules/typescript/lib/lib.dom.d.ts': libDOMText,
    };

    for (let i = 0; i < es2015Libs.length; i++) {
        vol[`/node_modules/typescript/lib/${es2015Libs[i]}`] =
            es2015LibContents[i];
    }
    const fs = new BrowserFS(vol);

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
