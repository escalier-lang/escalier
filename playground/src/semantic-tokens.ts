import type * as monaco from 'monaco-editor-core';
import type * as lsp from 'vscode-languageserver-protocol';

export type SemanticTokensRequest = (
    params: lsp.SemanticTokensParams,
) => Promise<lsp.SemanticTokens | null>;

export type SemanticTokensProviderOptions = {
    /** The legend the server advertised in its `initialize` result. */
    legend: lsp.SemanticTokensLegend;
    /** Sends `textDocument/semanticTokens/full` to the server. */
    request: SemanticTokensRequest;
    /** Reports whether a change to the document at uri has not been sent yet. */
    hasPendingChange: (uri: string) => boolean;
    /** Fires when every document's tokens may have changed. */
    onDidChange: monaco.IEvent<void>;
};

/**
 * Returns a Monaco provider of a document's semantic tokens, read from the server. While
 * hasPendingChange reports a change to the document, the provider throws an error whose
 * message starts with `busy` instead of sending a request. Monaco keeps the tokens it has
 * on such an error.
 */
export function createSemanticTokensProvider({
    legend,
    request,
    hasPendingChange,
    onDidChange,
}: SemanticTokensProviderOptions): monaco.languages.DocumentSemanticTokensProvider {
    return {
        onDidChange,
        getLegend() {
            return legend;
        },
        async provideDocumentSemanticTokens(model, _lastResultId, _token) {
            const uri = model.uri.toString();
            if (hasPendingChange(uri)) {
                throw new Error('busy: a change is waiting to be sent');
            }
            const tokens = await request({ textDocument: { uri } });
            if (!tokens) {
                return null;
            }
            return {
                resultId: tokens.resultId,
                data: new Uint32Array(tokens.data),
            };
        },
        releaseDocumentSemanticTokens() {},
    };
}

/**
 * Returns a function that calls fn once on the next turn of the event loop, however
 * many times it is called before then.
 */
export function coalesced(fn: () => void): () => void {
    let timer: ReturnType<typeof setTimeout> | undefined;
    return () => {
        if (timer !== undefined) {
            return;
        }
        timer = setTimeout(() => {
            timer = undefined;
            fn();
        }, 0);
    };
}

/**
 * Returns a theme rule that underlines each token type in legend when it carries the
 * `mutable` modifier. Monaco looks a semantic token up in the theme as its type followed
 * by its modifiers in legend order, joined by dots, so `variable.mutable` matches a
 * mutable variable and keeps the colour the theme gives `variable`. A rule matches only
 * while `mutable` is the first modifier in legend order that a token carries.
 */
export function mutableTokenRules(
    legend: lsp.SemanticTokensLegend | undefined,
): monaco.editor.ITokenThemeRule[] {
    if (!legend?.tokenModifiers.includes('mutable')) {
        return [];
    }
    return legend.tokenTypes.map((tokenType) => ({
        token: `${tokenType}.mutable`,
        fontStyle: 'underline',
    }));
}
