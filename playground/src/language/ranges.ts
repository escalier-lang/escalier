import type * as lsp from 'vscode-languageserver-protocol';

// Conversions between LSP and Monaco positions and ranges. LSP counts lines and
// columns from 0, and Monaco from 1. Both count columns in UTF-16 code units, so
// only the base changes.

/** A Monaco range as a plain object, which Monaco accepts wherever it takes a range. */
export type MonacoRange = {
    startLineNumber: number;
    startColumn: number;
    endLineNumber: number;
    endColumn: number;
};

/** A Monaco position as a plain object. */
export type MonacoPosition = {
    lineNumber: number;
    column: number;
};

export function lspRangeToMonacoRange(range: lsp.Range): MonacoRange {
    return {
        startLineNumber: range.start.line + 1,
        startColumn: range.start.character + 1,
        endLineNumber: range.end.line + 1,
        endColumn: range.end.character + 1,
    };
}

export function monacoRangeToLspRange(range: MonacoRange): lsp.Range {
    return {
        start: monacoPosToLspPos({
            lineNumber: range.startLineNumber,
            column: range.startColumn,
        }),
        end: monacoPosToLspPos({
            lineNumber: range.endLineNumber,
            column: range.endColumn,
        }),
    };
}

export function monacoPosToLspPos(position: MonacoPosition): lsp.Position {
    return { line: position.lineNumber - 1, character: position.column - 1 };
}
