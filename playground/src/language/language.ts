import * as monaco from 'monaco-editor-core';
import * as lsp from 'vscode-languageserver-protocol';

import {
    diagnosticsTokenRules,
    registerDiagnosticsLanguage,
} from '#diagnostics/diagnostics-language';
import type {
    DiagnosticSeverity,
    ProjectDiagnostic,
} from '#diagnostics/project-diagnostics';
import { useProjectDiagnosticsStore } from '#diagnostics/project-diagnostics-store';
import type { Client } from '#lsp-client/client';
import { monacoThemeName, useThemeStore } from '#theme/theme';

import { registerBasicLanguages } from './basic-languages';
import { createCodeActionProvider } from './code-actions';
import {
    type CompletionSuggestion,
    provideCompletionItems,
    resolveCompletionItem,
} from './completions';
import { monarchLanguage } from './monarch-language';
import { lspRangeToMonacoRange, monacoPosToLspPos } from './ranges';
import {
    coalesced,
    createSemanticTokensProvider,
    modifierTokenRules,
} from './semantic-tokens';
import { createSignatureHelpProvider } from './signature-help';

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

/** Returns the path of a `file://` URI, such as `/lib/index.esc`. */
export function uriToPath(uri: string): string {
    return decodeURIComponent(new URL(uri).pathname);
}

/**
 * Returns the name of an LSP severity. A diagnostic with no severity is an error, as
 * convertSeverity shows it.
 */
function severityName(severity?: lsp.DiagnosticSeverity): DiagnosticSeverity {
    switch (severity) {
        case lsp.DiagnosticSeverity.Warning:
            return 'warning';
        case lsp.DiagnosticSeverity.Information:
            return 'info';
        case lsp.DiagnosticSeverity.Hint:
            return 'hint';
        default:
            return 'error';
    }
}

