import type { Plugin } from 'vite';

const basicLanguagesDir = '/monaco-editor/esm/vs/basic-languages/';

/**
 * Returns a Vite plugin that resolves the editor API import inside monaco-editor's
 * basic-languages modules to `monaco-editor-core`.
 *
 * The plugin only sees modules that Vite transforms itself, so a dev server must list
 * `monaco-editor` in `optimizeDeps.exclude` and a Vitest project must inline it.
 */
export function monacoCorePlugin(): Plugin {
    return {
        name: 'escalier:monaco-core',
        enforce: 'pre',
        resolveId(source, importer) {
            if (
                importer?.includes(basicLanguagesDir) &&
                source.endsWith('/editor/editor.api.js')
            ) {
                return this.resolve('monaco-editor-core', importer, {
                    skipSelf: true,
                });
            }
            return null;
        },
    };
}
