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

// provideSignatureHelp requests the signatures of the call holding position in
// the document uri names, and returns them in Monaco's form.
export async function provideSignatureHelp(
    getSignatureHelp: (
        params: lsp.SignatureHelpParams,
    ) => Promise<lsp.SignatureHelp | null>,
    uri: string,
    position: lsp.Position,
): Promise<MonacoSignatureHelp | null> {
    const help = await getSignatureHelp({ textDocument: { uri }, position });
    return convertSignatureHelp(help);
}
