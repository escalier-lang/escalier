import type * as monaco from 'monaco-editor-core';
import type * as lsp from 'vscode-languageserver-protocol';

// Monaco is imported for its types alone, so this module loads no Monaco code
// and its tests run without a browser.

// HoverParams is the params of `textDocument/hover` with Escalier's verbosity
// extension. verbosityLevel is how many layers of type aliases the hover
// expands, and a server reads a missing level as 0.
export type HoverParams = lsp.HoverParams & { verbosityLevel?: number };

// Hover is a hover with Escalier's verbosity extension. verbosityLevel is the
// level the server rendered, which is lower than the level asked for when
// expanding stopped changing the type. The two flags say whether the next level
// up or down exists.
export type Hover = lsp.Hover & {
    verbosityLevel?: number;
    canIncreaseVerbosity?: boolean;
    canDecreaseVerbosity?: boolean;
};

// MonacoHover is a Monaco hover that remembers the level it was rendered at.
// Monaco hands it back as previousHover when the user expands or collapses it.
export type MonacoHover = monaco.languages.Hover & { verbosityLevel: number };

export type HoverRequest = (params: HoverParams) => Promise<Hover | null>;

export type HoverProviderOptions = {
    /** Sends `textDocument/hover` to the server. */
    request: HoverRequest;
    /** Sends any change to the document at uri that has not been sent yet. */
    flushPendingChanges: (uri: string) => void;
};

// requestedLevel returns the verbosity level to ask the server for. A fresh
// hover asks for level 0. An expand or collapse asks for the level of the
// hover it replaces plus Monaco's verbosityDelta, and never below 0.
export function requestedLevel(
    context?: monaco.languages.HoverContext<MonacoHover>,
): number {
    const request = context?.verbosityRequest;
    if (!request) {
        return 0;
    }
    return Math.max(
        0,
        request.previousHover.verbosityLevel + request.verbosityDelta,
    );
}

// provideHover requests the hover at position in the document uri names, at
// the level context asks for, and returns it in Monaco's form. It returns null
// when the server sends no hover or a hover that is not Markdown.
export async function provideHover(
    getHover: HoverRequest,
    uri: string,
    position: lsp.Position,
    context?: monaco.languages.HoverContext<MonacoHover>,
): Promise<MonacoHover | null> {
    const hover = await getHover({
        textDocument: { uri },
        position,
        verbosityLevel: requestedLevel(context),
    });
    if (!hover || !isMarkup(hover.contents)) {
        return null;
    }
    return {
        contents: [{ value: hover.contents.value }],
        verbosityLevel: hover.verbosityLevel ?? 0,
        canIncreaseVerbosity: hover.canIncreaseVerbosity ?? false,
        canDecreaseVerbosity: hover.canDecreaseVerbosity ?? false,
    };
}

function isMarkup(
    contents: lsp.Hover['contents'],
): contents is lsp.MarkupContent {
    return (
        typeof contents === 'object' &&
        !Array.isArray(contents) &&
        'kind' in contents
    );
}

/**
 * Returns a Monaco provider of the hover at the cursor, read from the server.
 * Monaco shows expand and collapse buttons from the hover's two verbosity
 * flags, and it returns null when the server returns no hover or the request
 * fails.
 */
export function createHoverProvider({
    request,
    flushPendingChanges,
}: HoverProviderOptions): monaco.languages.HoverProvider<MonacoHover> {
    return {
        async provideHover(model, position, _token, context) {
            const uri = model.uri.toString();
            try {
                flushPendingChanges(uri);
                return await provideHover(
                    request,
                    uri,
                    {
                        // Monaco counts lines and columns from 1, and LSP from 0.
                        line: position.lineNumber - 1,
                        character: position.column - 1,
                    },
                    context,
                );
            } catch (e) {
                console.error('Hover error:', e);
                return null;
            }
        },
    };
}
