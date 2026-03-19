import { describe, expect, test, vi } from 'vitest';
import * as lsp from 'vscode-languageserver-protocol';

import {
    type CompletionDeps,
    type MonacoRange,
    lspKindToMonacoKind,
    lspRangeToMonacoRange,
    provideCompletionItems,
} from './completions';

const defaultRange: MonacoRange = {
    startLineNumber: 1,
    startColumn: 1,
    endLineNumber: 1,
    endColumn: 5,
};

const position: lsp.Position = { line: 0, character: 4 };
const uri = 'file:///test.esc';

function makeDeps(
    result: lsp.CompletionList | lsp.CompletionItem[] | null,
): CompletionDeps {
    return {
        getCompletion: vi.fn().mockResolvedValue(result),
    };
}

describe('provideCompletionItems', () => {
    test('returns empty suggestions when result is null', async () => {
        const deps = makeDeps(null);
        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );
        expect(result).toEqual({ suggestions: [] });
    });

    test('handles CompletionItem[] result', async () => {
        const items: lsp.CompletionItem[] = [
            { label: 'foo', kind: lsp.CompletionItemKind.Variable },
            { label: 'bar', kind: lsp.CompletionItemKind.Function },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.incomplete).toBe(false);
        expect(result.suggestions).toHaveLength(2);
        expect(result.suggestions[0]).toEqual({
            label: 'foo',
            kind: lsp.CompletionItemKind.Variable,
            detail: undefined,
            filterText: undefined,
            insertText: 'foo',
            range: defaultRange,
        });
        expect(result.suggestions[1]).toEqual({
            label: 'bar',
            kind: lsp.CompletionItemKind.Function,
            detail: undefined,
            filterText: undefined,
            insertText: 'bar',
            range: defaultRange,
        });
    });

    test('handles CompletionList result', async () => {
        const list: lsp.CompletionList = {
            isIncomplete: true,
            items: [
                {
                    label: 'baz',
                    kind: lsp.CompletionItemKind.Method,
                    detail: 'a method',
                },
            ],
        };
        const deps = makeDeps(list);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.incomplete).toBe(true);
        expect(result.suggestions).toHaveLength(1);
        expect(result.suggestions[0]).toEqual({
            label: 'baz',
            kind: lsp.CompletionItemKind.Method,
            detail: 'a method',
            filterText: undefined,
            insertText: 'baz',
            range: defaultRange,
        });
    });

    test('uses insertText when provided', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'myFunc',
                kind: lsp.CompletionItemKind.Function,
                insertText: 'myFunc($1)',
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertText).toBe('myFunc($1)');
    });

    test('falls back to label when insertText is not a string', async () => {
        const items: lsp.CompletionItem[] = [
            { label: 'myVar', kind: lsp.CompletionItemKind.Variable },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertText).toBe('myVar');
    });

    test('preserves filterText and detail', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'item',
                kind: lsp.CompletionItemKind.Property,
                detail: 'string',
                filterText: '.item',
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].detail).toBe('string');
        expect(result.suggestions[0].filterText).toBe('.item');
    });

    test('passes correct params to getCompletion', async () => {
        const deps = makeDeps(null);

        await provideCompletionItems(deps, uri, position, defaultRange);

        expect(deps.getCompletion).toHaveBeenCalledWith({
            textDocument: { uri },
            position,
        });
    });

    test('uses textEdit.newText and range when TextEdit is provided', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'myFunc',
                kind: lsp.CompletionItemKind.Function,
                textEdit: {
                    newText: 'myFunc()',
                    range: {
                        start: { line: 0, character: 0 },
                        end: { line: 0, character: 4 },
                    },
                },
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertText).toBe('myFunc()');
        expect(result.suggestions[0].range).toEqual(
            lspRangeToMonacoRange({
                start: { line: 0, character: 0 },
                end: { line: 0, character: 4 },
            }),
        );
    });

    test('uses insert range from InsertReplaceEdit', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'myVar',
                kind: lsp.CompletionItemKind.Variable,
                textEdit: {
                    newText: 'myVar',
                    insert: {
                        start: { line: 0, character: 0 },
                        end: { line: 0, character: 2 },
                    },
                    replace: {
                        start: { line: 0, character: 0 },
                        end: { line: 0, character: 5 },
                    },
                },
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertText).toBe('myVar');
        expect(result.suggestions[0].range).toEqual(
            lspRangeToMonacoRange({
                start: { line: 0, character: 0 },
                end: { line: 0, character: 2 },
            }),
        );
    });

    test('textEdit takes precedence over insertText', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'foo',
                kind: lsp.CompletionItemKind.Function,
                insertText: 'ignored',
                textEdit: {
                    newText: 'foo()',
                    range: {
                        start: { line: 0, character: 0 },
                        end: { line: 0, character: 3 },
                    },
                },
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertText).toBe('foo()');
    });

    test('sets insertTextRules for snippet format', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'myFunc',
                kind: lsp.CompletionItemKind.Function,
                insertText: 'myFunc($1)',
                insertTextFormat: lsp.InsertTextFormat.Snippet,
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertText).toBe('myFunc($1)');
        expect(result.suggestions[0].insertTextRules).toBe(4); // InsertAsSnippet
    });

    test('does not set insertTextRules for plain text format', async () => {
        const items: lsp.CompletionItem[] = [
            {
                label: 'myVar',
                kind: lsp.CompletionItemKind.Variable,
                insertText: 'myVar',
                insertTextFormat: lsp.InsertTextFormat.PlainText,
            },
        ];
        const deps = makeDeps(items);

        const result = await provideCompletionItems(
            deps,
            uri,
            position,
            defaultRange,
        );

        expect(result.suggestions[0].insertTextRules).toBeUndefined();
    });
});

describe('lspKindToMonacoKind', () => {
    test('returns the kind value as-is', () => {
        expect(lspKindToMonacoKind(lsp.CompletionItemKind.Function)).toBe(
            lsp.CompletionItemKind.Function,
        );
        expect(lspKindToMonacoKind(lsp.CompletionItemKind.Variable)).toBe(
            lsp.CompletionItemKind.Variable,
        );
        expect(lspKindToMonacoKind(lsp.CompletionItemKind.Method)).toBe(
            lsp.CompletionItemKind.Method,
        );
    });

    test('defaults to Text when kind is undefined', () => {
        expect(lspKindToMonacoKind(undefined)).toBe(
            lsp.CompletionItemKind.Text,
        );
    });
});

describe('lspRangeToMonacoRange', () => {
    test('converts 0-based LSP range to 1-based Monaco range', () => {
        const lspRange: lsp.Range = {
            start: { line: 2, character: 5 },
            end: { line: 2, character: 10 },
        };
        expect(lspRangeToMonacoRange(lspRange)).toEqual({
            startLineNumber: 3,
            startColumn: 6,
            endLineNumber: 3,
            endColumn: 11,
        });
    });
});
