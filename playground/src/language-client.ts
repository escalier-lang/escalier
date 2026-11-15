// SPIKE: runs the WebAssembly language server behind monaco-languageclient. See
// docs/research/monaco-languageclient-spike.md. This branch is not meant to merge.
import * as monaco from 'monaco-editor-core';
import { MonacoLanguageClient } from 'monaco-languageclient';
import { MonacoVscodeApiWrapper } from 'monaco-languageclient/vscodeApiWrapper';
import * as vscode from 'vscode';
import {
    CloseAction,
    DidChangeTextDocumentNotification,
    type DocumentSemanticsTokensSignature,
    ErrorAction,
    type HandleDiagnosticsSignature,
    SemanticTokensRegistrationType,
} from 'vscode-languageclient/browser';

import { registerBasicLanguages } from './basic-languages';
import type { Client } from './lsp-client/client';
import { monarchLanguage } from './monarch-language';
import { coalesced, modifierTokenRules } from './semantic-tokens';
import { monacoThemeName, useThemeStore } from './theme';

export const languageID = 'escalier';

/**
 * Starts the VS Code services that monaco-languageclient needs. Must finish before the
 * first editor or model is created.
 */
export async function startVscodeApi() {
    const wrapper = new MonacoVscodeApiWrapper({
        // `classic` keeps Monaco's Monarch tokenizers and standalone themes, where
        // `extended` replaces both with TextMate grammars and VS Code themes.
        $type: 'classic',
        viewsConfig: { $type: 'EditorService' },
        // user-worker.ts already sets MonacoEnvironment.getWorker.
        monacoWorkerFactory: () => {},
        advanced: {
            loadExtensionServices: true,
            enforceSemanticHighlighting: true,
        },
    });
    await wrapper.start();
}

/** Registers the Escalier language, its tokenizer and the playground's themes. */
function registerLanguage(
    legend: { tokenTypes: string[]; tokenModifiers: string[] } | undefined,
) {
    monaco.languages.register({ id: languageID, extensions: ['.esc'] });
    registerBasicLanguages();
    monaco.languages.setMonarchTokensProvider(languageID, monarchLanguage);
    monaco.languages.setLanguageConfiguration(languageID, {
        brackets: [
            ['(', ')'],
            ['{', '}'],
            ['[', ']'],
            ['`', '`'],
            ['"', '"'],
        ],
    });

    const rules = modifierTokenRules(legend);
    monaco.editor.defineTheme(monacoThemeName('dark'), {
        base: 'vs-dark',
        inherit: true,
        colors: {},
        rules,
    });
    monaco.editor.defineTheme(monacoThemeName('light'), {
        base: 'vs',
        inherit: true,
        colors: {},
        rules,
    });
    monaco.editor.setTheme(monacoThemeName(useThemeStore.getState().theme));
    useThemeStore.subscribe((state, prev) => {
        if (state.theme !== prev.theme) {
            monaco.editor.setTheme(monacoThemeName(state.theme));
        }
    });
}

/**
 * Starts a language client over client's stdin and stdout, registers the language and
 * returns the started client.
 */
