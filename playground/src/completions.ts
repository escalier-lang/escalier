import * as lsp from 'vscode-languageserver-protocol';

export type CompletionDeps = {
    getCompletion: (
        params: lsp.CompletionParams,
    ) => Promise<lsp.CompletionList | lsp.CompletionItem[] | null>;
};

export type MonacoRange = {
    startLineNumber: number;
    startColumn: number;
    endLineNumber: number;
    endColumn: number;
};

export type CompletionSuggestion = {
    label: string;
    kind: number;
    detail?: string;
    filterText?: string;
    insertText: string;
    range: MonacoRange;
};

export type CompletionResult = {
    suggestions: CompletionSuggestion[];
    incomplete?: boolean;
};

export function lspKindToMonacoKind(kind?: lsp.CompletionItemKind): number {
    // LSP and Monaco use the same integer values for CompletionItemKind.
    // Default to Text (1) for undefined.
    return kind ?? lsp.CompletionItemKind.Text;
}

export async function provideCompletionItems(
    deps: CompletionDeps,
    uri: string,
    position: lsp.Position,
    defaultRange: MonacoRange,
): Promise<CompletionResult> {
    const result = await deps.getCompletion({
        textDocument: { uri },
        position,
    });

    if (!result) {
        return { suggestions: [] };
    }

    const items: lsp.CompletionItem[] = Array.isArray(result)
        ? result
        : result.items;
    const isIncomplete = Array.isArray(result) ? false : result.isIncomplete;

    const suggestions: CompletionSuggestion[] = items.map((item) => {
        const label =
            typeof item.label === 'string' ? item.label : item.label.label;
        return {
            label,
            kind: lspKindToMonacoKind(item.kind),
            detail: item.detail,
            filterText: item.filterText,
            insertText:
                typeof item.insertText === 'string' ? item.insertText : label,
            range: defaultRange,
        };
    });

    return { suggestions, incomplete: isIncomplete };
}
