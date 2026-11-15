import * as monaco from 'monaco-editor-core';
import { beforeEach, describe, expect, test, vi } from 'vitest';

import { registerBasicLanguages } from './basic-languages';

vi.mock('monaco-editor-core');

describe('registerBasicLanguages', () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });

    // Each language asserts one token or configuration detail that is specific to
    // it, so a tokenizer wired to the wrong definition fails here.
    const cComments = { lineComment: '//', blockComment: ['/*', '*/'] };
    test.each([
        {
            id: 'javascript',
            tokenPostfix: '.js',
            keyword: 'function',
            comments: cComments,
        },
        {
            id: 'typescript',
            tokenPostfix: '.ts',
            keyword: 'interface',
            comments: cComments,
        },
        // JSON has no comment syntax.
        {
            id: 'json',
            tokenPostfix: '.json',
            keyword: undefined,
            comments: undefined,
        },
        {
            id: 'toml',
            tokenPostfix: '.toml',
            keyword: undefined,
            comments: { lineComment: '#' },
        },
    ])(
        'registers $id with a Monarch tokenizer',
        ({ id, tokenPostfix, keyword, comments }) => {
            registerBasicLanguages();

            expect(monaco.languages.register).toHaveBeenCalledWith({ id });

            const tokenizerCall = vi
                .mocked(monaco.languages.setMonarchTokensProvider)
                .mock.calls.find(([languageId]) => languageId === id);
            expect(tokenizerCall).toBeDefined();
            const language =
                tokenizerCall?.[1] as monaco.languages.IMonarchLanguage;
            expect(language.tokenPostfix).toBe(tokenPostfix);
            expect(language.tokenizer.root).toBeDefined();
            if (keyword !== undefined) {
                expect(language.keywords).toContain(keyword);
            }

            const confCall = vi
                .mocked(monaco.languages.setLanguageConfiguration)
                .mock.calls.find(([languageId]) => languageId === id);
            expect(confCall?.[1].brackets).toContainEqual(['{', '}']);
            expect(confCall?.[1].comments).toEqual(comments);
        },
    );
});
