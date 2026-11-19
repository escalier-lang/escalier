import { execFileSync } from 'node:child_process';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

import { monacoCorePlugin } from './monaco-core-plugin';

/**
 * Returns the commit the playground is built from: GITHUB_SHA in a GitHub Actions run,
 * or else git's HEAD, or "unknown" when neither is available.
 */
function buildCommit(): string {
    if (process.env.GITHUB_SHA) {
        return process.env.GITHUB_SHA;
    }
    try {
        return execFileSync('git', ['rev-parse', 'HEAD'], {
            encoding: 'utf8',
        }).trim();
    } catch {
        return 'unknown';
    }
}

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
    // The language server's worker is a module worker, which the page starts with
    // `new Worker(url, { type: 'module' })`.
    worker: { format: 'es' },
    base: process.env.APP_ENV === 'prod' ? '/escalier/' : '/',
    define: {
        // A bug report names the build it came from.
        'import.meta.env.VITE_PLAYGROUND_COMMIT': JSON.stringify(buildCommit()),
    },
});
