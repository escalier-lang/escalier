import type * as monaco from 'monaco-editor-core';

export const conf: monaco.languages.LanguageConfiguration = {
    comments: {
        lineComment: '#',
    },
    brackets: [
        ['{', '}'],
        ['[', ']'],
    ],
    autoClosingPairs: [
        { open: '{', close: '}', notIn: ['string'] },
        { open: '[', close: ']', notIn: ['string'] },
        { open: '"', close: '"', notIn: ['string'] },
        { open: "'", close: "'", notIn: ['string'] },
    ],
};

/**
 * A Monarch tokenizer for TOML. Its tokens use the names that Monaco's `vs` and
 * `vs-dark` themes colour, such as `type` for a table header, `key` for a bare key,
 * `string`, `number` and `keyword` for the boolean literals.
 */
export const language: monaco.languages.IMonarchLanguage = {
    defaultToken: '',
    tokenPostfix: '.toml',
    tokenizer: {
        root: [
            [/#.*$/, 'comment'],
            // A table or array-of-tables header, such as `[project]` or `[[bin]]`.
            [/\[\[.*?\]\]/, 'type'],
            [/\[.*?\]/, 'type'],
            // A bare or quoted key up to its `=`.
            [
                /(^\s*)([\w.-]+|"[^"]*"|'[^']*')(\s*)(=)/,
                ['', 'key', '', 'delimiter'],
            ],
            { include: '@value' },
        ],
        value: [
            [/\s+/, 'white'],
            [/"""/, 'string', '@multilineBasic'],
            [/'''/, 'string', '@multilineLiteral'],
            [/"(?:[^"\\]|\\.)*"/, 'string'],
            [/'[^']*'/, 'string'],
            [/\b(?:true|false)\b/, 'keyword'],
            // A date or time, such as `2026-11-13T12:00:00Z`.
            [
                /\d{4}-\d{2}-\d{2}(?:[Tt ][\d:.]+(?:[Zz]|[+-]\d{2}:\d{2})?)?/,
                'number',
            ],
            [/[+-]?(?:0x[0-9a-fA-F_]+|0o[0-7_]+|0b[01_]+)/, 'number'],
            [/[+-]?(?:\d[\d_]*)(?:\.\d[\d_]*)?(?:[eE][+-]?\d+)?/, 'number'],
            [/[+-]?(?:inf|nan)\b/, 'number'],
            [/[{}[\],]/, 'delimiter'],
        ],
        multilineBasic: [
            [/[^"]+/, 'string'],
            [/"""/, 'string', '@pop'],
            [/"/, 'string'],
        ],
        multilineLiteral: [
            [/[^']+/, 'string'],
            [/'''/, 'string', '@pop'],
            [/'/, 'string'],
        ],
    },
};
