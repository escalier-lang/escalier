import * as fs from 'node:fs';
import * as path from 'node:path';

import type * as lsp from 'vscode-languageserver-protocol';

import { Client } from './client';

async function main() {
    const buffer = fs.readFileSync(
        path.join(__dirname, '../../../bin/lsp-server.wasm'),
    );
    const client = new Client(buffer);

    client.run();

    client.on(
        'textDocument/publishDiagnostics',
        (params: lsp.PublishDiagnosticsParams) => {
            console.log('Received diagnostics');
            console.log(JSON.stringify(params, null, 4));
        },
    );

    const initParams: lsp.InitializeParams = {
        processId: process.pid,
        rootUri: 'file:///home/user/project',
        capabilities: {},
    };
    const initResponse = await client.sendRequest('initialize', initParams);
    console.log('Initial response');
    console.log(JSON.stringify(initResponse, null, 4));

    const didChangeParams: lsp.DidChangeTextDocumentParams = {
        textDocument: {
            uri: 'file:///home/user/project/foo.esc',
            version: 2,
        },
        contentChanges: [{ text: 'console.log("Hello, world!")\nval x =\n' }],
    };
    await client.sendRequest('textDocument/didChange', didChangeParams);

    await client.stop();
}

main();
