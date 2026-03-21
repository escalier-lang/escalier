import * as lsp from 'vscode-languageserver-protocol';

export type CompletionDeps = {
    getCompletion: (
        params: lsp.CompletionParams,
    ) => Promise<lsp.CompletionList | lsp.CompletionItem[] | null>;
    resolveCompletionItem?: (
        item: lsp.CompletionItem,
    ) => Promise<lsp.CompletionItem>;
};

export type MonacoRange = {
    startLineNumber: number;
    startColumn: number;
    endLineNumber: number;
    endColumn: number;
};

// Monaco's CompletionItemInsertTextRule.InsertAsSnippet = 4
const INSERT_AS_SNIPPET = 4;

export type CompletionSuggestion = {
    label: string;
    kind: number;
    detail?: string;
    filterText?: string;
    insertText: string;
    insertTextRules?: number;
    range: MonacoRange;
    // Original LSP item, used by resolveCompletionItem to fetch deferred details.
    _lspItem?: lsp.CompletionItem;
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

export function lspRangeToMonacoRange(range: lsp.Range): MonacoRange {
    return {
        startLineNumber: range.start.line + 1,
        startColumn: range.start.character + 1,
        endLineNumber: range.end.line + 1,
        endColumn: range.end.character + 1,
    };
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
        let insertText: string;
        let range: MonacoRange;

        if (item.textEdit) {
            insertText = item.textEdit.newText;
            if (lsp.InsertReplaceEdit.is(item.textEdit)) {
                range = lspRangeToMonacoRange(item.textEdit.insert);
            } else {
                range = lspRangeToMonacoRange(item.textEdit.range);
            }
        } else {
            insertText = item.insertText ?? item.label;
            range = defaultRange;
        }

        const suggestion: CompletionSuggestion = {
            label: item.label,
            kind: lspKindToMonacoKind(item.kind),
            detail: item.detail,
            filterText: item.filterText,
            insertText,
            range,
        };

        if (item.insertTextFormat === lsp.InsertTextFormat.Snippet) {
            suggestion.insertTextRules = INSERT_AS_SNIPPET;
        }

        // Stash the original LSP item for deferred resolution.
        if (item.data !== undefined) {
            suggestion._lspItem = item;
        }

        return suggestion;
    });

    return { suggestions, incomplete: isIncomplete };
}

export async function resolveCompletionItem(
    deps: CompletionDeps,
    suggestion: CompletionSuggestion,
): Promise<CompletionSuggestion> {
    if (!deps.resolveCompletionItem || !suggestion._lspItem) {
        return suggestion;
    }
    const resolved = await deps.resolveCompletionItem(suggestion._lspItem);
    if (resolved.detail) {
        suggestion.detail = resolved.detail;
    }
    return suggestion;
}
