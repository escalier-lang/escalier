import type * as monaco from 'monaco-editor-core';

export const conf: monaco.languages.LanguageConfiguration = {
    brackets: [
        ['{', '}'],
        ['[', ']'],
    ],
    autoClosingPairs: [
        { open: '{', close: '}', notIn: ['string'] },
        { open: '[', close: ']', notIn: ['string'] },
        { open: '"', close: '"', notIn: ['string'] },
    ],
};

/**
 * A Monarch tokenizer for JSON. Its tokens use the `.json` names that Monaco's `vs`
 * and `vs-dark` themes colour, such as `string.key.json` and `string.value.json`.
 */
export const language: monaco.languages.IMonarchLanguage = {
    defaultToken: 'invalid',
    tokenPostfix: '.json',
    tokenizer: {
        root: [
            [/\s+/, 'white'],
            // A string followed by `:` on the same line is an object key.
            [/"(?:[^"\\]|\\.)*"(?=\s*:)/, 'string.key'],
            [/"(?:[^"\\]|\\.)*"/, 'string.value'],
            [/-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/, 'number'],
            [/\b(?:true|false|null)\b/, 'keyword'],
            [/[{}]/, 'delimiter.bracket'],
            [/[[\]]/, 'delimiter.array'],
            [/,/, 'delimiter.comma'],
            [/:/, 'delimiter.colon'],
        ],
    },
};
