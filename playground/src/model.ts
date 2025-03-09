import * as monaco from 'monaco-editor-core';

import { languageID } from './language';

export function createModel(code: string) {
    const model = monaco.editor.createModel(code, languageID);

    model.onDidChangeContent(() => {
        console.log(model?.getValue());

        const markers = [
            {
                severity: monaco.MarkerSeverity.Error,
                startLineNumber: 2,
                startColumn: 7,
                endLineNumber: 2,
                endColumn: 14,
                message: 'this is an error',
            },
        ];

        monaco.editor.setModelMarkers(model, languageID, markers);
    });

    return model;
}
