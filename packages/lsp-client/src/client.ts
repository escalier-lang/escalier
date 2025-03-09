import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import * as path from 'node:path';

import type * as lsp from 'vscode-languageserver-protocol';
import { JSONRPCEndpoint } from 'ts-lsp-client';

import { Result, type AsyncResult } from './result';

export class Client {
    endpoint: JSONRPCEndpoint;
    process: ChildProcessWithoutNullStreams;

    constructor() {
        this.process = spawn(
            path.join(__dirname, '../../../bin/lsp-server'),
            [],
        );
        this.endpoint = new JSONRPCEndpoint(
            this.process.stdin,
            this.process.stdout,
        );
        this.endpoint.on('error', (err) => {
            console.error(err);
        });
    }

    async initialize(
        params: lsp.InitializeParams,
    ): AsyncResult<lsp.InitializeResult, Error> {
        try {
            const value = await this.endpoint.send('initialize', params);
            return Result.Ok(value);
        } catch (e) {
            return Result.Err(e);
        }
    }

    async didOpen(
        params: lsp.DidOpenTextDocumentParams,
    ): AsyncResult<void, Error> {
        try {
            const value = await this.endpoint.send(
                'textDocument/didOpen',
                params,
            );
            return Result.Ok(value);
        } catch (e) {
            return Result.Err(e);
        }
    }

    async didChange(
        params: lsp.DidChangeTextDocumentParams,
    ): AsyncResult<void, Error> {
        try {
            const value = await this.endpoint.send(
                'textDocument/didChange',
                params,
            );
            return Result.Ok(value);
        } catch (e) {
            return Result.Err(e);
        }
    }

    onPublishDiagnostics(
        callback: (params: lsp.PublishDiagnosticsParams) => void,
    ) {
        this.endpoint.addListener('textDocument/publishDiagnostics', callback);
    }

    dispose() {
        this.process.kill();
    }
}
