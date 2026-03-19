import * as monaco from 'monaco-editor-core';
import * as lsp from 'vscode-languageserver-protocol';

import type { Client } from './lsp-client/client';
import { monarchLanguage } from './monarch-language';

export const languageID = 'escalier';

function convertSeverity(
    severity?: lsp.DiagnosticSeverity,
): monaco.MarkerSeverity {
    switch (severity) {
        case lsp.DiagnosticSeverity.Error:
            return monaco.MarkerSeverity.Error;
        case lsp.DiagnosticSeverity.Warning:
            return monaco.MarkerSeverity.Warning;
        case lsp.DiagnosticSeverity.Information:
            return monaco.MarkerSeverity.Info;
        case lsp.DiagnosticSeverity.Hint:
            return monaco.MarkerSeverity.Hint;
        default:
            return monaco.MarkerSeverity.Error;
    }
}

type Loc = {
    line: number;
    column: number;
};

type Span = {
    start: Loc;
    end: Loc;
};

type ErrorMesage = {
    message: string;
    span: Span;
};

function monacoPosToLspPos(position: monaco.Position): lsp.Position {
    return {
        line: position.lineNumber - 1, // LSP uses 0-based line numbers
        character: position.column - 1, // LSP uses 0-based character indices
    };
}

function lspRangeToMonacoRange(range: lsp.Range): monaco.Range {
    return new monaco.Range(
        range.start.line + 1,
        range.start.character + 1,
        range.end.line + 1,
        range.end.character + 1,
    );
}

function lspKindToMonacoKind(
    kind?: lsp.CompletionItemKind,
): monaco.languages.CompletionItemKind {
    switch (kind) {
        case lsp.CompletionItemKind.Text:
            return monaco.languages.CompletionItemKind.Text;
        case lsp.CompletionItemKind.Method:
            return monaco.languages.CompletionItemKind.Method;
        case lsp.CompletionItemKind.Function:
            return monaco.languages.CompletionItemKind.Function;
        case lsp.CompletionItemKind.Constructor:
            return monaco.languages.CompletionItemKind.Constructor;
        case lsp.CompletionItemKind.Field:
            return monaco.languages.CompletionItemKind.Field;
        case lsp.CompletionItemKind.Variable:
            return monaco.languages.CompletionItemKind.Variable;
        case lsp.CompletionItemKind.Class:
            return monaco.languages.CompletionItemKind.Class;
        case lsp.CompletionItemKind.Interface:
            return monaco.languages.CompletionItemKind.Interface;
        case lsp.CompletionItemKind.Module:
            return monaco.languages.CompletionItemKind.Module;
        case lsp.CompletionItemKind.Property:
            return monaco.languages.CompletionItemKind.Property;
        case lsp.CompletionItemKind.Unit:
            return monaco.languages.CompletionItemKind.Unit;
        case lsp.CompletionItemKind.Value:
            return monaco.languages.CompletionItemKind.Value;
        case lsp.CompletionItemKind.Enum:
            return monaco.languages.CompletionItemKind.Enum;
        case lsp.CompletionItemKind.Keyword:
            return monaco.languages.CompletionItemKind.Keyword;
        case lsp.CompletionItemKind.Snippet:
            return monaco.languages.CompletionItemKind.Snippet;
        case lsp.CompletionItemKind.Color:
            return monaco.languages.CompletionItemKind.Color;
        case lsp.CompletionItemKind.File:
            return monaco.languages.CompletionItemKind.File;
        case lsp.CompletionItemKind.Reference:
            return monaco.languages.CompletionItemKind.Reference;
        case lsp.CompletionItemKind.Folder:
            return monaco.languages.CompletionItemKind.Folder;
        case lsp.CompletionItemKind.EnumMember:
            return monaco.languages.CompletionItemKind.EnumMember;
        case lsp.CompletionItemKind.Constant:
            return monaco.languages.CompletionItemKind.Constant;
        case lsp.CompletionItemKind.Struct:
            return monaco.languages.CompletionItemKind.Struct;
        case lsp.CompletionItemKind.Event:
            return monaco.languages.CompletionItemKind.Event;
        case lsp.CompletionItemKind.Operator:
            return monaco.languages.CompletionItemKind.Operator;
        case lsp.CompletionItemKind.TypeParameter:
            return monaco.languages.CompletionItemKind.TypeParameter;
        default:
            return monaco.languages.CompletionItemKind.Text;
    }
}

