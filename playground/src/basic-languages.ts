import * as monaco from 'monaco-editor-core';
import * as javascript from 'monaco-editor/esm/vs/basic-languages/javascript/javascript.js';
import * as typescript from 'monaco-editor/esm/vs/basic-languages/typescript/typescript.js';

// monaco-editor ships JSON support only as a language service that runs in a worker,
// so the playground defines its own tokenizer.
import * as json from './json-language';
// TOML has no basic-languages definition either, so the playground defines its own.
import * as toml from './toml-language';

type BasicLanguage = {
    conf: monaco.languages.LanguageConfiguration;
    language: monaco.languages.IMonarchLanguage;
};

const basicLanguages: Readonly<Record<string, BasicLanguage>> = {
    javascript,
    typescript,
    json,
    toml,
};

/**
 * Registers the `javascript`, `typescript`, `json` and `toml` languages, each with a
 * Monarch tokenizer and a language configuration.
 */
export function registerBasicLanguages() {
    for (const [id, { conf, language }] of Object.entries(basicLanguages)) {
        monaco.languages.register({ id });
        monaco.languages.setMonarchTokensProvider(id, language);
        monaco.languages.setLanguageConfiguration(id, conf);
    }
}
