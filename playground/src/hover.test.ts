import type * as monaco from 'monaco-editor-core';
import { describe, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import {
    type Hover,
    type MonacoHover,
    createHoverProvider,
    provideHover,
    requestedLevel,
} from './hover';

const position: lsp.Position = { line: 1, character: 12 };
const uri = 'file:///lib/a.esc';

// verbosityRequest returns the context Monaco passes when the user expands or
// collapses a hover rendered at level.
function verbosityRequest(
    level: number,
    delta: number,
): monaco.languages.HoverContext<MonacoHover> {
    return {
        verbosityRequest: {
            verbosityDelta: delta,
            previousHover: { contents: [], verbosityLevel: level },
        },
    };
}

function markdownHover(value: string, extra: Partial<Hover> = {}): Hover {
    return { contents: { kind: 'markdown', value }, ...extra };
}

describe('requestedLevel', () => {
    test('asks for level 0 on a fresh hover', () => {
        expect(requestedLevel(undefined)).toBe(0);
        expect(requestedLevel({})).toBe(0);
    });

    test('adds the delta to the level of the previous hover', () => {
        expect(requestedLevel(verbosityRequest(1, 1))).toBe(2);
        expect(requestedLevel(verbosityRequest(2, -1))).toBe(1);
    });

    test('never asks for a level below 0', () => {
        expect(requestedLevel(verbosityRequest(0, -1))).toBe(0);
    });
});

describe('provideHover', () => {
    test('sends the document, position and level', async () => {
        const getHover = vi.fn().mockResolvedValue(null);
        await provideHover(getHover, uri, position, verbosityRequest(0, 1));
        expect(getHover).toHaveBeenCalledWith({
            textDocument: { uri },
            position,
            verbosityLevel: 1,
        });
    });

    test('returns null when the server returns null', async () => {
        const getHover = vi.fn().mockResolvedValue(null);
        expect(await provideHover(getHover, uri, position)).toBeNull();
    });

    test('returns null for a hover that is not Markdown', async () => {
        const getHover = vi.fn().mockResolvedValue({ contents: 'plain' });
        expect(await provideHover(getHover, uri, position)).toBeNull();
    });

    test('carries the level and both flags over', async () => {
        const getHover = vi.fn().mockResolvedValue(
            markdownHover('type Y = number', {
                verbosityLevel: 1,
                canIncreaseVerbosity: false,
                canDecreaseVerbosity: true,
            }),
        );
        expect(
            await provideHover(getHover, uri, position, verbosityRequest(0, 1)),
        ).toEqual({
            contents: [{ value: 'type Y = number' }],
            verbosityLevel: 1,
            canIncreaseVerbosity: false,
            canDecreaseVerbosity: true,
        });
    });

    test('reads a hover without the extension as level 0', async () => {
        const getHover = vi.fn().mockResolvedValue(markdownHover('x: 5'));
        expect(await provideHover(getHover, uri, position)).toEqual({
            contents: [{ value: 'x: 5' }],
            verbosityLevel: 0,
            canIncreaseVerbosity: false,
            canDecreaseVerbosity: false,
        });
    });
});

describe('createHoverProvider', () => {
    const model = {
        uri: { toString: () => uri },
    } as unknown as monaco.editor.ITextModel;
    const monacoPosition = { lineNumber: 2, column: 13 } as monaco.Position;
    const token = {} as monaco.CancellationToken;

    test('flushes pending changes and converts the position', async () => {
        const request = vi.fn().mockResolvedValue(markdownHover('x: 5'));
        const flushPendingChanges = vi.fn();
        const provider = createHoverProvider({ request, flushPendingChanges });
        await provider.provideHover(model, monacoPosition, token);
        expect(flushPendingChanges).toHaveBeenCalledWith(uri);
        expect(request).toHaveBeenCalledWith({
            textDocument: { uri },
            position,
            verbosityLevel: 0,
        });
    });

    test('returns null when the request fails', async () => {
        const request = vi.fn().mockRejectedValue(new Error('boom'));
        const error = vi.spyOn(console, 'error').mockImplementation(() => {});
        const provider = createHoverProvider({
            request,
            flushPendingChanges: () => {},
        });
        expect(
            await provider.provideHover(model, monacoPosition, token),
        ).toBeNull();
        expect(error).toHaveBeenCalled();
        error.mockRestore();
    });
});
