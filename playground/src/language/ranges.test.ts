import { describe, expect, test } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import {
    lspRangeToMonacoRange,
    monacoPosToLspPos,
    monacoRangeToLspRange,
} from './ranges';

const lspRange: lsp.Range = {
    start: { line: 2, character: 5 },
    end: { line: 3, character: 10 },
};
const monacoRange = {
    startLineNumber: 3,
    startColumn: 6,
    endLineNumber: 4,
    endColumn: 11,
};

describe('lspRangeToMonacoRange', () => {
    test('converts a 0-based LSP range to a 1-based Monaco range', () => {
        expect(lspRangeToMonacoRange(lspRange)).toEqual(monacoRange);
    });
});

describe('monacoRangeToLspRange', () => {
    test('converts a 1-based Monaco range to a 0-based LSP range', () => {
        expect(monacoRangeToLspRange(monacoRange)).toEqual(lspRange);
    });
});

describe('monacoPosToLspPos', () => {
    test('converts a 1-based Monaco position to a 0-based LSP position', () => {
        expect(monacoPosToLspPos({ lineNumber: 1, column: 5 })).toEqual({
            line: 0,
            character: 4,
        });
    });
});
