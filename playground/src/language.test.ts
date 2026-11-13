import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { setupLanguage } from './language';
import type { Client } from './lsp-client/client';
import { useThemeStore } from './theme';

const monacoMock = vi.hoisted(() => {
    class Emitter<T> {
        private listeners: Array<(value: T) => void> = [];
        event = (listener: (value: T) => void) => {
            this.listeners.push(listener);
            return { dispose() {} };
        };
        fire(value: T) {
            for (const listener of this.listeners) {
                listener(value);
            }
        }
    }
    return {
        Emitter,
        MarkerSeverity: { Error: 8, Warning: 4, Info: 2, Hint: 1 },
        Range: class {},
        Uri: { parse: (s: string) => s },
        editor: {
            defineTheme: vi.fn(),
            getModels: vi.fn(() => [] as unknown[]),
            onDidCreateModel: vi.fn(),
            onWillDisposeModel: vi.fn(),
            setModelMarkers: vi.fn(),
            setTheme: vi.fn(),
        },
        languages: {
            onLanguage: vi.fn(),
            register: vi.fn(),
            registerCompletionItemProvider: vi.fn(),
            registerDeclarationProvider: vi.fn(),
            registerDefinitionProvider: vi.fn(),
            registerDocumentSemanticTokensProvider: vi.fn(),
            registerHoverProvider: vi.fn(),
            setLanguageConfiguration: vi.fn(),
            setMonarchTokensProvider: vi.fn(),
        },
    };
});

vi.mock('monaco-editor-core', () => monacoMock);

const uri = 'file:///lib/index.esc';
const legend: lsp.SemanticTokensLegend = {
    tokenTypes: ['variable'],
    tokenModifiers: ['mutable'],
};

type FakeClient = {
    client: Client;
    publishDiagnostics: (params: lsp.PublishDiagnosticsParams) => void;
    textDocumentSemanticTokensFull: ReturnType<typeof vi.fn>;
    textDocumentHover: ReturnType<typeof vi.fn>;
};

function fakeClient(): FakeClient {
    let onDiagnostics: (params: lsp.PublishDiagnosticsParams) => void =
        () => {};
    const textDocumentSemanticTokensFull = vi
        .fn()
        .mockResolvedValue({ data: [0, 8, 1, 0, 1] });
    const textDocumentHover = vi.fn();
    const client = {
        onTextDocumentPublishDiagnostics: (
            handler: (params: lsp.PublishDiagnosticsParams) => void,
        ) => {
            onDiagnostics = handler;
        },
        workspaceExecuteCommand: vi.fn().mockResolvedValue(undefined),
        textDocumentSemanticTokensFull,
        textDocumentHover,
    } as unknown as Client;
    return {
        client,
        publishDiagnostics: (params) => onDiagnostics(params),
        textDocumentSemanticTokensFull,
        textDocumentHover,
    };
}

describe('setupLanguage', () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });

    afterEach(() => {
        vi.useRealTimers();
    });

    test('registers a semantic tokens provider with the server legend', async () => {
        const { client, textDocumentSemanticTokensFull } = fakeClient();

        setupLanguage(client, {
            semanticTokensProvider: { legend, full: true },
        });

        const register =
            monacoMock.languages.registerDocumentSemanticTokensProvider;
        expect(register).toHaveBeenCalledTimes(1);
        const [languageID, provider] = register.mock.calls[0];
        expect(languageID).toBe('escalier');
        expect(provider.getLegend()).toBe(legend);

        const model = { uri: { toString: () => uri } };
        const tokens = await provider.provideDocumentSemanticTokens(
            model,
            null,
            {},
        );
        expect(textDocumentSemanticTokensFull).toHaveBeenCalledWith({
            textDocument: { uri },
        });
        expect(tokens.data).toEqual(new Uint32Array([0, 8, 1, 0, 1]));

        const rules = [{ token: 'variable.mutable', fontStyle: 'underline' }];
        expect(monacoMock.editor.defineTheme).toHaveBeenCalledWith(
            'escalier-dark',
            expect.objectContaining({ base: 'vs-dark', rules }),
        );
        expect(monacoMock.editor.defineTheme).toHaveBeenCalledWith(
            'escalier-light',
            expect.objectContaining({ base: 'vs', rules }),
        );
    });

    test('registers no provider when the server advertises no semantic tokens', () => {
        const { client } = fakeClient();

        setupLanguage(client, {});

        expect(
            monacoMock.languages.registerDocumentSemanticTokensProvider,
        ).not.toHaveBeenCalled();
        expect(monacoMock.editor.defineTheme).toHaveBeenCalledWith(
            'escalier-dark',
            expect.objectContaining({ rules: [] }),
        );
    });

    test('sets the Monaco theme that matches the theme store', () => {
        const { client } = fakeClient();
        useThemeStore.getState().setPreference('dark');

        setupLanguage(client, {});
        expect(monacoMock.editor.setTheme).toHaveBeenLastCalledWith(
            'escalier-dark',
        );

        useThemeStore.getState().setPreference('light');
        expect(monacoMock.editor.setTheme).toHaveBeenLastCalledWith(
            'escalier-light',
        );
    });

    test('asks Monaco for new tokens once after a burst of diagnostics', () => {
        vi.useFakeTimers();
        const { client, publishDiagnostics } = fakeClient();
        monacoMock.editor.getModels.mockReturnValue([
            { uri: { toString: () => uri } },
        ]);

        setupLanguage(client, {
            semanticTokensProvider: { legend, full: true },
        });
        const [, provider] =
            monacoMock.languages.registerDocumentSemanticTokensProvider.mock
                .calls[0];
        const refreshed = vi.fn();
        provider.onDidChange(refreshed);

        publishDiagnostics({ uri, diagnostics: [] });
        publishDiagnostics({ uri, diagnostics: [] });
        expect(refreshed).not.toHaveBeenCalled();

        vi.runAllTimers();
        expect(refreshed).toHaveBeenCalledTimes(1);
    });

    test('shows the markdown hover the server returns', async () => {
        const { client, textDocumentHover } = fakeClient();
        textDocumentHover.mockResolvedValue({
            contents: {
                kind: 'markdown',
                value: '```escalier\nx: number\n```',
            },
        });

        setupLanguage(client, {});

        const [languageID, provider] =
            monacoMock.languages.registerHoverProvider.mock.calls[0];
        expect(languageID).toBe('escalier');
        const model = { uri: { toString: () => uri } };
        const hover = await provider.provideHover(model, {
            lineNumber: 1,
            column: 5,
        });
        expect(textDocumentHover).toHaveBeenCalledWith({
            textDocument: { uri },
            position: { line: 0, character: 4 },
        });
        expect(hover).toEqual({
            contents: [{ value: '```escalier\nx: number\n```' }],
        });
    });

    test('returns no hover when the server returns none', async () => {
        const { client, textDocumentHover } = fakeClient();
        textDocumentHover.mockResolvedValue(null);

        setupLanguage(client, {});

        const [, provider] =
            monacoMock.languages.registerHoverProvider.mock.calls[0];
        const model = { uri: { toString: () => uri } };
        const hover = await provider.provideHover(model, {
            lineNumber: 1,
            column: 5,
        });
        expect(hover).toBeNull();
    });
});