export async function startLanguageClient(
    client: Client,
): Promise<MonacoLanguageClient> {
    // Documents with a didChange that vscode-languageclient holds back. It sends a
    // full-sync change 250 ms after the last edit, or before the next request.
    const pendingChanges = new Set<string>();

    const refreshSemanticTokens = coalesced(() => {
        const feature = languageClient.getFeature(
            SemanticTokensRegistrationType.method,
        );
        for (const editor of vscode.window.visibleTextEditors) {
            feature
                ?.getProvider(editor.document)
                ?.onDidChangeSemanticTokensEmitter.fire();
        }
        // The standalone editors aren't VS Code text editors, so fire for one open
        // document as well. The emitter is shared by every document.
        const doc = vscode.workspace.textDocuments.find(
            (d) => d.languageId === languageID,
        );
        if (doc) {
            feature?.getProvider(doc)?.onDidChangeSemanticTokensEmitter.fire();
        }
    });

    const languageClient: MonacoLanguageClient = new MonacoLanguageClient({
        name: 'Escalier Language Client',
        clientOptions: {
            documentSelector: [{ language: languageID }],
            workspaceFolder: {
                index: 0,
                name: 'root',
                uri: vscode.Uri.parse('file:///'),
            },
            errorHandler: {
                error: () => ({ action: ErrorAction.Continue }),
                closed: () => ({ action: CloseAction.DoNotRestart }),
            },
            middleware: {
                // Hover goes through registerVerboseHover, since the client's hover
                // feature drops Monaco's verbosityRequest.
                provideHover: () => null,
                didChange: (
                    event: vscode.TextDocumentChangeEvent,
                    next: (e: vscode.TextDocumentChangeEvent) => Promise<void>,
                ) => {
                    pendingChanges.add(event.document.uri.toString());
                    return next(event);
                },
                handleDiagnostics: (
                    uri: vscode.Uri,
                    diagnostics: vscode.Diagnostic[],
                    next: HandleDiagnosticsSignature,
                ) => {
                    next(uri, diagnostics);
                    refreshSemanticTokens();
                    const isOpen = vscode.workspace.textDocuments.some(
                        (d) => d.uri.toString() === uri.toString(),
                    );
                    if (isOpen && uri.path.endsWith('.esc')) {
                        languageClient
                            ?.sendRequest('workspace/executeCommand', {
                                command: 'compile',
                                arguments: [uri.toString()],
                            })
                            .catch((err: Error) =>
                                console.error('compile failed:', err.message),
                            );
                    }
                },
                provideDocumentSemanticTokens: (
                    document: vscode.TextDocument,
                    token: vscode.CancellationToken,
                    next: DocumentSemanticsTokensSignature,
                ) => {
                    if (pendingChanges.has(document.uri.toString())) {
                        throw new Error('busy: a change is waiting to be sent');
                    }
                    return next(document, token);
                },
            },
        },
        messageTransports: client.messageTransports(),
    });

    await languageClient.start();

    languageClient
        .getFeature(DidChangeTextDocumentNotification.method)
        .onNotificationSent((event) => {
            pendingChanges.delete(event.textDocument.uri.toString());
        });

    registerVerboseHover(languageClient);

    registerLanguage(
        languageClient.initializeResult?.capabilities.semanticTokensProvider
            ?.legend,
    );
    return languageClient;
}

/**
 * Registers a Monaco hover provider that sends `textDocument/hover` through
 * languageClient and reports a verbosity level Monaco can step. The server has no
 * verbosity support yet (escalier-lang/escalier#2715), so the level is shown in the
 * hover text only.
 */
function registerVerboseHover(languageClient: MonacoLanguageClient) {
    const levels = new WeakMap<object, number>();
    monaco.languages.registerHoverProvider(languageID, {
        async provideHover(model, position, _token, context) {
            const previous = context?.verbosityRequest?.previousHover;
            const level = Math.max(
                0,
                (previous ? (levels.get(previous) ?? 0) : 0) +
                    (context?.verbosityRequest?.verbosityDelta ?? 0),
            );
            const hover = await languageClient.sendRequest<{
                contents: { kind: string; value: string };
            } | null>('textDocument/hover', {
                textDocument: { uri: model.uri.toString() },
                position: {
                    line: position.lineNumber - 1,
                    character: position.column - 1,
                },
            });
            if (!hover) {
                return null;
            }
            const result: monaco.languages.Hover = {
                contents: [
                    {
                        value: `${hover.contents.value}\n\nverbosity level ${level}`,
                    },
                ],
                canIncreaseVerbosity: level < 2,
                canDecreaseVerbosity: level > 0,
            };
            levels.set(result, level);
            return result;
        },
    });
}
