import * as lsTypes from 'vscode-languageserver-types';
import type { worker } from 'monaco-editor';

import type { ILanguageWorkerWithDiagnostics } from '../common/lsp-language-features';

export interface ICreateData {
	languageId: string;
	languageSettings: Record<string, unknown>; // TODO: define language settings
}

export class EscalierWorker implements ILanguageWorkerWithDiagnostics {
    private _ctx: worker.IWorkerContext;

    constructor(ctx: worker.IWorkerContext, createData: ICreateData) {
        console.log("createData =", createData);
        console.log("ctx =", ctx);
        this._ctx = ctx;
    }

    async doValidation(uri: string): Promise<lsTypes.Diagnostic[]> {
        console.log('doValidation, uri =', uri);
        return [];
    }
}