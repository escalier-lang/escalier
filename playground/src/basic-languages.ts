import * as monaco from 'monaco-editor-core';
import * as javascript from 'monaco-editor/esm/vs/basic-languages/javascript/javascript.js';
import * as typescript from 'monaco-editor/esm/vs/basic-languages/typescript/typescript.js';

type BasicLanguage = {
    conf: monaco.languages.LanguageConfiguration;
    language: monaco.languages.IMonarchLanguage;
};

/** The language ids registered by registerBasicLanguages, mapped to their definitions. */
export const basicLanguages: Readonly<Record<string, BasicLanguage>> = {
    javascript,
    typescript,
};

/**
 * Registers a Monarch tokenizer and a language configuration for each language in
 * basicLanguages.
 */
export function registerBasicLanguages() {
    for (const [id, { conf, language }] of Object.entries(basicLanguages)) {
        monaco.languages.register({ id });
        monaco.languages.setMonarchTokensProvider(id, language);
        monaco.languages.setLanguageConfiguration(id, conf);
    }
}
