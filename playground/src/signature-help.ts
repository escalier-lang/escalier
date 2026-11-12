import type * as monaco from 'monaco-editor-core';
import type * as lsp from 'vscode-languageserver-protocol';

// These types mirror Monaco's SignatureHelp types, so this module converts the
// LSP response without importing Monaco and its tests run without a browser.

export type MonacoDocumentation = string | { value: string };

export type MonacoParameterInformation = {
    // A string the signature label contains, or the [start, end) offsets of
    // the parameter in the signature label.
    label: string | [number, number];
    documentation?: MonacoDocumentation;
};

export type MonacoSignatureInformation = {
    label: string;
    documentation?: MonacoDocumentation;
    parameters: MonacoParameterInformation[];
    activeParameter?: number;
};

export type MonacoSignatureHelp = {
    signatures: MonacoSignatureInformation[];
    activeSignature: number;
    activeParameter: number;
};

function convertDocumentation(
    doc: string | lsp.MarkupContent | undefined,
): MonacoDocumentation | undefined {
    if (doc === undefined || typeof doc === 'string') {
        return doc;
    }
    return { value: doc.value };
}

// convertSignatureHelp returns the Monaco form of an LSP signature help
// response, or null when the response has no signature. LSP and Monaco both
// count label offsets in UTF-16 code units, so a parameter's range carries
// over unchanged.
export function convertSignatureHelp(
    help: lsp.SignatureHelp | null,
): MonacoSignatureHelp | null {
    if (!help || help.signatures.length === 0) {
        return null;
    }
    return {
        signatures: help.signatures.map((sig) => ({
            label: sig.label,
            documentation: convertDocumentation(sig.documentation),
            parameters: (sig.parameters ?? []).map((param) => ({
                label: param.label,
                documentation: convertDocumentation(param.documentation),
            })),
            activeParameter: sig.activeParameter ?? undefined,
        })),
        activeSignature: help.activeSignature ?? 0,
        activeParameter: help.activeParameter ?? 0,
    };
}

export type SignatureHelpRequest = (
    params: lsp.SignatureHelpParams,
) => Promise<lsp.SignatureHelp | null>;

export type SignatureHelpProviderOptions = {
    /** The options the server advertised in its `initialize` result. */
    options: lsp.SignatureHelpOptions;
    /** Sends `textDocument/signatureHelp` to the server. */
    request: SignatureHelpRequest;
    /** Sends any change to the document at uri that has not been sent yet. */
    flushPendingChanges: (uri: string) => void;
};

/**
 * Returns a Monaco provider of the signatures of the call holding the cursor,
 * read from the server. It triggers on the characters the server advertised,
 * and it returns null when the server returns no signature or the request
 * fails.
 */
export function createSignatureHelpProvider({
    options,
    request,
    flushPendingChanges,
}: SignatureHelpProviderOptions): monaco.languages.SignatureHelpProvider {
    return {
        signatureHelpTriggerCharacters: options.triggerCharacters,
        signatureHelpRetriggerCharacters: options.retriggerCharacters,
        async provideSignatureHelp(model, position, _token, _context) {
            const uri = model.uri.toString();
            try {
                // The `(` or `,` that triggered the request is usually still
                // in a debounced didChange, and the server must see it.
                flushPendingChanges(uri);
                const value = await provideSignatureHelp(request, uri, {
                    // Monaco counts lines and columns from 1, and LSP from 0.
                    line: position.lineNumber - 1,
                    character: position.column - 1,
                });
                if (!value) {
                    return null;
                }
                return { value, dispose() {} };
            } catch (e) {
                console.error('Signature help error:', e);
                return null;
            }
        },
    };
}

// provideSignatureHelp requests the signatures of the call holding position in
// the document uri names, and returns them in Monaco's form.
export async function provideSignatureHelp(
    getSignatureHelp: SignatureHelpRequest,
    uri: string,
    position: lsp.Position,
): Promise<MonacoSignatureHelp | null> {
    const help = await getSignatureHelp({ textDocument: { uri }, position });
    return convertSignatureHelp(help);
}
