import * as monaco from 'monaco-editor-core';

import type { Theme } from './theme';

/** The Monaco language of the diagnostics tab, which colours each line's severity. */
export const diagnosticsLanguageID = 'escalier-diagnostics';

/**
 * Registers the diagnostics tab's language. It reads each line as projectDiagnosticsText
 * writes it, `path:line:column: severity: message`, and gives the location and the
 * severity their own tokens.
 */
export function registerDiagnosticsLanguage(): void {
    monaco.languages.register({ id: diagnosticsLanguageID });
    monaco.languages.setMonarchTokensProvider(diagnosticsLanguageID, {
        tokenizer: {
            root: [
                [/[^:\s][^:]*:\d+:\d+:/, 'diagnostic.location'],
                [/\s*error:/, 'diagnostic.error'],
                [/\s*warning:/, 'diagnostic.warning'],
                [/\s*(?:info|hint):/, 'diagnostic.info'],
                [/.+/, ''],
            ],
        },
    });
}

/** The colours of the diagnostics tab's tokens in each theme. */
export const diagnosticsTokenRules: Record<
    Theme,
    monaco.editor.ITokenThemeRule[]
> = {
    light: [
        { token: 'diagnostic.location', foreground: '6e6e6e' },
        { token: 'diagnostic.error', foreground: 'd11717', fontStyle: 'bold' },
        {
            token: 'diagnostic.warning',
            foreground: 'a76d00',
            fontStyle: 'bold',
        },
        { token: 'diagnostic.info', foreground: '1a6fd1', fontStyle: 'bold' },
    ],
    dark: [
        { token: 'diagnostic.location', foreground: '9d9d9d' },
        { token: 'diagnostic.error', foreground: 'f14c4c', fontStyle: 'bold' },
        {
            token: 'diagnostic.warning',
            foreground: 'cca700',
            fontStyle: 'bold',
        },
        { token: 'diagnostic.info', foreground: '3794ff', fontStyle: 'bold' },
    ],
};
