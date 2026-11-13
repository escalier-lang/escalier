// monaco-editor ships these modules without type declarations.

declare module 'monaco-editor/esm/vs/basic-languages/javascript/javascript.js' {
    import type { languages } from 'monaco-editor-core';
    export const conf: languages.LanguageConfiguration;
    export const language: languages.IMonarchLanguage;
}

declare module 'monaco-editor/esm/vs/basic-languages/typescript/typescript.js' {
    import type { languages } from 'monaco-editor-core';
    export const conf: languages.LanguageConfiguration;
    export const language: languages.IMonarchLanguage;
}
