import { describe, expect, test, vi } from 'vitest';
import type * as lsp from 'vscode-languageserver-protocol';

import { convertSignatureHelp, provideSignatureHelp } from './signature-help';

const position: lsp.Position = { line: 0, character: 12 };
const uri = 'file:///bin/main.esc';

describe('provideSignatureHelp', () => {
    test('sends the document and position', async () => {
        const getSignatureHelp = vi.fn().mockResolvedValue(null);
        await provideSignatureHelp(getSignatureHelp, uri, position);
        expect(getSignatureHelp).toHaveBeenCalledWith({
            textDocument: { uri },
            position,
        });
    });

    test('returns null when the server returns null', async () => {
        const getSignatureHelp = vi.fn().mockResolvedValue(null);
        const result = await provideSignatureHelp(
            getSignatureHelp,
            uri,
            position,
        );
        expect(result).toBeNull();
    });

    test('converts every overload with its parameter ranges', async () => {
        const help: lsp.SignatureHelp = {
            signatures: [
                {
                    label: 'dup(value: number) -> number',
                    parameters: [{ label: [4, 17] }],
                    activeParameter: 1,
                },
                {
                    label: 'dup(value: string, times: number) -> string',
                    parameters: [{ label: [4, 17] }, { label: [19, 32] }],
                    activeParameter: 1,
                },
            ],
            activeSignature: 1,
            activeParameter: 1,
        };
        const getSignatureHelp = vi.fn().mockResolvedValue(help);
        const result = await provideSignatureHelp(
            getSignatureHelp,
            uri,
            position,
        );
        expect(result).toEqual({
            signatures: [
                {
                    label: 'dup(value: number) -> number',
                    documentation: undefined,
                    parameters: [{ label: [4, 17], documentation: undefined }],
                    activeParameter: 1,
                },
                {
                    label: 'dup(value: string, times: number) -> string',
                    documentation: undefined,
                    parameters: [
                        { label: [4, 17], documentation: undefined },
                        { label: [19, 32], documentation: undefined },
                    ],
                    activeParameter: 1,
                },
            ],
            activeSignature: 1,
            activeParameter: 1,
        });
    });
});

describe('convertSignatureHelp', () => {
    test('returns null when there is no signature', () => {
        expect(convertSignatureHelp({ signatures: [] })).toBeNull();
    });

    test('defaults the active signature and parameter to 0', () => {
        const result = convertSignatureHelp({
            signatures: [{ label: 'f() -> number' }],
        });
        expect(result).toEqual({
            signatures: [
                {
                    label: 'f() -> number',
                    documentation: undefined,
                    parameters: [],
                    activeParameter: undefined,
                },
            ],
            activeSignature: 0,
            activeParameter: 0,
        });
    });

    test('keeps string labels and converts documentation', () => {
        const result = convertSignatureHelp({
            signatures: [
                {
                    label: 'f(x: number) -> number',
                    documentation: {
                        kind: 'markdown',
                        value: 'Returns `x`.',
                    },
                    parameters: [
                        { label: 'x: number', documentation: 'The input.' },
                    ],
                },
            ],
        });
        expect(result?.signatures[0]).toEqual({
            label: 'f(x: number) -> number',
            documentation: { value: 'Returns `x`.' },
            parameters: [{ label: 'x: number', documentation: 'The input.' }],
            activeParameter: undefined,
        });
    });
});