/** Returns diagnostics as the ProjectDiagnostics of the file at uri. */
export function projectDiagnostics(
    uri: string,
    diagnostics: readonly lsp.Diagnostic[],
): ProjectDiagnostic[] {
    const path = uriToPath(uri);
    return diagnostics.map((d) => ({
        path,
        line: d.range.start.line + 1,
        column: d.range.start.character + 1,
        severity: severityName(d.severity),
        message: d.message,
    }));
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

export function setupLanguage(
    client: Client,
    capabilities: lsp.ServerCapabilities,
) {
    monaco.languages.register({ id: languageID });
    // The editor opens compiled output such as `build/main.js` next to Escalier source.
    registerBasicLanguages();

    // Fires when a document's semantic tokens may have changed, so Monaco asks for them
    // again.
    const semanticTokensChanged = new monaco.Emitter<void>();
    // A package check publishes diagnostics for every open file in a row, and each fire
    // makes Monaco request tokens for every visible model, so a burst fires once.
    const scheduleSemanticTokensRefresh = coalesced(() =>
        semanticTokensChanged.fire(),
    );

    type ModelState = {
        debounceTimer: ReturnType<typeof setTimeout> | undefined;
        model: monaco.editor.ITextModel;
    };

    // Per-model debounce state for didChange notifications.
    const modelStates = new Map<string, ModelState>();

    function sendDidChange(state: ModelState) {
        clearTimeout(state.debounceTimer);
        state.debounceTimer = undefined;
        void client.textDocumentDidChange({
            textDocument: {
                uri: state.model.uri.toString(),
                version: state.model.getVersionId(),
            },
            contentChanges: [
                {
                    text: state.model.getValue(),
                },
            ],
        });
    }

    // Flush any pending debounced didChange so the server has the latest
    // content before handling a request.
    function flushPendingChanges(uri: string) {
        const state = modelStates.get(uri);
        if (state?.debounceTimer !== undefined) {
            sendDidChange(state);
        }
    }

    // The diagnostics the server last published for each document, keyed by URI. A
    // code action request sends them back with the `data` the markers drop.
    const publishedDiagnostics = new Map<string, lsp.Diagnostic[]>();

    client.onTextDocumentPublishDiagnostics(
        (params: lsp.PublishDiagnosticsParams) => {
            publishedDiagnostics.set(params.uri, params.diagnostics);
            // The status bar counts the diagnostics of every file the server checked,
            // open or not, so they are recorded before the check for an open model below.
            useProjectDiagnosticsStore.getState().dispatch({
                type: 'setFileDiagnostics',
                path: uriToPath(params.uri),
                diagnostics: projectDiagnostics(params.uri, params.diagnostics),
            });
            const models = monaco.editor.getModels();

            const model = models.find(
                (model) => model.uri.toString() === params.uri,
            );

            if (!model) {
                return;
            }

            // The server publishes diagnostics after each check, and the tokens it reports
            // come from the latest check.
            scheduleSemanticTokensRefresh();

            if (params.uri.endsWith('.esc')) {
                // Trigger a full package recompilation. The compile
                // command writes output files directly to the VFS.
                // We only do this for files open in the editor (checked
                // by the !model guard above) because the LSP publishes
                // diagnostics for all project files, but the compile
                // command requires the document to be open.
                client
                    .workspaceExecuteCommand({
                        command: 'compile',
                        arguments: [params.uri],
                    })
                    .catch((err: unknown) => {
                        if (err instanceof Error && err.message) {
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
                            } catch {
                                console.error('Error message:', err.message);
                            }
                        }
                    });
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

        // We only create models for .esc files because these are the only files
        // we're editing.
        if (!model.uri.path.endsWith('.esc')) {
            return;
        }

        void client.textDocumentDidOpen({
            textDocument: {
                uri: model.uri.toString(),
                languageId: model.getLanguageId(),
                version: model.getVersionId(),
                text: model.getValue(),
            },
        });

        const state: ModelState = { debounceTimer: undefined, model };
        modelStates.set(model.uri.toString(), state);

        model.onDidChangeContent(() => {
            clearTimeout(state.debounceTimer);
            state.debounceTimer = setTimeout(() => sendDidChange(state), 500);
        });
    });

    monaco.editor.onWillDisposeModel((model) => {
        console.log('onWillDisposeModel', model.uri.toString());

        // We only create models for .esc files because these are the only files
        // we're editing.
        if (!model.uri.path.endsWith('.esc')) {
            return;
        }

        // Cancel any pending debounced didChange so it doesn't fire after close.
        const state = modelStates.get(model.uri.toString());
        if (state) {
            clearTimeout(state.debounceTimer);
            state.debounceTimer = undefined;
        }
        modelStates.delete(model.uri.toString());
        void client.textDocumentDidClose({
            textDocument: {
                uri: model.uri.toString(),
            },
        });
    });

    monaco.languages.onLanguage(languageID, () => {
        console.log(`onLanguage called for ${languageID}`);
    });

    monaco.languages.registerDeclarationProvider(languageID, {
        async provideDeclaration(model, position, _token) {
            try {
                flushPendingChanges(model.uri.toString());
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
                flushPendingChanges(model.uri.toString());
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
        // `<` and `>` are left out because they also appear in operators such as `->`
        // and `>=`, where bracket pair colouring would mark them as unmatched.
        brackets: [
            ['(', ')'],
            ['{', '}'],
            ['[', ']'],
            ['`', '`'],
            ['"', '"'],
        ],
    });

    monaco.languages.registerCompletionItemProvider(languageID, {
        triggerCharacters: ['.'],
        async provideCompletionItems(model, position, _context, _token) {
            try {
                // Flush any pending didChange so the server has the latest content.
                flushPendingChanges(model.uri.toString());

                const word = model.getWordUntilPosition(position);
                const defaultRange = new monaco.Range(
                    position.lineNumber,
                    word.startColumn,
                    position.lineNumber,
                    word.endColumn,
                );

                return await provideCompletionItems(
                    (params) => client.textDocumentCompletion(params),
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
                    (item) => client.completionItemResolve(item),
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
            flushPendingChanges(model.uri.toString());
            const hover = await client.textDocumentHover({
                textDocument: { uri: model.uri.toString() },
                position: monacoPosToLspPos(position),
            });
            // The server sends its hover as Markdown, or nothing when the
            // cursor is on nothing with a hover to show.
            if (!hover || !lsp.MarkupContent.is(hover.contents)) {
                return null;
            }
            return {
                contents: [
                    {
                        value: hover.contents.value,
                    },
                ],
            };
        },
    });

    const codeActionOptions = capabilities.codeActionProvider;
    if (codeActionOptions) {
        monaco.languages.registerCodeActionProvider(
            languageID,
            createCodeActionProvider({
                request: (params) => client.textDocumentCodeAction(params),
                flushPendingChanges,
                publishedDiagnostics: (uri) =>
                    publishedDiagnostics.get(uri) ?? [],
            }),
            {
                providedCodeActionKinds:
                    typeof codeActionOptions === 'object'
                        ? codeActionOptions.codeActionKinds
                        : undefined,
            },
        );
    }

    const signatureHelpOptions = capabilities.signatureHelpProvider;
    if (signatureHelpOptions) {
        monaco.languages.registerSignatureHelpProvider(
            languageID,
            createSignatureHelpProvider({
                options: signatureHelpOptions,
                request: (params) => client.textDocumentSignatureHelp(params),
                flushPendingChanges,
            }),
        );
    }

    const semanticTokensProvider = capabilities.semanticTokensProvider;
    const legend = semanticTokensProvider?.legend;
    if (legend) {
        monaco.languages.registerDocumentSemanticTokensProvider(
            languageID,
            createSemanticTokensProvider({
                legend,
                request: (params) =>
                    client.textDocumentSemanticTokensFull(params),
                // Monaco asks for tokens soon after every edit. Flushing the debounced
                // didChange, as completion and hover do, would start a check per
                // request. The provider declines while the change is pending instead,
                // and the refresh that follows the check's diagnostics fetches the new
                // tokens.
                hasPendingChange: (uri) =>
                    modelStates.get(uri)?.debounceTimer !== undefined,
                onDidChange: semanticTokensChanged.event,
            }),
        );
    }

    registerDiagnosticsLanguage();
    const rules = modifierTokenRules(legend);
    monaco.editor.defineTheme(monacoThemeName('dark'), {
        base: 'vs-dark',
        inherit: true,
        colors: {},
        rules: [...rules, ...diagnosticsTokenRules.dark],
    });
    monaco.editor.defineTheme(monacoThemeName('light'), {
        base: 'vs',
        inherit: true,
        colors: {},
        rules: [...rules, ...diagnosticsTokenRules.light],
    });
    // Monaco applies one theme to every editor, so following the store here also
    // restyles the editors that exist already.
    monaco.editor.setTheme(monacoThemeName(useThemeStore.getState().theme));
    useThemeStore.subscribe((state, prev) => {
        if (state.theme !== prev.theme) {
            monaco.editor.setTheme(monacoThemeName(state.theme));
        }
    });
}
