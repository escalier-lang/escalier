import * as monaco from 'monaco-editor-core';
import { describe, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

// The mock supplies the Monaco enums the test names, without loading Monaco.
vi.mock('monaco-editor-core');

import {
    convertCodeActions,
    createCodeActionProvider,
    documentTextEdits,
    markerDiagnostics,
} from './code-actions';

const uri = 'file:///bin/main.esc';
const resource = { toString: () => uri } as monaco.Uri;

// The marker Monaco shows for diagnostic, which covers 1:8-3:1 in LSP terms.
const marker: monaco.editor.IMarkerData = {
    severity: monaco.MarkerSeverity.Error,
    startLineNumber: 2,
    startColumn: 9,
    endLineNumber: 4,
    endColumn: 2,
    message: 'match is not exhaustive; add a branch for `"b"`',
};
const diagnostic: lsp.Diagnostic = {
    range: {
        start: { line: 1, character: 8 },
        end: { line: 3, character: 1 },
    },
    severity: 1,
    source: 'escalier',
    message: marker.message,
    data: { kind: 'example' },
};

const fillArms: lsp.CodeAction = {
    title: 'Add missing match arms',
    kind: 'quickfix',
    isPreferred: true,
    diagnostics: [diagnostic],
    edit: {
        changes: {
            [uri]: [
                {
                    range: {
                        start: { line: 2, character: 9 },
                        end: { line: 2, character: 9 },
                    },
                    newText: ',\n\t"b" => throw "todo"',
                },
            ],
        },
    },
};

describe('markerDiagnostics', () => {
    test('returns the published diagnostic a marker matches, with its data', () => {
        expect(markerDiagnostics([marker], [diagnostic])).toEqual([diagnostic]);
    });

    test('rebuilds a diagnostic for a marker nothing published matches', () => {
        expect(markerDiagnostics([marker], [])).toEqual([
            { range: diagnostic.range, message: marker.message },
        ]);
    });
});

describe('documentTextEdits', () => {
    const edit: lsp.TextEdit = {
        range: {
            start: { line: 0, character: 0 },
            end: { line: 0, character: 0 },
        },
        newText: 'x',
    };

    test('reads the edits to the document from documentChanges', () => {
        expect(
            documentTextEdits(
                {
                    documentChanges: [
                        {
                            textDocument: { uri, version: 1 },
                            edits: [edit],
                        },
                        {
                            textDocument: {
                                uri: 'file:///lib/index.esc',
                                version: 1,
                            },
                            edits: [edit],
                        },
                        { kind: 'create', uri: 'file:///lib/new.esc' },
                    ],
                },
                uri,
            ),
        ).toEqual([edit]);
    });

    test('reads the edits to the document from changes', () => {
        expect(documentTextEdits({ changes: { [uri]: [edit] } }, uri)).toEqual([
            edit,
        ]);
    });
});

describe('convertCodeActions', () => {
    test('converts an action with edits to the document', () => {
        expect(
            convertCodeActions([fillArms], uri, resource, 7, [marker]),
        ).toEqual([
            {
                title: 'Add missing match arms',
                kind: 'quickfix',
                isPreferred: true,
                diagnostics: [marker],
                edit: {
                    edits: [
                        {
                            resource,
                            versionId: 7,
                            textEdit: {
                                range: {
                                    startLineNumber: 3,
                                    startColumn: 10,
                                    endLineNumber: 3,
                                    endColumn: 10,
                                },
                                text: ',\n\t"b" => throw "todo"',
                            },
                        },
                    ],
                },
            },
        ]);
    });

    test('leaves out commands and actions without an edit to the document', () => {
        const command: lsp.Command = { title: 'Compile', command: 'compile' };
        const compile: lsp.CodeAction = {
            title: 'Compile',
            kind: 'compile',
            command,
        };
        const elsewhere: lsp.CodeAction = {
            ...fillArms,
            edit: { changes: { 'file:///lib/index.esc': [] } },
        };
        expect(
            convertCodeActions(
                [command, compile, elsewhere],
                uri,
                resource,
                1,
                [],
            ),
        ).toEqual([]);
    });

    test('returns no actions for a null response', () => {
        expect(convertCodeActions(null, uri, resource, 1, [])).toEqual([]);
    });
});

describe('createCodeActionProvider', () => {
    const model = {
        uri: resource,
        getVersionId: () => 3,
    } as unknown as monaco.editor.ITextModel;
    const range = {
        startLineNumber: 2,
        startColumn: 9,
        endLineNumber: 2,
        endColumn: 9,
    } as monaco.Range;
    const context = {
        markers: [marker],
        trigger: monaco.languages.CodeActionTriggerType.Invoke,
        only: 'quickfix',
    } as monaco.languages.CodeActionContext;
    const token = {} as monaco.CancellationToken;

    test('flushes pending changes and sends the published diagnostics', async () => {
        const request = vi.fn().mockResolvedValue([fillArms]);
        const flushPendingChanges = vi.fn();
        const provider = createCodeActionProvider({
            request,
            flushPendingChanges,
            publishedDiagnostics: () => [diagnostic],
        });
        const result = await provider.provideCodeActions(
            model,
            range,
            context,
            token,
        );
        expect(flushPendingChanges).toHaveBeenCalledWith(uri);
        expect(request).toHaveBeenCalledWith({
            textDocument: { uri },
            range: {
                start: { line: 1, character: 8 },
                end: { line: 1, character: 8 },
            },
            context: { diagnostics: [diagnostic], only: ['quickfix'] },
        });
        expect(result?.actions.map((a) => a.title)).toEqual([
            'Add missing match arms',
        ]);
    });

    test('returns no actions when the request fails', async () => {
        const consoleError = vi
            .spyOn(console, 'error')
            .mockImplementation(() => {});
        const provider = createCodeActionProvider({
            request: vi.fn().mockRejectedValue(new Error('down')),
            flushPendingChanges: () => {},
            publishedDiagnostics: () => [],
        });
        const result = await provider.provideCodeActions(
            model,
            range,
            context,
            token,
        );
        expect(result?.actions).toEqual([]);
        consoleError.mockRestore();
    });
});
