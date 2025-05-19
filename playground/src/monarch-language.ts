/* eslint-disable */
import type * as monaco from 'monaco-editor-core';

/**
 * Custom language definition from https://microsoft.github.io/monaco-editor/monarch.html
 * (in Monarch format)
 */
export const monarchLanguage: monaco.languages.IMonarchLanguage = {
    // biome-ignore format:
    keywords: [
        // declarations
        'var', 'val', 'fn', 'type', 'class', 'enum', 'extends', 'implements',
        'export', 'import', 'declare', 'namespace',

        // control flow
        'if', 'else', 'match', 'for', 'return',  'throw', 'try', 'catch', 'finally',

        // type modifiers
        'readonly', 'mutable',

        // function modifiers
        'async', 'await', 'gen', 'yield',
    ],

    // biome-ignore format:
    typeKeywords: [
        'boolean', 'number', 'string',
    ],

    // biome-ignore format:
    operators: [
        '+', '-', '*', '/',
        '==', '!=', '>', '>=', '<', '<=',
        '&&', '||', '!',
        '=', '+=', '-=', '*=', '/=',
        '=>', '->',
    ],

    // we include these common regular expressions
    symbols: /[=><!~?:&|+\-*\/\^%]+/,

    // C# style strings
    escapes:
        /\\(?:[abfnrtv\\"']|x[0-9A-Fa-f]{1,4}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8})/,

    // The main tokenizer for our languages
    tokenizer: {
        root: [
            // identifiers and keywords
            [
                /[a-z_$][\w$]*/,
                {
                    cases: {
                        '@keywords': 'keyword',
                        '@typeKeywords': 'keyword',
                        '@default': 'identifier',
                    },
                },
            ],
            [/[A-Z][\w\$]*/, 'type.identifier'], // to show class names nicely

            // whitespace
            { include: '@whitespace' },

            // delimiters and operators
            [/[{}()\[\]]/, '@brackets'],
            [/[<>](?!@symbols)/, '@brackets'],
            [
                /@symbols/,
                {
                    cases: {
                        '@operators': 'operator',
                        '@default': '',
                    },
                },
            ],

            // @ annotations.
            // As an example, we emit a debugging log message on these tokens.
            // Note: message are supressed during the first load -- change some lines to see them.
            [
                /@\s*[a-zA-Z_\$][\w\$]*/,
                { token: 'annotation', log: 'annotation token: $0' },
            ],

            // numbers
            [/\d*\.\d+([eE][\-+]?\d+)?/, 'number.float'],
            [/0[xX][0-9a-fA-F]+/, 'number.hex'],
            [/\d+/, 'number'],

            // delimiter: after number because of .\d floats
            [/[;,.]/, 'delimiter'],

            // strings
            [/"([^"\\]|\\.)*$/, 'string.invalid'], // non-teminated string
            [/"/, { token: 'string.quote', bracket: '@open', next: '@string' }],
            [/`/, 'string', '@string_backtick'],

            // characters
            [/'[^\\']'/, 'string'],
            [/(')(@escapes)(')/, ['string', 'string.escape', 'string']],
            [/'/, 'string.invalid'],
        ],

        comment: [
            [/[^\/*]+/, 'comment'],
            [/\/\*/, 'comment', '@push'], // nested comment
            ['\\*/', 'comment', '@pop'],
            [/[\/*]/, 'comment'],
        ],

        string: [
            [/[^\\"]+/, 'string'],
            [/@escapes/, 'string.escape'],
            [/\\./, 'string.escape.invalid'],
            [/"/, { token: 'string.quote', bracket: '@close', next: '@pop' }],
        ],

        string_backtick: [
            [/\$\{/, { token: 'delimiter.bracket', next: '@bracketCounting' }],
            [/[^\\`$]+/, 'string'],
            [/@escapes/, 'string.escape'],
            [/\\./, 'string.escape.invalid'],
            [/`/, 'string', '@pop'],
        ],

        bracketCounting: [
            [/\{/, 'delimiter.bracket', '@bracketCounting'],
            [/\}/, 'delimiter.bracket', '@pop'],
            // { include: 'common' },
        ],

        whitespace: [
            [/[ \t\r\n]+/, 'white'],
            [/\/\*/, 'comment', '@comment'],
            [/\/\/.*$/, 'comment'],
        ],
    },
};
