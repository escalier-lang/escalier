import * as monaco from 'monaco-editor-core';
import * as lsp from 'vscode-languageserver-protocol';

import {
    provideCompletionItems,
    resolveCompletionItem,
    type CompletionDeps,
    type CompletionSuggestion,
} from './completions';
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

export function setupLanguage(client: Client) {
    monaco.languages.register({ id: languageID });

    // Map of URI → flush function for pending debounced didChange notifications.
    const pendingFlush = new Map<string, () => void>();
    // Map of URI → cancel function to clear the debounce timer without sending.
    const pendingCancel = new Map<string, () => void>();

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

        if (!model.uri.path.endsWith('.esc')) {
            return;
        }

        client.textDocumentDidOpen({
            textDocument: {
                uri: model.uri.toString(),
                languageId: model.getLanguageId(),
                version: model.getVersionId(),
                text: model.getValue(),
            },
        });

        let debounceTimer: ReturnType<typeof setTimeout> | undefined;
        const sendDidChange = () => {
            clearTimeout(debounceTimer);
            debounceTimer = undefined;
            const version = model.getVersionId();
            console.log(`version: ${version}`);
            client.textDocumentDidChange({
                textDocument: {
                    uri: model.uri.toString(),
                    version: version,
                },
                contentChanges: [
                    {
                        text: model.getValue(),
                    },
                ],
            });
        };
        // Flush any pending debounced didChange so the server has the
        // latest content before handling a completion or hover request.
        pendingFlush.set(model.uri.toString(), () => {
            if (debounceTimer !== undefined) {
                sendDidChange();
            }
        });
        // Cancel any pending debounced didChange without sending.
        pendingCancel.set(model.uri.toString(), () => {
            clearTimeout(debounceTimer);
            debounceTimer = undefined;
        });
        model.onDidChangeContent(() => {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(sendDidChange, 500);
        });
    });

    monaco.editor.onWillDisposeModel((model) => {
        console.log('onWillDisposeModel', model.uri.toString());

        if (!model.uri.path.endsWith('.esc')) {
            return;
        }

        // Cancel any pending debounced didChange so it doesn't fire after close.
        pendingCancel.get(model.uri.toString())?.();
        pendingCancel.delete(model.uri.toString());
        pendingFlush.delete(model.uri.toString());
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
                pendingFlush.get(model.uri.toString())?.();
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
                pendingFlush.get(model.uri.toString())?.();
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
    const completionDeps: CompletionDeps = {
        getCompletion: (params) => client.textDocumentCompletion(params),
        resolveCompletionItem: (item) =>
            client.completionItemResolve(item),
    };

    monaco.languages.registerCompletionItemProvider(languageID, {
        triggerCharacters: ['.'],
        async provideCompletionItems(model, position, _context, _token) {
            try {
                // Flush any pending didChange so the server has the latest content.
                pendingFlush.get(model.uri.toString())?.();

                const word = model.getWordUntilPosition(position);
                const defaultRange = new monaco.Range(
                    position.lineNumber,
                    word.startColumn,
                    position.lineNumber,
                    word.endColumn,
                );

                return await provideCompletionItems(
                    completionDeps,
                    model.uri.toString(),
                    monacoPosToLspPos(position),
                    defaultRange,
                );
            } catch (e) {
                console.error('Completion error:', e);
                return { suggestions: [] };
            }
        },
        async resolveCompletionItem(item, _token) {
            try {
                // Monaco passes back the same object returned by
                // provideCompletionItems, which includes our _lspItem field.
                // Cast to CompletionSuggestion to access it.
                return await resolveCompletionItem(
                    completionDeps,
                    item as CompletionSuggestion,
                );
            } catch (e) {
                console.error('Completion resolve error:', e);
                return item;
            }
        },
    });

    monaco.languages.registerHoverProvider(languageID, {
        async provideHover(model, position, _token, _context) {
            pendingFlush.get(model.uri.toString())?.();
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
