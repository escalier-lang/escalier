import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

import { monacoCorePlugin } from './monaco-core-plugin';

// https://vite.dev/config/
export default defineConfig({
    // The basic-languages modules import monaco-editor's own editor API. Resolving that
    // import to monaco-editor-core keeps a single Monaco instance in the page.
    plugins: [react(), monacoCorePlugin()],
    optimizeDeps: {
        // Pre-bundling would copy monaco-editor's editor API into the bundle before
        // monacoCorePlugin could redirect it.
        exclude: ['monaco-editor'],
    },
    base: process.env.APP_ENV === 'prod' ? '/escalier/' : '/',
});
