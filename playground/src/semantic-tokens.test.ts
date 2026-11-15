import { afterEach, describe, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { coalesced, modifierTokenRules } from './semantic-tokens';

const legend: lsp.SemanticTokensLegend = {
    tokenTypes: ['variable', 'parameter'],
    tokenModifiers: ['mutable'],
};

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

    test('styles every combination the modifier bits can encode', () => {
        const serverLegend: lsp.SemanticTokensLegend = {
            tokenTypes: ['variable'],
            tokenModifiers: ['mutable', 'reassignable'],
        };
        const rules = modifierTokenRules(serverLegend);
        // Monaco reads a token's modifier bits and joins the set modifiers, in legend
        // order, after its type to form the theme key a rule must match.
        const themeKey = (bits: number) =>
            [
                'variable',
                ...serverLegend.tokenModifiers.filter(
                    (_, i) => bits & (1 << i),
                ),
            ].join('.');
        const fontStyleFor = (bits: number) =>
            rules.find((rule) => rule.token === themeKey(bits))?.fontStyle;

        expect(fontStyleFor(0b01)).toBe('underline');
        expect(fontStyleFor(0b10)).toBe('bold');
        expect(fontStyleFor(0b11)).toBe('underline bold');
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
