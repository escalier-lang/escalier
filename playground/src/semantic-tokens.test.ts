import type * as monaco from 'monaco-editor-core';
import { afterEach, describe, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import {
    coalesced,
    createSemanticTokensProvider,
    modifierTokenRules,
} from './semantic-tokens';

const uri = 'file:///lib/index.esc';
const legend: lsp.SemanticTokensLegend = {
    tokenTypes: ['variable', 'parameter'],
    tokenModifiers: ['mutable'],
};
const model = { uri: { toString: () => uri } } as monaco.editor.ITextModel;
const cancellation = {} as monaco.CancellationToken;
const onDidChange = (() => ({
    dispose() {},
})) as unknown as monaco.IEvent<void>;

describe('createSemanticTokensProvider', () => {
    test('returns the server tokens as a Uint32Array', async () => {
        const request = vi.fn().mockResolvedValue({ data: [0, 4, 5, 0, 1] });
        const provider = createSemanticTokensProvider({
            legend,
            request,
            hasPendingChange: () => false,
            onDidChange,
        });

        const result = await provider.provideDocumentSemanticTokens(
            model,
            null,
            cancellation,
        );

        expect(request).toHaveBeenCalledWith({ textDocument: { uri } });
        expect(result).toEqual({
            resultId: undefined,
            data: new Uint32Array([0, 4, 5, 0, 1]),
        });
        expect(provider.getLegend()).toBe(legend);
        expect(provider.onDidChange).toBe(onDidChange);
        // The provider keeps no state per result, so releasing one does nothing.
        expect(() =>
            provider.releaseDocumentSemanticTokens(undefined),
        ).not.toThrow();
    });

    test('returns null when the server returns no tokens', async () => {
        const provider = createSemanticTokensProvider({
            legend,
            request: vi.fn().mockResolvedValue(null),
            hasPendingChange: () => false,
            onDidChange,
        });

        const result = await provider.provideDocumentSemanticTokens(
            model,
            null,
            cancellation,
        );

        expect(result).toBeNull();
    });

    test('declines with a busy error while a change is pending', async () => {
        const request = vi.fn();
        const hasPendingChange = vi.fn().mockReturnValue(true);
        const provider = createSemanticTokensProvider({
            legend,
            request,
            hasPendingChange,
            onDidChange,
        });

        await expect(
            provider.provideDocumentSemanticTokens(model, null, cancellation),
        ).rejects.toThrow('busy: a change is waiting to be sent');
        expect(hasPendingChange).toHaveBeenCalledWith(uri);
        expect(request).not.toHaveBeenCalled();
    });
});

describe('coalesced', () => {
    afterEach(() => {
        vi.useRealTimers();
    });

    test('calls the function once for a burst of calls', () => {
        vi.useFakeTimers();
        const fn = vi.fn();
        const schedule = coalesced(fn);

        schedule();
        schedule();
        schedule();
        expect(fn).not.toHaveBeenCalled();

        vi.runAllTimers();
        expect(fn).toHaveBeenCalledTimes(1);

        schedule();
        vi.runAllTimers();
        expect(fn).toHaveBeenCalledTimes(2);
    });
});

describe('modifierTokenRules', () => {
    test('styles each token type by every combination of its modifiers', () => {
        expect(
            modifierTokenRules({
                tokenTypes: ['variable', 'parameter'],
                tokenModifiers: ['mutable', 'reassignable'],
            }),
        ).toEqual([
            { token: 'variable.mutable', fontStyle: 'underline' },
            { token: 'parameter.mutable', fontStyle: 'underline' },
            { token: 'variable.reassignable', fontStyle: 'bold' },
            { token: 'parameter.reassignable', fontStyle: 'bold' },
            {
                token: 'variable.mutable.reassignable',
                fontStyle: 'underline bold',
            },
            {
                token: 'parameter.mutable.reassignable',
                fontStyle: 'underline bold',
            },
        ]);
    });

    test('underlines each token type when the legend has only mutable', () => {
        expect(modifierTokenRules(legend)).toEqual([
            { token: 'variable.mutable', fontStyle: 'underline' },
            { token: 'parameter.mutable', fontStyle: 'underline' },
        ]);
    });

    test('returns no rules without a legend', () => {
        expect(modifierTokenRules(undefined)).toEqual([]);
    });

    test('returns no rules when the legend has no styled modifier', () => {
        expect(
            modifierTokenRules({
                tokenTypes: ['variable'],
                tokenModifiers: ['readonly'],
            }),
        ).toEqual([]);
    });

    test('ignores a modifier named like an object property', () => {
        expect(
            modifierTokenRules({
                tokenTypes: ['variable'],
                tokenModifiers: ['constructor', 'toString'],
            }),
        ).toEqual([]);
    });
});
