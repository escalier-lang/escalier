import * as monaco from 'monaco-editor-core';
import { beforeEach, describe, expect, test, vi } from 'vitest';

import { registerBasicLanguages } from './basic-languages';

vi.mock('monaco-editor-core');

describe('registerBasicLanguages', () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });

    test.each([
        { id: 'javascript', tokenPostfix: '.js' },
        { id: 'typescript', tokenPostfix: '.ts' },
    ])('registers $id with a Monarch tokenizer', ({ id, tokenPostfix }) => {
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
        expect(language.keywords).toContain('function');

        const confCall = vi
            .mocked(monaco.languages.setLanguageConfiguration)
            .mock.calls.find(([languageId]) => languageId === id);
        expect(confCall?.[1].comments).toEqual({
            lineComment: '//',
            blockComment: ['/*', '*/'],
        });
    });
});