export function setupLanguage(client: Client) {
    monaco.languages.register({ id: languageID });

    client.onTextDocumentPublishDiagnostics(
        (params: lsp.PublishDiagnosticsParams) => {
            const models = monaco.editor.getModels();

            if (params.uri.endsWith('.esc')) {
                client
                    .workspaceExecuteCommand({
                        command: 'compile',
                        arguments: [params.uri],
                    })
                    .catch((err) => {
                        if (err.message) {
                            try {
                                const errorMessages: Array<ErrorMesage> =
                                    JSON.parse(err.message);
                                for (const message of errorMessages) {
                                    const start = `${message.span.start.line}:${message.span.start.column}`;
                                    const end = `${message.span.end.line}:${message.span.end.column}`;
                                    console.log(
                                        `ERROR: ${start}-${end} ${message.message}`,
                                    );
                                }
                            } catch (e) {
                                console.error('Error message:', err.message);
                            }
                        }
                    })
                    .then((result) => {
                        if (!result) {
                            return;
                        }
                        const outputUri = params.uri.replace('.esc', '.js');
                        const model = models.find(
                            (model) => model.uri.toString() === outputUri,
                        );

                        if (!model) {
                            return;
                        }

                        model.setValue(result.text);
                    });
            }

            const model = models.find(
                (model) => model.uri.toString() === params.uri,
            );

            if (!model) {
                return;
            }

            const markers: monaco.editor.IMarkerData[] = params.diagnostics.map(
                (diagnostic): monaco.editor.IMarkerData => {
                    const result: monaco.editor.IMarkerData = {
                        severity: convertSeverity(diagnostic.severity),
                        startLineNumber: diagnostic.range.start.line + 1,
                        startColumn: diagnostic.range.start.character + 1,
                        endLineNumber: diagnostic.range.end.line + 1,
                        endColumn: diagnostic.range.end.character + 1,
                        message: diagnostic.message,
                    };

                    if (typeof diagnostic.code === 'string') {
                        result.code = diagnostic.code;
                    } else if (typeof diagnostic.code === 'number') {
                        result.code = `(${diagnostic.code})`;
                    }

                    return result;
                },
            );
            monaco.editor.setModelMarkers(model, languageID, markers);
        },
    );

    monaco.editor.onDidCreateModel((model) => {
        console.log('onDidCreateModel', model.uri.toString());

        client.textDocumentDidOpen({
            textDocument: {
                uri: model.uri.toString(),
                languageId: model.getLanguageId(),
                version: model.getVersionId(),
                text: model.getValue(),
            },
        });

        model.onDidChangeContent(() => {
            client.textDocumentDidChange({
                textDocument: {
                    uri: model.uri.toString(),
                    version: model.getVersionId(),
                },
                contentChanges: [
                    {
                        text: model.getValue(),
                    },
                ],
            });
        });
    });

    monaco.editor.onWillDisposeModel((model) => {
        console.log('onWillDisposeModel', model.uri.toString());

        client.textDocumentDidClose({
            textDocument: {
                uri: model.uri.toString(),
            },
        });
    });

    monaco.languages.onLanguage(languageID, async () => {
        console.log(`onLanguage called for ${languageID}`);
    });

    monaco.languages.registerDeclarationProvider(languageID, {
        async provideDeclaration(model, position, _token) {
            try {
                const decl = await client.textDocumentDeclaration({
                    textDocument: {
                        uri: model.uri.toString(),
                    },
                    position: monacoPosToLspPos(position),
                });

                if (lsp.Location.is(decl)) {
                    return {
                        uri: monaco.Uri.parse(decl.uri),
                        range: lspRangeToMonacoRange(decl.range),
                    };
                }

                throw new Error('TODO: handle Location[] and LocationLink[]');
            } catch (e) {
                console.error(e);
            }
        },
    });

    monaco.languages.registerDefinitionProvider(languageID, {
        async provideDefinition(model, position, _token) {
            try {
                console.log('provideDefinition called');
                console.log(position);
                const def = await client.textDocumentDefinition({
                    textDocument: {
                        uri: model.uri.toString(),
                    },
                    position: monacoPosToLspPos(position),
                });

                if (lsp.Location.is(def)) {
                    return {
                        uri: monaco.Uri.parse(def.uri),
                        range: lspRangeToMonacoRange(def.range),
                    };
                }

                throw new Error('TODO: handle Location[] and LocationLink[]');
            } catch (e) {
                console.error(e);
            }
        },
    });

    monaco.languages.setMonarchTokensProvider(languageID, monarchLanguage);
    monaco.languages.setLanguageConfiguration(languageID, {
        brackets: [
            ['(', ')'],
            ['{', '}'],
            ['[', ']'],
            ['<', '>'],
            ['`', '`'],
            ['"', '"'],
        ],
    });
    monaco.languages.registerCompletionItemProvider(languageID, {
        triggerCharacters: ['.'],
        async provideCompletionItems(model, position, _context, _token) {
            try {
                const result = await client.textDocumentCompletion({
                    textDocument: { uri: model.uri.toString() },
                    position: monacoPosToLspPos(position),
                });

                if (!result) {
                    return { suggestions: [] };
                }

                const items: lsp.CompletionItem[] = Array.isArray(result)
                    ? result
                    : result.items;
                const isIncomplete = Array.isArray(result)
                    ? false
                    : result.isIncomplete;

                const word = model.getWordUntilPosition(position);
                const defaultRange = new monaco.Range(
                    position.lineNumber,
                    word.startColumn,
                    position.lineNumber,
                    word.endColumn,
                );

                const suggestions: monaco.languages.CompletionItem[] =
                    items.map((item) => ({
                        label: item.label,
                        kind: lspKindToMonacoKind(item.kind),
                        detail: item.detail,
                        filterText: item.filterText,
                        insertText:
                            typeof item.insertText === 'string'
                                ? item.insertText
                                : item.label,
                        range: defaultRange,
                    }));

                return { suggestions, incomplete: isIncomplete };
            } catch (e) {
                console.error('Completion error:', e);
                return { suggestions: [] };
            }
        },
    });

    monaco.languages.registerHoverProvider(languageID, {
        async provideHover(model, position, _token, _context) {
            const hover = await client.textDocumentHover({
                textDocument: { uri: model.uri.toString() },
                position: monacoPosToLspPos(position),
            });
            if (!hover) {
                return {
                    contents: [
                        {
                            value: 'TODO: return hover contents',
                        },
                    ],
                };
            }

            if (lsp.MarkupContent.is(hover.contents)) {
                return {
                    contents: [
                        {
                            value: hover.contents.value,
                        },
                    ],
                };
            }

            return {
                contents: [
                    {
                        value: 'TODO: hover - handle other types',
                    },
                ],
            };
        },
    });

    monaco.editor.defineTheme('escalier-theme', {
        base: 'vs-dark',
        inherit: true,
        colors: {},
        rules: [],
    });
}
